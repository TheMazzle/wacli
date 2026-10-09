package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestParseUserOrJID(t *testing.T) {
	j, err := ParseUserOrJID("1234567890")
	if err != nil {
		t.Fatalf("ParseUserOrJID: %v", err)
	}
	if j.Server != types.DefaultUserServer || j.User != "1234567890" {
		t.Fatalf("unexpected jid: %+v", j)
	}

	j, err = ParseUserOrJID("123@g.us")
	if err != nil {
		t.Fatalf("ParseUserOrJID group: %v", err)
	}
	if !IsGroupJID(j) {
		t.Fatalf("expected group jid, got %+v", j)
	}
}

func TestBestContactName(t *testing.T) {
	if BestContactName(types.ContactInfo{Found: false, FullName: "x"}) != "" {
		t.Fatalf("expected empty for not found")
	}
	if BestContactName(types.ContactInfo{Found: true, FullName: "Full"}) != "Full" {
		t.Fatalf("expected full name")
	}
	if BestContactName(types.ContactInfo{Found: true, FirstName: "First"}) != "First" {
		t.Fatalf("expected first name")
	}
	if BestContactName(types.ContactInfo{Found: true, BusinessName: "Biz"}) != "Biz" {
		t.Fatalf("expected business name")
	}
	if BestContactName(types.ContactInfo{Found: true, PushName: "Push"}) != "Push" {
		t.Fatalf("expected push name")
	}
}

// Regressie: SendReaction zette geen Participant in de MessageKey. In een
// groep weet WhatsApp dan niet op wiens bericht de reactie slaat; de server
// accepteert hem, maar geen enkele client toont hem. Op 2026-10-09 kwamen zo
// zeven reacties op een groepsbericht nergens aan.
func TestBuildReactionKeyGroupMessageFromOtherHasParticipant(t *testing.T) {
	chat, _ := ParseUserOrJID("120363265298442716@g.us")
	sender, _ := ParseUserOrJID("171743025057880@lid")

	key := BuildReactionKey(chat, sender, "ACCE2C39", false)

	if key.GetParticipant() != "171743025057880@lid" {
		t.Fatalf("participant = %q, want de afzender van het bericht", key.GetParticipant())
	}
	if key.GetFromMe() {
		t.Fatalf("fromMe moet false zijn")
	}
	if key.GetRemoteJID() != chat.String() || key.GetID() != "ACCE2C39" {
		t.Fatalf("remoteJID/id kloppen niet: %v", key)
	}
}

func TestBuildReactionKeyStripsDeviceFromParticipant(t *testing.T) {
	chat, _ := ParseUserOrJID("123@g.us")
	sender, _ := ParseUserOrJID("31612345678:12@s.whatsapp.net")

	key := BuildReactionKey(chat, sender, "ID1", false)

	if key.GetParticipant() != "31612345678@s.whatsapp.net" {
		t.Fatalf("participant = %q, want zonder device-deel", key.GetParticipant())
	}
}

func TestBuildReactionKeyOwnGroupMessageHasNoParticipant(t *testing.T) {
	chat, _ := ParseUserOrJID("123@g.us")
	sender, _ := ParseUserOrJID("171743025057880@lid")

	key := BuildReactionKey(chat, sender, "ID1", true)

	if key.Participant != nil {
		t.Fatalf("eigen bericht hoort geen participant te hebben, kreeg %q", key.GetParticipant())
	}
	if !key.GetFromMe() {
		t.Fatalf("fromMe moet true zijn")
	}
}

func TestBuildReactionKeyDirectChatHasNoParticipant(t *testing.T) {
	chat, _ := ParseUserOrJID("31612345678@s.whatsapp.net")

	key := BuildReactionKey(chat, chat, "ID1", false)

	if key.Participant != nil {
		t.Fatalf("1-op-1 hoort geen participant te hebben, kreeg %q", key.GetParticipant())
	}
}
