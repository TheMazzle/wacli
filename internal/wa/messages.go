package wa

import (
	"strings"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Media struct {
	Type          string
	Caption       string
	Filename      string
	MimeType      string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    uint64
}

type ParsedMessage struct {
	Chat            types.JID
	ID              string
	SenderJID       string
	Timestamp       time.Time
	FromMe          bool
	Text            string
	Media           *Media
	PushName        string
	ReplyToID       string
	ReplyToDisplay  string
	ReactionToID    string
	ReactionEmoji   string
	MsgOrderID      *uint64
	IsLive          bool
	EventType       string // "message" (default), "system"
	IsForwarded     bool
	ForwardingScore uint32

	// Poll is gevuld als dit bericht een poll aanmaakt.
	Poll *Poll
	// PollVoteFor is het ID van de poll waar dit bericht een stem op is
	// (PollUpdateMessage). Zo'n bericht krijgt geen eigen rij in messages;
	// de stem zelf staat in Votes zodra hij ontsleuteld is.
	PollVoteFor string
	// Votes zijn ontsleutelde stemmen op de poll in dit bericht (history sync
	// levert ze mee bij de poll) of op PollVoteFor (live stem).
	Votes []PollVote
}

// Poll is de inhoud van een poll: vraag, opties en hoeveel opties een
// deelnemer mag kiezen (1 = enkele keuze, 0 of meer dan 1 = meerdere).
type Poll struct {
	Question        string
	Options         []string
	SelectableCount uint32
}

// PollVote is de actuele keuze van één deelnemer. Een nieuwe stem vervangt de
// vorige; een lege OptionHashes betekent dat de stem is ingetrokken.
// OptionHashes zijn SHA-256-hashes van de optienamen, zoals WhatsApp ze stuurt.
type PollVote struct {
	VoterJID     string
	FromMe       bool
	OptionHashes [][]byte
	Timestamp    time.Time
}

func ParseLiveMessage(evt *events.Message) ParsedMessage {
	msg := ParsedMessage{
		Chat:      evt.Info.Chat,
		ID:        evt.Info.ID,
		Timestamp: evt.Info.Timestamp,
		FromMe:    evt.Info.IsFromMe,
		PushName:  evt.Info.PushName,
		IsLive:    true,
	}
	if s := evt.Info.Sender.String(); s != "" {
		msg.SenderJID = s
	}

	extractWAProto(evt.Message, &msg)
	return msg
}

func ParseHistoryMessage(chatJID string, hist *waProto.WebMessageInfo, msgOrderID *uint64) ParsedMessage {
	var chat types.JID
	if parsed, err := types.ParseJID(chatJID); err == nil {
		chat = parsed
	}

	pm := ParsedMessage{
		Chat:       chat,
		ID:         hist.GetKey().GetID(),
		Timestamp:  time.Unix(int64(hist.GetMessageTimestamp()), 0).UTC(),
		FromMe:     hist.GetKey().GetFromMe(),
		MsgOrderID: msgOrderID,
	}

	// Prefer top-level participant (works for LID-addressed groups),
	// then fall back to key.participant, then key.remoteJID.
	// This matches whatsmeow's own ParseWebMessage priority.
	sender := strings.TrimSpace(hist.GetParticipant())
	if sender == "" {
		sender = strings.TrimSpace(hist.GetKey().GetParticipant())
	}
	if sender == "" {
		sender = strings.TrimSpace(hist.GetKey().GetRemoteJID())
	}
	pm.SenderJID = sender

	if hist.GetMessage() != nil {
		extractWAProto(hist.GetMessage(), &pm)
	}
	if pm.Poll != nil {
		pm.Votes = historyPollVotes(hist)
	}
	return pm
}

// historyPollVotes haalt de stemmen op die history sync al ontsleuteld bij een
// poll meelevert. De kiezer staat in de sleutel van het stembericht.
func historyPollVotes(hist *waProto.WebMessageInfo) []PollVote {
	var out []PollVote
	for _, u := range hist.GetPollUpdates() {
		key := u.GetPollUpdateMessageKey()
		if key == nil || u.GetVote() == nil {
			continue
		}
		v := PollVote{
			FromMe:       key.GetFromMe(),
			OptionHashes: u.GetVote().GetSelectedOptions(),
			Timestamp:    time.UnixMilli(u.GetSenderTimestampMS()).UTC(),
		}
		if !v.FromMe {
			v.VoterJID = strings.TrimSpace(key.GetParticipant())
			if v.VoterJID == "" {
				v.VoterJID = strings.TrimSpace(key.GetRemoteJID())
			}
			if v.VoterJID == "" {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

// pollCreation geeft de poll in m, ongeacht welke van de protoversies
// WhatsApp gebruikt (V1 enkele keuze, V2/V3 en later zijn varianten).
func pollCreation(m *waProto.Message) *waProto.PollCreationMessage {
	for _, p := range []*waProto.PollCreationMessage{
		m.GetPollCreationMessage(),
		m.GetPollCreationMessageV2(),
		m.GetPollCreationMessageV3(),
		m.GetPollCreationMessageV5(),
		m.GetPollCreationMessageV6(),
	} {
		if p != nil {
			return p
		}
	}
	return nil
}

func extractWAProto(m *waProto.Message, pm *ParsedMessage) {
	if m == nil || pm == nil {
		return
	}

	if reaction := m.GetReactionMessage(); reaction != nil {
		pm.ReactionEmoji = reaction.GetText()
		if key := reaction.GetKey(); key != nil {
			pm.ReactionToID = key.GetID()
		}
	} else if encReaction := m.GetEncReactionMessage(); encReaction != nil {
		if key := encReaction.GetTargetMessageKey(); key != nil {
			pm.ReactionToID = key.GetID()
		}
	}

	switch {
	case m.GetConversation() != "":
		pm.Text = m.GetConversation()
	case m.GetExtendedTextMessage() != nil:
		pm.Text = m.GetExtendedTextMessage().GetText()
	}

	if poll := pollCreation(m); poll != nil {
		p := &Poll{
			Question:        strings.TrimSpace(poll.GetName()),
			SelectableCount: poll.GetSelectableOptionsCount(),
		}
		for _, o := range poll.GetOptions() {
			p.Options = append(p.Options, o.GetOptionName())
		}
		pm.Poll = p
		pm.EventType = "poll"
		if pm.Text == "" {
			pm.Text = p.Question
		}
	}

	if upd := m.GetPollUpdateMessage(); upd != nil {
		if key := upd.GetPollCreationMessageKey(); key != nil {
			pm.PollVoteFor = key.GetID()
		}
	}

	if img := m.GetImageMessage(); img != nil {
		if pm.Text == "" {
			pm.Text = img.GetCaption()
		}
		pm.Media = &Media{
			Type:          "image",
			Caption:       img.GetCaption(),
			MimeType:      img.GetMimetype(),
			DirectPath:    img.GetDirectPath(),
			MediaKey:      clone(img.GetMediaKey()),
			FileSHA256:    clone(img.GetFileSHA256()),
			FileEncSHA256: clone(img.GetFileEncSHA256()),
			FileLength:    img.GetFileLength(),
		}
	}

	if vid := m.GetVideoMessage(); vid != nil {
		if pm.Text == "" {
			pm.Text = vid.GetCaption()
		}
		mediaType := "video"
		if vid.GetGifPlayback() {
			mediaType = "gif"
		}
		pm.Media = &Media{
			Type:          mediaType,
			Caption:       vid.GetCaption(),
			MimeType:      vid.GetMimetype(),
			DirectPath:    vid.GetDirectPath(),
			MediaKey:      clone(vid.GetMediaKey()),
			FileSHA256:    clone(vid.GetFileSHA256()),
			FileEncSHA256: clone(vid.GetFileEncSHA256()),
			FileLength:    vid.GetFileLength(),
		}
	}

	if aud := m.GetAudioMessage(); aud != nil {
		if pm.Text == "" {
			pm.Text = "[Audio]"
		}
		pm.Media = &Media{
			Type:          "audio",
			Caption:       pm.Text,
			MimeType:      aud.GetMimetype(),
			DirectPath:    aud.GetDirectPath(),
			MediaKey:      clone(aud.GetMediaKey()),
			FileSHA256:    clone(aud.GetFileSHA256()),
			FileEncSHA256: clone(aud.GetFileEncSHA256()),
			FileLength:    aud.GetFileLength(),
		}
	}

	if doc := m.GetDocumentMessage(); doc != nil {
		if pm.Text == "" {
			pm.Text = doc.GetCaption()
		}
		pm.Media = &Media{
			Type:          "document",
			Caption:       doc.GetCaption(),
			Filename:      doc.GetFileName(),
			MimeType:      doc.GetMimetype(),
			DirectPath:    doc.GetDirectPath(),
			MediaKey:      clone(doc.GetMediaKey()),
			FileSHA256:    clone(doc.GetFileSHA256()),
			FileEncSHA256: clone(doc.GetFileEncSHA256()),
			FileLength:    doc.GetFileLength(),
		}
	}

	if sticker := m.GetStickerMessage(); sticker != nil {
		pm.Media = &Media{
			Type:          "sticker",
			MimeType:      sticker.GetMimetype(),
			DirectPath:    sticker.GetDirectPath(),
			MediaKey:      clone(sticker.GetMediaKey()),
			FileSHA256:    clone(sticker.GetFileSHA256()),
			FileEncSHA256: clone(sticker.GetFileEncSHA256()),
			FileLength:    sticker.GetFileLength(),
		}
	}

	if ctx := contextInfoForMessage(m); ctx != nil {
		if id := strings.TrimSpace(ctx.GetStanzaID()); id != "" {
			pm.ReplyToID = id
		}
		if quoted := ctx.GetQuotedMessage(); quoted != nil {
			pm.ReplyToDisplay = strings.TrimSpace(displayTextForProto(quoted))
		}
		if ctx.GetIsForwarded() {
			pm.IsForwarded = true
			pm.ForwardingScore = ctx.GetForwardingScore()
		}
	}

	// Detect protocol/system messages that would otherwise be stored with empty text.
	// Only set if nothing above already populated the message.
	if pm.Text == "" && pm.Media == nil && pm.ReactionToID == "" {
		if proto := m.GetProtocolMessage(); proto != nil {
			pm.EventType = "system"
			switch proto.GetType() {
			case waProto.ProtocolMessage_MESSAGE_EDIT:
				pm.Text = "Message edited"
			case waProto.ProtocolMessage_REVOKE:
				pm.Text = "Message deleted"
				if key := proto.GetKey(); key != nil {
					pm.ReplyToID = key.GetID()
				}
			case waProto.ProtocolMessage_EPHEMERAL_SETTING:
				dur := proto.GetEphemeralExpiration()
				if dur == 0 {
					pm.Text = "Disappearing messages turned off"
				} else {
					pm.Text = "Disappearing messages turned on"
				}
			default:
				// Other protocol messages (key distribution, etc.) — skip silently
				pm.EventType = ""
			}
		}
		if m.GetGroupInviteMessage() != nil {
			pm.EventType = "system"
			pm.Text = "Group invite shared"
		}
	}
}

func clone(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func contextInfoForMessage(m *waProto.Message) *waProto.ContextInfo {
	if m == nil {
		return nil
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetContextInfo()
	}
	if img := m.GetImageMessage(); img != nil {
		return img.GetContextInfo()
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid.GetContextInfo()
	}
	if aud := m.GetAudioMessage(); aud != nil {
		return aud.GetContextInfo()
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return doc.GetContextInfo()
	}
	if sticker := m.GetStickerMessage(); sticker != nil {
		return sticker.GetContextInfo()
	}
	if loc := m.GetLocationMessage(); loc != nil {
		return loc.GetContextInfo()
	}
	if contact := m.GetContactMessage(); contact != nil {
		return contact.GetContextInfo()
	}
	if contacts := m.GetContactsArrayMessage(); contacts != nil {
		return contacts.GetContextInfo()
	}
	if poll := pollCreation(m); poll != nil {
		return poll.GetContextInfo()
	}
	return nil
}

func displayTextForProto(m *waProto.Message) string {
	if m == nil {
		return ""
	}

	if img := m.GetImageMessage(); img != nil {
		return "Sent image"
	}
	if vid := m.GetVideoMessage(); vid != nil {
		if vid.GetGifPlayback() {
			return "Sent gif"
		}
		return "Sent video"
	}
	if aud := m.GetAudioMessage(); aud != nil {
		return "Sent audio"
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return "Sent document"
	}
	if sticker := m.GetStickerMessage(); sticker != nil {
		return "Sent sticker"
	}
	if loc := m.GetLocationMessage(); loc != nil {
		return "Sent location"
	}
	if contact := m.GetContactMessage(); contact != nil {
		return "Sent contact"
	}
	if contacts := m.GetContactsArrayMessage(); contacts != nil {
		return "Sent contacts"
	}
	if poll := pollCreation(m); poll != nil {
		return "Poll: " + strings.TrimSpace(poll.GetName())
	}

	if text := strings.TrimSpace(m.GetConversation()); text != "" {
		return text
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		if text := strings.TrimSpace(ext.GetText()); text != "" {
			return text
		}
	}
	return ""
}

// PopulatedFields noemt de gevulde velden van een bericht, voor diagnose van
// berichttypes die wacli nog niet kent (anders worden het lege rijen).
func PopulatedFields(m *waProto.Message) []string {
	if m == nil {
		return nil
	}
	var out []string
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		out = append(out, string(fd.Name()))
		return true
	})
	return out
}

// IsEmpty meldt of er na het parsen niets bruikbaars in het bericht zat.
func (pm ParsedMessage) IsEmpty() bool {
	return strings.TrimSpace(pm.Text) == "" && pm.Media == nil && pm.ReactionToID == "" &&
		pm.Poll == nil && pm.PollVoteFor == "" && (pm.EventType == "" || pm.EventType == "message")
}
