package store

import (
	"encoding/hex"
	"testing"
)

func TestPollRoundTripAndVotes(t *testing.T) {
	db := openTestDB(t)
	if err := db.UpsertPoll("g@g.us", "p1", "Etentje?", []string{"Vrijdag", "Zaterdag"}, 1); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetPoll("g@g.us", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Question != "Etentje?" || len(p.Options) != 2 || p.Options[0].Hash != PollOptionHash("Vrijdag") {
		t.Fatalf("unexpected poll %+v", p)
	}
	if f, err := db.FindPoll("p1"); err != nil || f.ChatJID != "g@g.us" {
		t.Fatalf("FindPoll = %+v, %v", f, err)
	}
	if _, err := db.GetPoll("g@g.us", "nope"); !IsNotFound(err) {
		t.Fatalf("missing poll: err = %v, want not found", err)
	}

	h := func(name string) []byte {
		b, _ := hex.DecodeString(PollOptionHash(name))
		return b
	}
	// Nieuwe stem vervangt oude; een oudere (dubbel afgeleverde) stem niet.
	must(t, db.RecordPollVote("g@g.us", "p1", "a@s.whatsapp.net", [][]byte{h("Vrijdag")}, 100))
	must(t, db.RecordPollVote("g@g.us", "p1", "a@s.whatsapp.net", [][]byte{h("Zaterdag")}, 200))
	must(t, db.RecordPollVote("g@g.us", "p1", "a@s.whatsapp.net", [][]byte{h("Vrijdag")}, 150))
	must(t, db.RecordPollVote("g@g.us", "p1", "me", nil, 300)) // ingetrokken stem

	votes, err := db.ListPollVotes("g@g.us", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(votes) != 2 {
		t.Fatalf("votes = %d, want 2", len(votes))
	}
	if votes[0].VoterJID != "a@s.whatsapp.net" || len(votes[0].OptionHashes) != 1 || votes[0].OptionHashes[0] != PollOptionHash("Zaterdag") {
		t.Errorf("a's vote = %+v, want Zaterdag", votes[0])
	}
	if votes[1].VoterJID != "me" || len(votes[1].OptionHashes) != 0 {
		t.Errorf("my vote = %+v, want empty", votes[1])
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
