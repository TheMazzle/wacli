package app

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// groupCacheTTL bepaalt hoe lang groepsinfo hergebruikt wordt. Groepsnamen en
// deelnemerslijsten veranderen zelden; events.GroupInfo werkt de opslag
// bovendien direct bij, dus een ruime TTL kost niets aan actualiteit.
const groupCacheTTL = 6 * time.Hour

type groupCacheEntry struct {
	info    *types.GroupInfo
	fetched time.Time
}

// groupInfoCached geeft groepsinfo terug met hooguit één netwerkronde per groep
// per groupCacheTTL.
//
// storeParsedMessage riep GetGroupInfo aan voor elk groepsbericht. In whatsmeow
// is dat een ongecachete IQ, dus een history sync van 500 berichten in één groep
// was 500 opeenvolgende netwerkrondes — dezelfde fout die repairChatNames al op
// rate limits zag stuklopen.
//
// Mislukte opvragingen worden ook onthouden: zonder dat blijft een onbekende
// groep alsnog een ronde per bericht kosten.
func (a *App) groupInfoCached(ctx context.Context, jid types.JID) (*types.GroupInfo, bool) {
	key := jid.ToNonAD().String()

	a.groupCacheMu.Lock()
	if a.groupCache == nil {
		a.groupCache = map[string]groupCacheEntry{}
	}
	entry, found := a.groupCache[key]
	a.groupCacheMu.Unlock()

	if found && time.Since(entry.fetched) < groupCacheTTL {
		return entry.info, entry.info != nil
	}

	info, err := a.wa.GetGroupInfo(ctx, jid)
	if err != nil {
		info = nil
	}

	a.groupCacheMu.Lock()
	a.groupCache[key] = groupCacheEntry{info: info, fetched: time.Now()}
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
