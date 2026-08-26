package app

import (
	"context"
	"testing"

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
