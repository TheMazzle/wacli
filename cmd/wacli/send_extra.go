package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/steipete/wacli/internal/store"
	"github.com/steipete/wacli/internal/wa"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// uploadTTL is hoe lang een upload bruikbaar blijft voor send_media. Ruim
// genoeg om één bericht naar tientallen chats te sturen.
const uploadTTL = time.Hour

// MaxUploadBytes begrenst wat via de socket binnenkomt: het hele bestand
// staat als base64 in één JSON-regel.
const MaxUploadBytes = 100 << 20

type uploadCache struct {
	mu    sync.Mutex
	items map[string]cachedUpload
}

type cachedUpload struct {
	media   preparedMedia
	expires time.Time
}

func (c *uploadCache) put(pm preparedMedia, now time.Time) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]cachedUpload{}
	}
	for k, v := range c.items {
		if now.After(v.expires) {
			delete(c.items, k)
		}
	}
	c.items[token] = cachedUpload{media: pm, expires: now.Add(uploadTTL)}
	return token
}

func (c *uploadCache) get(token string, now time.Time) (preparedMedia, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[token]
	if !ok || now.After(v.expires) {
		return preparedMedia{}, false
	}
	return v.media, true
}

var uploads = &uploadCache{}

func (h *syncHandler) ready() error {
	if h.app == nil {
		return fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return fmt.Errorf("whatsapp not connected")
	}
	return nil
}

// UploadMedia uploadt een bestand naar WhatsApp en geeft een token terug
// waarmee SendMedia het naar één of meer chats stuurt.
func (h *syncHandler) UploadMedia(data []byte, filename, mimeType string) (string, string, error) {
	if err := h.ready(); err != nil {
		return "", "", err
	}
	if len(data) == 0 {
		return "", "", fmt.Errorf("empty file")
	}
	if len(data) > MaxUploadBytes {
		return "", "", fmt.Errorf("file is %d MB; the limit is %d MB", len(data)>>20, MaxUploadBytes>>20)
	}
	name := strings.TrimSpace(filename)
	if name == "" {
		name = "file"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pm, err := uploadMedia(ctx, h.app, data, name, name, mimeType)
	if err != nil {
		return "", "", fmt.Errorf("upload: %w", err)
	}
	return uploads.put(pm, time.Now()), pm.MediaType, nil
}

func (h *syncHandler) SendMedia(to, token, caption string) (string, error) {
	if err := h.ready(); err != nil {
		return "", err
	}
	pm, ok := uploads.get(token, time.Now())
	if !ok {
		return "", fmt.Errorf("upload expired or unknown; upload the file again")
	}
	toJID, err := wa.ParseUserOrJID(to)
	if err != nil {
		return "", fmt.Errorf("parse recipient: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := sendPrepared(ctx, h.app, toJID, pm, caption)
	if err != nil {
		return "", fmt.Errorf("send media: %w", err)
	}
	return id, nil
}

// ValidateLocation controleert coördinaten.
func ValidateLocation(lat, lon float64) error {
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return fmt.Errorf("invalid coordinates %v, %v", lat, lon)
	}
	if lat == 0 && lon == 0 {
		return fmt.Errorf("coordinates 0,0 are almost certainly a mistake")
	}
	return nil
}

func locationMessage(lat, lon float64, name, address string) *waProto.Message {
	loc := &waProto.LocationMessage{
		DegreesLatitude:  proto.Float64(lat),
		DegreesLongitude: proto.Float64(lon),
	}
	if n := strings.TrimSpace(name); n != "" {
		loc.Name = proto.String(n)
	}
	if a := strings.TrimSpace(address); a != "" {
		loc.Address = proto.String(a)
	}
	return &waProto.Message{LocationMessage: loc}
}

func (h *syncHandler) SendLocation(to string, lat, lon float64, name, address string) (string, error) {
	if err := h.ready(); err != nil {
		return "", err
	}
	if err := ValidateLocation(lat, lon); err != nil {
		return "", err
	}
	toJID, err := wa.ParseUserOrJID(to)
	if err != nil {
		return "", fmt.Errorf("parse recipient: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := h.app.WA().SendProtoMessage(ctx, toJID, locationMessage(lat, lon, name, address))
	if err != nil {
		return "", fmt.Errorf("send location: %w", err)
	}
	label := strings.TrimSpace(name)
	if label == "" {
		label = fmt.Sprintf("%.5f, %.5f", lat, lon)
	}
	h.storeSent(ctx, toJID, string(id), "📍 "+label)
	return string(id), nil
}

// vCard maakt een contactkaart die WhatsApp herkent: waid koppelt het nummer
// aan het WhatsApp-account, zodat de ontvanger direct kan chatten.
func vCard(name, phone string) string {
	esc := strings.NewReplacer("\\", "\\\\", ",", "\\,", ";", "\\;", "\n", " ")
	n := esc.Replace(name)
	return "BEGIN:VCARD\nVERSION:3.0\nN:;" + n + ";;;\nFN:" + n +
		"\nTEL;type=CELL;type=VOICE;waid=" + phone + ":+" + phone + "\nEND:VCARD"
}

func (h *syncHandler) SendContact(to, contactJID, displayName string) (string, error) {
	if err := h.ready(); err != nil {
		return "", err
	}
	toJID, err := wa.ParseUserOrJID(to)
	if err != nil {
		return "", fmt.Errorf("parse recipient: %w", err)
	}
	cJID, err := wa.ParseUserOrJID(contactJID)
	if err != nil {
		return "", fmt.Errorf("parse contact: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Een kaart heeft een telefoonnummer nodig; een LID is geen nummer.
	pn := h.app.WA().ResolveLIDToPN(ctx, cJID.ToNonAD())
	if pn.Server != types.DefaultUserServer || pn.User == "" {
		return "", fmt.Errorf("no phone number known for %s", contactJID)
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		if info, err := h.app.WA().GetContact(ctx, pn); err == nil {
			name = wa.BestContactName(info)
		}
	}
	if name == "" {
		name = "+" + pn.User
	}
	msg := &waProto.Message{ContactMessage: &waProto.ContactMessage{
		DisplayName: proto.String(name),
		Vcard:       proto.String(vCard(name, pn.User)),
	}}
	id, err := h.app.WA().SendProtoMessage(ctx, toJID, msg)
	if err != nil {
		return "", fmt.Errorf("send contact: %w", err)
	}
	h.storeSent(ctx, toJID, string(id), "👤 "+name)
	return string(id), nil
}

// storeSent legt een verstuurd bericht zonder media vast, zodat het meteen in
// de chat staat (de server stuurt eigen berichten niet altijd terug).
func (h *syncHandler) storeSent(ctx context.Context, to types.JID, id, text string) {
	now := time.Now().UTC()
	chatName := h.app.WA().ResolveChatName(ctx, to, "")
	_ = h.app.DB().UpsertChat(to.String(), chatKindFromJID(to), chatName, now)
	_ = h.app.DB().UpsertMessage(store.UpsertMessageParams{
		ChatJID:     to.String(),
		ChatName:    chatName,
		MsgID:       id,
		SenderName:  "me",
		Timestamp:   now,
		FromMe:      true,
		Text:        text,
		DisplayText: text,
		IsLive:      true,
	})
}
