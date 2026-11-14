package main

import (
	"context"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steipete/wacli/internal/app"
	"github.com/steipete/wacli/internal/store"
	"github.com/steipete/wacli/internal/wa"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type mediaSender interface {
	WA() app.WAClient
	DB() *store.DB
}

// preparedMedia is een naar WhatsApp geüpload bestand dat nog verstuurd moet
// worden. Eén upload kan naar meerdere chats: de mediasleutel mag hergebruikt
// worden, zo gaan de bytes maar één keer over de lijn.
type preparedMedia struct {
	MediaType string // image, video, audio, document
	MimeType  string
	Name      string
	Up        whatsmeow.UploadResponse
}

// detectMedia bepaalt mime- en WhatsApp-mediatype. extPath geeft de extensie
// (een bestandspad of de bestandsnaam); mimeOverride wint als die gezet is.
func detectMedia(data []byte, extPath, mimeOverride string) (mimeType, mediaType string) {
	mimeType = strings.TrimSpace(mimeOverride)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(extPath)))
	}
	if mimeType == "" {
		sniff := data
		if len(sniff) > 512 {
			sniff = sniff[:512]
		}
		mimeType = http.DetectContentType(sniff)
	}
	mediaType = "document"
	switch {
	// GIF en SVG zijn voor WhatsApp geen foto: als document komen ze heel aan.
	case strings.HasPrefix(mimeType, "image/") && mimeType != "image/gif" && mimeType != "image/svg+xml":
		mediaType = "image"
	case strings.HasPrefix(mimeType, "video/"):
		mediaType = "video"
	case strings.HasPrefix(mimeType, "audio/"):
		mediaType = "audio"
	}
	return mimeType, mediaType
}

func uploadMedia(ctx context.Context, a mediaSender, data []byte, extPath, name, mimeOverride string) (preparedMedia, error) {
	mimeType, mediaType := detectMedia(data, extPath, mimeOverride)
	uploadType, _ := wa.MediaTypeFromString(mediaType)
	up, err := a.WA().Upload(ctx, data, uploadType)
	if err != nil {
		return preparedMedia{}, err
	}
	return preparedMedia{MediaType: mediaType, MimeType: mimeType, Name: name, Up: up}, nil
}

func mediaMessage(pm preparedMedia, caption string) *waProto.Message {
	up := pm.Up
	msg := &waProto.Message{}
	switch pm.MediaType {
	case "image":
		msg.ImageMessage = &waProto.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(pm.MimeType),
			Caption:       proto.String(caption),
		}
	case "video":
		msg.VideoMessage = &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(pm.MimeType),
			Caption:       proto.String(caption),
		}
	case "audio":
		msg.AudioMessage = &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(pm.MimeType),
			PTT:           proto.Bool(false),
		}
	default:
		msg.DocumentMessage = &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			Mimetype:      proto.String(pm.MimeType),
			FileName:      proto.String(pm.Name),
			Caption:       proto.String(caption),
			Title:         proto.String(pm.Name),
		}
	}
	return msg
}

// sendPrepared verstuurt een geüpload bestand naar één chat en legt het vast.
// Audio heeft in WhatsApp geen bijschrift; dat valt dan weg.
func sendPrepared(ctx context.Context, a mediaSender, to types.JID, pm preparedMedia, caption string) (string, error) {
	if pm.MediaType == "audio" {
		caption = ""
	}
	id, err := a.WA().SendProtoMessage(ctx, to, mediaMessage(pm, caption))
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	up := pm.Up
	chatName := a.WA().ResolveChatName(ctx, to, "")
	_ = a.DB().UpsertChat(to.String(), chatKindFromJID(to), chatName, now)
	_ = a.DB().UpsertMessage(store.UpsertMessageParams{
		ChatJID:       to.String(),
		ChatName:      chatName,
		MsgID:         string(id),
		SenderName:    "me",
		Timestamp:     now,
		FromMe:        true,
		Text:          caption,
		MediaType:     pm.MediaType,
		MediaCaption:  caption,
		Filename:      pm.Name,
		MimeType:      pm.MimeType,
		DirectPath:    up.DirectPath,
		MediaKey:      up.MediaKey,
		FileSHA256:    up.FileSHA256,
		FileEncSHA256: up.FileEncSHA256,
		FileLength:    up.FileLength,
		IsLive:        true,
	})
	return string(id), nil
}

func sendFile(ctx context.Context, a mediaSender, to types.JID, filePath, filename, caption, mimeOverride string) (string, map[string]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, err
	}
	name := strings.TrimSpace(filename)
	if name == "" {
		name = filepath.Base(filePath)
	}
	// Mime op basis van het pad, niet van een afwijkende weergavenaam.
	pm, err := uploadMedia(ctx, a, data, filePath, name, mimeOverride)
	if err != nil {
		return "", nil, err
	}
	id, err := sendPrepared(ctx, a, to, pm, caption)
	if err != nil {
		return "", nil, err
	}
	return id, map[string]string{
		"name":      name,
		"mime_type": pm.MimeType,
		"media":     pm.MediaType,
	}, nil
}

func chatKindFromJID(j types.JID) string {
	if j.Server == types.GroupServer {
		return "group"
	}
	if j.IsBroadcastList() {
		return "broadcast"
	}
	if j.Server == types.DefaultUserServer {
		return "dm"
	}
	return "unknown"
}
