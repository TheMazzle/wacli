package app

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// groupCacheTTL bepaalt hoe lang een ECHT resultaat hergebruikt wordt: een
// succesvolle opvraging, mét een groep gevonden, of mét bevestigd dat de
// groep onbekend is ((nil, nil) van whatsmeow). Groepsnamen en
// deelnemerslijsten veranderen zelden; events.GroupInfo werkt de opslag
// bovendien direct bij, dus een ruime TTL kost niets aan actualiteit.
const groupCacheTTL = 6 * time.Hour

// groupCacheErrorTTL bepaalt hoe lang een MISLUKTE opvraging (netwerkfout,
// timeout, rate limit) geldt voordat een volgend bericht het opnieuw
// probeert. Een fout is geen kennis over de groep — het is een mislukte
// poging. Die voor de volle groupCacheTTL cachen zou een enkele netwerkblip
// de groepsmetadata-verzameling voor de rest van de sync laten blinderen: een
// resilience-regressie t.o.v. het oude ongecachete gedrag, waarbij alleen dat
// ene bericht de metadata miste en het volgende bericht het meteen opnieuw
// probeerde. Eén minuut is kort genoeg om die blindheid te beperken, maar
// lang genoeg om een rate-limit-storm (honderden berichten in dezelfde groep
// binnen enkele seconden) niet alsnog honderden retries te laten doen.
const groupCacheErrorTTL = 1 * time.Minute

type groupCacheEntry struct {
	info    *types.GroupInfo
	fetched time.Time
	isErr   bool // true: deze entry legt een mislukte poging vast, geen echte kennis
}

// groupInfoCached geeft groepsinfo terug met hooguit één netwerkronde per groep
// per groupCacheTTL (of per groupCacheErrorTTL na een mislukte poging).
//
// storeParsedMessage riep GetGroupInfo aan voor elk groepsbericht. In whatsmeow
// is dat een ongecachete IQ, dus een history sync van 500 berichten in één groep
// was 500 opeenvolgende netwerkrondes — dezelfde fout die repairChatNames al op
// rate limits zag stuklopen.
//
// Mislukte opvragingen worden ook onthouden: zonder dat blijft een onbekende
// groep alsnog een ronde per bericht kosten. Maar een mislukte opvraging
// (err != nil) is geen bevestigde "onbekende groep" — die twee worden apart
// gehouden (isErr) en met een andere TTL gecached, zodat een tijdelijke
// storing niet urenlang groepsmetadata blokkeert.
func (a *App) groupInfoCached(ctx context.Context, jid types.JID) (*types.GroupInfo, bool) {
	key := jid.ToNonAD().String()

	a.groupCacheMu.Lock()
	if a.groupCache == nil {
		a.groupCache = map[string]groupCacheEntry{}
	}
	entry, found := a.groupCache[key]
	a.groupCacheMu.Unlock()

	if found {
		ttl := groupCacheTTL
		if entry.isErr {
			ttl = groupCacheErrorTTL
		}
		if time.Since(entry.fetched) < ttl {
			return entry.info, entry.info != nil
		}
	}

	info, err := a.wa.GetGroupInfo(ctx, jid)
	isErr := err != nil
	if isErr {
		info = nil
	}

	a.groupCacheMu.Lock()
	a.groupCache[key] = groupCacheEntry{info: info, fetched: time.Now(), isErr: isErr}
	a.groupCacheMu.Unlock()

	return info, info != nil
}

// invalidateGroupCache verwijdert een groep uit de cache, zodat de volgende
// aanroep verse info ophaalt. Aan te roepen bij een events.GroupInfo.
func (a *App) invalidateGroupCache(jid types.JID) {
	a.groupCacheMu.Lock()
	defer a.groupCacheMu.Unlock()
	delete(a.groupCache, jid.ToNonAD().String())
}
