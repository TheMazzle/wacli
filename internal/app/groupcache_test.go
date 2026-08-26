package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/steipete/wacli/internal/wa"
	"go.mau.fi/whatsmeow/types"
)

// Regressie: storeParsedMessage deed een netwerkronde per groepsbericht.
// Een history sync van 500 berichten in één groep was 500 IQ's.
func TestGroupInfoCachedHitsNetworkOncePerGroup(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	group := types.JID{User: "120363", Server: types.GroupServer}
	f.groups[group] = &types.GroupInfo{
		GroupName: types.GroupName{Name: "Amsteldorpkidz"},
	}

	ctx := context.Background()
	for i := 0; i < 25; i++ {
		info, ok := a.groupInfoCached(ctx, group)
		if !ok || info == nil {
			t.Fatalf("aanroep %d: geen groepsinfo", i)
		}
		if info.GroupName.Name != "Amsteldorpkidz" {
			t.Fatalf("aanroep %d: naam = %q", i, info.GroupName.Name)
		}
	}

	if f.groupInfoCalls != 1 {
		t.Errorf("GetGroupInfo %d keer aangeroepen, wil 1", f.groupInfoCalls)
	}
}

func TestGroupInfoCachedSeparatesGroups(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	one := types.JID{User: "111", Server: types.GroupServer}
	two := types.JID{User: "222", Server: types.GroupServer}
	f.groups[one] = &types.GroupInfo{GroupName: types.GroupName{Name: "Een"}}
	f.groups[two] = &types.GroupInfo{GroupName: types.GroupName{Name: "Twee"}}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if info, ok := a.groupInfoCached(ctx, one); !ok || info.GroupName.Name != "Een" {
			t.Fatal("groep een niet correct")
		}
		if info, ok := a.groupInfoCached(ctx, two); !ok || info.GroupName.Name != "Twee" {
			t.Fatal("groep twee niet correct")
		}
	}

	if f.groupInfoCalls != 2 {
		t.Errorf("GetGroupInfo %d keer aangeroepen, wil 2 (één per groep)", f.groupInfoCalls)
	}
}

// Een groep die niets teruggeeft mag niet elke keer opnieuw bevraagd worden;
// anders is een onbekende groep alsnog een ronde per bericht.
func TestGroupInfoCachedRemembersMisses(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	unknown := types.JID{User: "999", Server: types.GroupServer}

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, ok := a.groupInfoCached(ctx, unknown); ok {
			t.Fatalf("aanroep %d gaf onverwacht groepsinfo", i)
		}
	}

	if f.groupInfoCalls != 1 {
		t.Errorf("GetGroupInfo %d keer aangeroepen voor een onbekende groep, wil 1", f.groupInfoCalls)
	}
}

// Fix-round 1, bevinding 1: een foutresultaat (netwerkblip, rate limit) is
// geen kennis over de groep en mag niet de volle groupCacheTTL blijven
// hangen. Na het verstrijken van de KORTE groupCacheErrorTTL moet de
// volgende aanroep opnieuw een netwerkronde doen.
//
// De klok wordt teruggezet door rechtstreeks aan a.groupCache te schrijven
// (zelfde package als groupcache.go) in plaats van echt te slapen.
func TestGroupInfoCachedRetriesAfterErrorTTL(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f
	f.groupInfoErr = errors.New("tijdelijke netwerkfout")

	group := types.JID{User: "120363", Server: types.GroupServer}
	ctx := context.Background()

	if info, ok := a.groupInfoCached(ctx, group); ok || info != nil {
		t.Fatalf("verwachtte geen groepsinfo bij foutresultaat, kreeg ok=%v info=%v", ok, info)
	}
	if f.groupInfoCalls != 1 {
		t.Fatalf("na eerste mislukte poging: %d aanroepen, wil 1", f.groupInfoCalls)
	}

	// Nog ruim binnen groupCacheErrorTTL: geen nieuwe ronde.
	if _, ok := a.groupInfoCached(ctx, group); ok {
		t.Fatal("onverwacht groepsinfo binnen error-TTL")
	}
	if f.groupInfoCalls != 1 {
		t.Fatalf("binnen error-TTL: %d aanroepen, wil 1", f.groupInfoCalls)
	}

	// Zet de klok van deze entry terug tot net voorbij groupCacheErrorTTL.
	key := group.ToNonAD().String()
	a.groupCacheMu.Lock()
	entry := a.groupCache[key]
	if !entry.isErr {
		a.groupCacheMu.Unlock()
		t.Fatal("entry was niet als foutresultaat gemarkeerd")
	}
	entry.fetched = time.Now().Add(-groupCacheErrorTTL - time.Second)
	a.groupCache[key] = entry
	a.groupCacheMu.Unlock()

	// De storing is voorbij; de groep blijkt nu bekend.
	f.groupInfoErr = nil
	f.groups[group] = &types.GroupInfo{GroupName: types.GroupName{Name: "Amsteldorpkidz"}}

	info, ok := a.groupInfoCached(ctx, group)
	if !ok || info == nil || info.GroupName.Name != "Amsteldorpkidz" {
		t.Fatalf("na verval van error-TTL geen retry: ok=%v info=%v", ok, info)
	}
	if f.groupInfoCalls != 2 {
		t.Errorf("na verval van error-TTL: %d aanroepen, wil 2", f.groupInfoCalls)
	}
}

