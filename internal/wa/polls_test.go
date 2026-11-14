package wa

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func pollProto(q string, opts ...string) *waProto.PollCreationMessage {
	p := &waProto.PollCreationMessage{Name: proto.String(q), SelectableOptionsCount: proto.Uint32(1)}
	for _, o := range opts {
		p.Options = append(p.Options, &waProto.PollCreationMessage_Option{OptionName: proto.String(o)})
	}
	return p
}

// De bug van 2026-11-14: een poll kwam binnen als lege rij met '(message)'.
func TestParseLiveMessagePollAllVersions(t *testing.T) {
	chat, _ := types.ParseJID("123@s.whatsapp.net")
	for name, msg := range map[string]*waProto.Message{
		"v1": {PollCreationMessage: pollProto("Etentje?", "Vrijdag", "Zaterdag")},
		"v2": {PollCreationMessageV2: pollProto("Etentje?", "Vrijdag", "Zaterdag")},
		"v3": {PollCreationMessageV3: pollProto("Etentje?", "Vrijdag", "Zaterdag")},
		"v5": {PollCreationMessageV5: pollProto("Etentje?", "Vrijdag", "Zaterdag")},
		"v6": {PollCreationMessageV6: pollProto("Etentje?", "Vrijdag", "Zaterdag")},
	} {
		ev := &events.Message{
			Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, ID: "p1", Timestamp: time.Now()},
			Message: msg,
		}
		pm := ParseLiveMessage(ev)
		if pm.Poll == nil {
			t.Fatalf("%s: poll not parsed", name)
		}
		if pm.Poll.Question != "Etentje?" || len(pm.Poll.Options) != 2 || pm.Poll.Options[1] != "Zaterdag" || pm.Poll.SelectableCount != 1 {
			t.Errorf("%s: unexpected poll %+v", name, pm.Poll)
		}
		if pm.Text != "Etentje?" || pm.EventType != "poll" {
			t.Errorf("%s: text=%q event=%q, want question and 'poll'", name, pm.Text, pm.EventType)
		}
	}
}

func TestParseLiveMessagePollVoteHasTarget(t *testing.T) {
	chat, _ := types.ParseJID("123@g.us")
	ev := &events.Message{
		Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat}, ID: "v1"},
		Message: &waProto.Message{PollUpdateMessage: &waProto.PollUpdateMessage{
			PollCreationMessageKey: &waProto.MessageKey{ID: proto.String("p1")},
		}},
	}
	pm := ParseLiveMessage(ev)
	if pm.PollVoteFor != "p1" {
		t.Fatalf("PollVoteFor = %q, want p1", pm.PollVoteFor)
	}
	if pm.Poll != nil {
		t.Fatal("a vote must not parse as a poll")
	}
}

func TestParseHistoryMessagePollWithVotes(t *testing.T) {
	hist := &waWeb.WebMessageInfo{
		Key:              &waProto.MessageKey{ID: proto.String("p1"), RemoteJID: proto.String("g@g.us"), FromMe: proto.Bool(true)},
		MessageTimestamp: proto.Uint64(1700000000),
		Message:          &waProto.Message{PollCreationMessageV3: pollProto("Waar?", "A", "B")},
		PollUpdates: []*waWeb.PollUpdate{
			{
				PollUpdateMessageKey: &waProto.MessageKey{RemoteJID: proto.String("g@g.us"), Participant: proto.String("31600000001@s.whatsapp.net")},
				Vote:                 &waProto.PollVoteMessage{SelectedOptions: [][]byte{{1, 2}}},
				SenderTimestampMS:    proto.Int64(1700000100000),
			},
			{
				PollUpdateMessageKey: &waProto.MessageKey{RemoteJID: proto.String("g@g.us"), FromMe: proto.Bool(true)},
				Vote:                 &waProto.PollVoteMessage{SelectedOptions: [][]byte{{3}}},
				SenderTimestampMS:    proto.Int64(1700000200000),
			},
		},
	}
	pm := ParseHistoryMessage("g@g.us", hist, nil)
	if pm.Poll == nil || pm.Poll.Question != "Waar?" {
		t.Fatalf("poll not parsed: %+v", pm.Poll)
	}
	if len(pm.Votes) != 2 {
		t.Fatalf("votes = %d, want 2", len(pm.Votes))
	}
	if pm.Votes[0].VoterJID != "31600000001@s.whatsapp.net" || pm.Votes[0].Timestamp.Unix() != 1700000100 {
		t.Errorf("vote 0 = %+v", pm.Votes[0])
	}
	if !pm.Votes[1].FromMe {
		t.Errorf("vote 1 should be mine: %+v", pm.Votes[1])
	}
}
