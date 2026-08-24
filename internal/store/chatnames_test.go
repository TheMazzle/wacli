package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestChatsWithUnresolvedNames(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	seed := []struct{ jid, kind, name string }{
		// Naam is letterlijk de JID — dit is wat de oude ResolveChatName opleverde.
		{"152136902033557@lid", "dm", "152136902033557@lid"},
		{"31612345678@s.whatsapp.net", "dm", "31612345678@s.whatsapp.net"},
		// Alleen het gebruikersdeel, ook onbruikbaar.
		{"31687654321@s.whatsapp.net", "dm", "31687654321"},
		// Echte namen moeten met rust gelaten worden.
		{"31611111111@s.whatsapp.net", "dm", "Marieke"},
		{"120363@g.us", "group", "Amsteldorpkidz"},
		// Leeg telt ook als onopgelost.
		{"31622222222@s.whatsapp.net", "dm", ""},
	}
	for _, s := range seed {
		if err := db.UpsertChat(s.jid, s.kind, s.name, now); err != nil {
			t.Fatalf("seed %s: %v", s.jid, err)
		}
	}

	got, err := db.ChatsWithUnresolvedNames()
	if err != nil {
		t.Fatalf("ChatsWithUnresolvedNames: %v", err)
	}

	want := map[string]bool{
		"152136902033557@lid":        true,
		"31612345678@s.whatsapp.net": true,
		"31687654321@s.whatsapp.net": true,
		"31622222222@s.whatsapp.net": true,
	}
	if len(got) != len(want) {
		t.Fatalf("kreeg %d chats, wil %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		if !want[c.JID] {
			t.Errorf("%s zou niet als onopgelost moeten tellen (naam=%q)", c.JID, c.Name)
		}
	}
}

func TestSetChatName(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	jid := "152136902033557@lid"
	if err := db.UpsertChat(jid, "dm", jid, time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.SetChatName(jid, "+31623664613"); err != nil {
		t.Fatalf("SetChatName: %v", err)
	}

	chats, err := db.ListChats("", 10)
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	for _, c := range chats {
		if c.JID == jid {
			if c.Name != "+31623664613" {
				t.Errorf("naam = %q, wil %q", c.Name, "+31623664613")
			}
			return
		}
	}
	t.Fatal("chat niet teruggevonden")
}