// Fix-round 1, bevinding 1 (keerzijde): een ECHTE (nil, nil) miss is wél
// bevestigde kennis en moet de volle, lange groupCacheTTL standhouden — ook
// voorbij het punt waarop een foutresultaat allang opnieuw geprobeerd zou
// zijn. Dat onderscheidt de twee TTL's; zonder isErr-vlag zou dit falen.
func TestGroupInfoCachedGenuineMissOutlivesErrorTTLWindow(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	unknown := types.JID{User: "999", Server: types.GroupServer}
	ctx := context.Background()

	if _, ok := a.groupInfoCached(ctx, unknown); ok {
		t.Fatal("verwachtte geen groepsinfo voor onbekende groep")
	}
	if f.groupInfoCalls != 1 {
		t.Fatalf("%d aanroepen, wil 1", f.groupInfoCalls)
	}

	// Klok net voorbij de KORTE error-TTL, ruim binnen de LANGE succes-TTL.
	key := unknown.ToNonAD().String()
	a.groupCacheMu.Lock()
	entry := a.groupCache[key]
	if entry.isErr {
		a.groupCacheMu.Unlock()
		t.Fatal("een echte (nil, nil) miss werd als foutresultaat gemarkeerd")
	}
	entry.fetched = time.Now().Add(-groupCacheErrorTTL - time.Second)
	a.groupCache[key] = entry
	a.groupCacheMu.Unlock()

	if _, ok := a.groupInfoCached(ctx, unknown); ok {
		t.Fatal("onverwacht groepsinfo")
	}
	if f.groupInfoCalls != 1 {
		t.Errorf("echte miss werd opnieuw bevraagd binnen de lange TTL: %d aanroepen, wil 1", f.groupInfoCalls)
	}
}

