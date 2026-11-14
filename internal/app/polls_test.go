package app

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/steipete/wacli/internal/store"
	"github.com/steipete/wacli/internal/wa"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Een poll moet als leesbaar bericht plus poll-rij landen, en een stem mag
// geen eigen (lege) berichtrij opleveren.
func TestStorePollAndLiveVote(t *testing.T) {
	a := newTestApp(t)
	a.wa = newFakeWA()
	ctx := context.Background()

	chat := types.JID{User: "120363000000000001", Server: types.GroupServer}
	voter := types.JID{User: "31600000001", Server: types.DefaultUserServer}
	ts := time.Date(2026, 11, 14, 11, 22, 0, 0, time.UTC)

	pollEvt := &events.Message{
		Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: voter}, ID: "POLL1", Timestamp: ts},
		Message: &waProto.Message{PollCreationMessageV3: &waProto.PollCreationMessage{
			Name:                   proto.String("Etentje?"),
			SelectableOptionsCount: proto.Uint32(1),
			Options: []*waProto.PollCreationMessage_Option{
				{OptionName: proto.String("Vrijdag")}, {OptionName: proto.String("Zaterdag")},
			},
		}},
	}
	if err := a.storeParsedMessage(ctx, wa.ParseLiveMessage(pollEvt)); err != nil {
		t.Fatal(err)
	}

	msg, err := a.db.GetMessage(chat.String(), "POLL1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "Etentje?" || msg.DisplayText == "(message)" {
		t.Fatalf("poll message stored as text=%q display=%q", msg.Text, msg.DisplayText)
	}
	if _, err := a.db.GetPoll(chat.String(), "POLL1"); err != nil {
		t.Fatalf("poll row missing: %v", err)
	}

	// Live stem: een PollUpdateMessage die de fake "ontsleutelt".
	hash, _ := hex.DecodeString(store.PollOptionHash("Zaterdag"))
	voteEvt := &events.Message{
		Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: voter}, ID: "VOTE1", Timestamp: ts.Add(time.Minute)},
		Message: &waProto.Message{PollUpdateMessage: &waProto.PollUpdateMessage{
			PollCreationMessageKey: &waProto.MessageKey{ID: proto.String("POLL1")},
		}},
	}
	pm := wa.ParseLiveMessage(voteEvt)
	pm.Votes = []wa.PollVote{{VoterJID: voter.String(), OptionHashes: [][]byte{hash}, Timestamp: voteEvt.Info.Timestamp}}
	if err := a.storeParsedMessage(ctx, pm); err != nil {
		t.Fatal(err)
	}

	if _, err := a.db.GetMessage(chat.String(), "VOTE1"); err == nil {
		t.Fatal("a vote must not be stored as a message row")
	}
	votes, err := a.db.ListPollVotes(chat.String(), "POLL1")
	if err != nil {
		t.Fatal(err)
	}
	if len(votes) != 1 || votes[0].VoterJID != voter.String() || votes[0].OptionHashes[0] != store.PollOptionHash("Zaterdag") {
		t.Fatalf("votes = %+v", votes)
	}
}