// Fix-round 1, bevinding 2: de vorige tests riepen allemaal groupInfoCached
// rechtstreeks aan, niet de wiring in storeParsedMessage (sync.go:349). Deze
// test drijft het echte hete pad aan: meerdere berichten in dezelfde groep
// via storeParsedMessage, en verifieert zowel het aanroepgedrag als dat de
// groep/deelnemer-rijen nog steeds in de database landen op het cache-pad.
func TestStoreParsedMessageUsesGroupCache(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	group := types.JID{User: "120363", Server: types.GroupServer}
	member := types.JID{User: "31600000000", Server: types.DefaultUserServer}
	f.groups[group] = &types.GroupInfo{
		GroupName:    types.GroupName{Name: "Amsteldorpkidz"},
		Participants: []types.GroupParticipant{{JID: member}},
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		pm := wa.ParsedMessage{
			Chat:      group,
			ID:        fmt.Sprintf("msg-%d", i),
			SenderJID: member.ToNonAD().String(),
			Timestamp: time.Now(),
			Text:      "hoi",
			// Niet-lege PushName: de fake's ResolveChatName kortsluit dan zonder
			// zelf GetGroupInfo aan te roepen, zodat deze test alleen het
			// aanroepgedrag van storeParsedMessage zelf meet (en niet dat van
			// ResolveChatName, dat in productie sowieso al via een ander pad
			// loopt).
			PushName: "Amsteldorpkidz",
		}
		if err := a.storeParsedMessage(ctx, pm); err != nil {
			t.Fatalf("bericht %d: %v", i, err)
		}
	}

	if f.groupInfoCalls != 1 {
		t.Errorf("storeParsedMessage riep GetGroupInfo %d keer aan voor 5 berichten in dezelfde groep, wil 1", f.groupInfoCalls)
	}

	dbPath := filepath.Join(a.opts.StoreDir, "wacli.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open verificatie-connectie: %v", err)
	}
	defer raw.Close()

	var name string
	if err := raw.QueryRow(`SELECT name FROM groups WHERE jid = ?`, group.ToNonAD().String()).Scan(&name); err != nil {
		t.Fatalf("groep niet in database gevonden: %v", err)
	}
	if name != "Amsteldorpkidz" {
		t.Errorf("groepsnaam in db = %q, wil Amsteldorpkidz", name)
	}

	var participantCount int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM group_participants WHERE group_jid = ?`, group.ToNonAD().String()).Scan(&participantCount); err != nil {
		t.Fatalf("deelnemers niet leesbaar: %v", err)
	}
	if participantCount != 1 {
		t.Errorf("%d deelnemersrijen in db, wil 1", participantCount)
	}
}

// Fix-ronde 2 van 5: storeParsedMessage riep vóór de metadata-cache eerst
// a.wa.ResolveChatName aan voor de chatnaam. In productie
// (internal/wa/client.go, Client.ResolveChatName) doet die voor een
// groeps-JID altijd een EIGEN, ongecachete GetGroupInfo-aanroep — los van de
// cache die de vorige twee ronden aan de metadata-write (regel ~350)
// toevoegden. Het gevolg: elk groepsbericht kostte nog steeds precies één
// netwerkronde, alleen niet meer de tweede.
//
// Met een lege PushName volgt de fake dezelfde volgorde als de echte
// client (eerst de groepstak controleren, pushname komt pas daarna aan
// bod), dus deze test reproduceert het productiegedrag correct — in
// tegenstelling tot TestStoreParsedMessageUsesGroupCache hierboven, die met
// een niet-lege PushName de fake's eigen kortsluiting gebruikte en daardoor
// deze bug niet ving.
func TestStoreParsedMessageResolvesGroupNameFromSingleCachedLookup(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	group := types.JID{User: "120363", Server: types.GroupServer}
	member := types.JID{User: "31600000000", Server: types.DefaultUserServer}
	f.groups[group] = &types.GroupInfo{
		GroupName:    types.GroupName{Name: "Amsteldorpkidz"},
		Participants: []types.GroupParticipant{{JID: member}},
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		pm := wa.ParsedMessage{
			Chat:      group,
			ID:        fmt.Sprintf("naam-msg-%d", i),
			SenderJID: member.ToNonAD().String(),
			Timestamp: time.Now(),
			Text:      "hoi",
			PushName:  "", // leeg: dwingt hetzelfde pad af als de echte client
		}
		if err := a.storeParsedMessage(ctx, pm); err != nil {
			t.Fatalf("bericht %d: %v", i, err)
		}
	}

	if f.groupInfoCalls != 1 {
		t.Errorf("GetGroupInfo %d keer aangeroepen voor naam+metadata van 5 berichten in dezelfde groep, wil 1", f.groupInfoCalls)
	}

	dbPath := filepath.Join(a.opts.StoreDir, "wacli.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open verificatie-connectie: %v", err)
	}
	defer raw.Close()

	var name string
	if err := raw.QueryRow(`SELECT name FROM chats WHERE jid = ?`, group.ToNonAD().String()).Scan(&name); err != nil {
		t.Fatalf("chat niet in database gevonden: %v", err)
	}
	if name != "Amsteldorpkidz" {
		t.Errorf("chatnaam in db = %q, wil Amsteldorpkidz (naamgeving mag niet veranderen)", name)
	}
}
