package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/steipete/wacli/internal/store"
	"github.com/steipete/wacli/internal/wa"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type SyncMode string

const (
	SyncModeBootstrap SyncMode = "bootstrap"
	SyncModeOnce      SyncMode = "once"
	SyncModeFollow    SyncMode = "follow"
)

type SyncOptions struct {
	Mode            SyncMode
	AllowQR         bool
	OnQRCode        func(string)
	AfterConnect    func(context.Context) error
	DownloadMedia   bool
	RefreshContacts bool
	RefreshGroups   bool
	BackfillGaps    bool          // auto-detect and backfill sync gaps on connect
	IdleExit        time.Duration // only used for bootstrap/once
	Verbosity       int           // future
}

type SyncResult struct {
	MessagesStored int64
}

func (a *App) Sync(ctx context.Context, opts SyncOptions) (SyncResult, error) {
	if opts.Mode == "" {
		opts.Mode = SyncModeFollow
	}
	if (opts.Mode == SyncModeBootstrap || opts.Mode == SyncModeOnce) && opts.IdleExit <= 0 {
		opts.IdleExit = 30 * time.Second
	}

	if err := a.OpenWA(); err != nil {
		return SyncResult{}, err
	}

	var messagesStored atomic.Int64
	lastEvent := atomic.Int64{}
	lastEvent.Store(time.Now().UTC().UnixNano())

	disconnected := make(chan struct{}, 1)

	var stopMedia func()
	var mediaJobs chan mediaJob
	enqueueMedia := func(chatJID, msgID string) {}
	if opts.DownloadMedia {
		mediaJobs = make(chan mediaJob, 512)
		enqueueMedia = func(chatJID, msgID string) {
			if strings.TrimSpace(chatJID) == "" || strings.TrimSpace(msgID) == "" {
				return
			}
			select {
			case mediaJobs <- mediaJob{chatJID: chatJID, msgID: msgID}:
			default:
				// Avoid blocking the event handler.
				go func() {
					select {
					case mediaJobs <- mediaJob{chatJID: chatJID, msgID: msgID}:
					case <-ctx.Done():
					}
				}()
			}
		}
	}

	handlerID := a.wa.AddEventHandler(func(evt interface{}) {
		lastEvent.Store(time.Now().UTC().UnixNano())

		switch v := evt.(type) {
		case *events.Message:
			pm := wa.ParseLiveMessage(v)
			if pm.ReactionToID != "" && pm.ReactionEmoji == "" && v.Message != nil && v.Message.GetEncReactionMessage() != nil {
				if reaction, err := a.wa.DecryptReaction(ctx, v); err == nil && reaction != nil {
					pm.ReactionEmoji = reaction.GetText()
					if pm.ReactionToID == "" {
						if key := reaction.GetKey(); key != nil {
							pm.ReactionToID = key.GetID()
						}
					}
				}
			}
			if err := a.storeParsedMessage(ctx, pm); err == nil {
				messagesStored.Add(1)
			}
			if opts.DownloadMedia && pm.Media != nil && pm.ID != "" {
				enqueueMedia(pm.Chat.String(), pm.ID)
			}
			if messagesStored.Load()%25 == 0 {
				fmt.Fprintf(os.Stderr, "\rSynced %d messages...", messagesStored.Load())
			}
		case *events.HistorySync:
			fmt.Fprintf(os.Stderr, "\nProcessing history sync (%d conversations)...\n", len(v.Data.Conversations))
			for _, conv := range v.Data.Conversations {
				lastEvent.Store(time.Now().UTC().UnixNano())
				chatID := strings.TrimSpace(conv.GetID())
				if chatID == "" {
					continue
				}
				for _, m := range conv.Messages {
					lastEvent.Store(time.Now().UTC().UnixNano())
					if m.Message == nil {
						continue
					}
					var orderID *uint64
					if m.MsgOrderID != nil {
						v := m.GetMsgOrderID()
						orderID = &v
					}
					pm := wa.ParseHistoryMessage(chatID, m.Message, orderID)
					if pm.ID == "" || pm.Chat.IsEmpty() {
						continue
					}
					if err := a.storeParsedMessage(ctx, pm); err == nil {
						messagesStored.Add(1)
					}
					if opts.DownloadMedia && pm.Media != nil && pm.ID != "" {
						enqueueMedia(pm.Chat.String(), pm.ID)
					}
				}
			}
			fmt.Fprintf(os.Stderr, "\rSynced %d messages...", messagesStored.Load())
		case *events.Receipt:
			// Track read state: ReceiptTypeReadSelf = we read on another device (e.g. phone),
			// ReceiptTypeRead = someone else read our message.
			if v.Type == types.ReceiptTypeRead || v.Type == types.ReceiptTypeReadSelf {
				chatJID := v.Chat.ToNonAD().String()
				readTS := v.Timestamp.UTC().Unix()
				_ = a.db.UpdateReadTS(chatJID, readTS)
			}
		case *events.Connected:
			fmt.Fprintln(os.Stderr, "\nConnected.")
		case *events.Disconnected:
			fmt.Fprintln(os.Stderr, "\nDisconnected.")
			select {
			case disconnected <- struct{}{}:
			default:
			}
		}
	})
	defer a.wa.RemoveEventHandler(handlerID)

	if err := a.Connect(ctx, opts.AllowQR, opts.OnQRCode); err != nil {
		return SyncResult{}, err
	}

	if opts.DownloadMedia {
		var err error
		stopMedia, err = a.runMediaWorkers(ctx, mediaJobs, 4)
		if err != nil {
			return SyncResult{}, err
		}
		defer stopMedia()
	}

	// Optional: bootstrap imports (helps contacts/groups management without waiting for events).
	if opts.RefreshContacts {
		_ = a.refreshContacts(ctx)
	}
	if opts.RefreshGroups {
		_ = a.refreshGroups(ctx)
	}
	if opts.AfterConnect != nil {
		if err := opts.AfterConnect(ctx); err != nil {
			return SyncResult{MessagesStored: messagesStored.Load()}, err
		}
	}

	// Auto-backfill: detect gaps in message history and request missing messages.
	// Uses the same WhatsApp connection, so no store lock conflict.
	if opts.BackfillGaps {
		a.backfillDetectedGaps(ctx)
	}

	if opts.Mode == SyncModeFollow {
		for {
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "\nStopping sync.")
				return SyncResult{MessagesStored: messagesStored.Load()}, nil
			case <-disconnected:
				fmt.Fprintln(os.Stderr, "Reconnecting...")
				if err := a.wa.ReconnectWithBackoff(ctx, 2*time.Second, 30*time.Second); err != nil {
					return SyncResult{MessagesStored: messagesStored.Load()}, err
				}
				fmt.Fprintln(os.Stderr, "Reconnected.")
				if opts.BackfillGaps {
					go a.backfillDetectedGaps(ctx)
				}
			}
		}
	}

	// Bootstrap/once: exit after idle.
	poll := 250 * time.Millisecond
	if opts.IdleExit >= 2*time.Second {
		poll = 1 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "\nStopping sync.")
			return SyncResult{MessagesStored: messagesStored.Load()}, nil
		case <-disconnected:
			fmt.Fprintln(os.Stderr, "Reconnecting...")
			if err := a.wa.ReconnectWithBackoff(ctx, 2*time.Second, 30*time.Second); err != nil {
				return SyncResult{MessagesStored: messagesStored.Load()}, err
			}
			fmt.Fprintln(os.Stderr, "Reconnected.")
			if opts.BackfillGaps {
				go a.backfillDetectedGaps(ctx)
			}
		case <-ticker.C:
			last := time.Unix(0, lastEvent.Load())
			if time.Since(last) >= opts.IdleExit {
				fmt.Fprintf(os.Stderr, "\nIdle for %s, exiting.\n", opts.IdleExit)
				return SyncResult{MessagesStored: messagesStored.Load()}, nil
			}
		}
	}
}

func chatKind(chat types.JID) string {
	if chat.Server == types.GroupServer {
		return "group"
	}
	if chat.IsBroadcastList() {
		return "broadcast"
	}
	if chat.Server == types.DefaultUserServer {
		return "dm"
	}
	return "unknown"
}

func (a *App) storeParsedMessage(ctx context.Context, pm wa.ParsedMessage) error {
	chatJID := pm.Chat.ToNonAD().String()
	chatName := a.wa.ResolveChatName(ctx, pm.Chat, pm.PushName)
	if err := a.db.UpsertChat(chatJID, chatKind(pm.Chat), chatName, pm.Timestamp); err != nil {
		return err
	}

	// Best-effort: store contact info for DMs.
	if pm.Chat.Server == types.DefaultUserServer {
		normalizedChat := pm.Chat.ToNonAD()
		if info, err := a.wa.GetContact(ctx, normalizedChat); err == nil {
			_ = a.db.UpsertContact(
				normalizedChat.String(),
				normalizedChat.User,
				info.PushName,
				info.FullName,
				info.FirstName,
				info.BusinessName,
			)
		}
	}

	senderName := ""
	senderJID := ""
	if pm.FromMe {
		senderName = "me"
	} else if s := strings.TrimSpace(pm.PushName); s != "" && s != "-" {
		senderName = s
	}
	if pm.SenderJID != "" {
		if jid, err := types.ParseJID(pm.SenderJID); err == nil {
			normalizedJID := jid.ToNonAD()
			senderJID = normalizedJID.String()
			if info, err := a.wa.GetContact(ctx, normalizedJID); err == nil {
				if name := wa.BestContactName(info); name != "" {
					senderName = name
				}
				_ = a.db.UpsertContact(
					normalizedJID.String(),
					normalizedJID.User,
					info.PushName,
					info.FullName,
					info.FirstName,
					info.BusinessName,
				)
			}
		}
	}

	// Best-effort: store group metadata (and participants) when available.
	if pm.Chat.Server == types.GroupServer {
		if gi, err := a.wa.GetGroupInfo(ctx, pm.Chat); err == nil && gi != nil {
			normalizedChat := pm.Chat.ToNonAD()
			_ = a.db.UpsertGroup(normalizedChat.String(), gi.GroupName.Name, gi.OwnerJID.String(), gi.GroupCreated, gi.IsParent, gi.LinkedParentJID.String())
			var ps []store.GroupParticipant
			for _, p := range gi.Participants {
				role := "member"
				if p.IsSuperAdmin {
					role = "superadmin"
				} else if p.IsAdmin {
					role = "admin"
				}
				ps = append(ps, store.GroupParticipant{
					GroupJID: normalizedChat.String(),
					UserJID:  p.JID.ToNonAD().String(),
					Role:     role,
				})
			}
			_ = a.db.ReplaceGroupParticipants(normalizedChat.String(), ps)
		}
	}

	var mediaType, caption, filename, mimeType, directPath string
	var mediaKey, fileSha, fileEncSha []byte
	var fileLen uint64
	if pm.Media != nil {
		mediaType = pm.Media.Type
		caption = pm.Media.Caption
		filename = pm.Media.Filename
		mimeType = pm.Media.MimeType
		directPath = pm.Media.DirectPath
		mediaKey = pm.Media.MediaKey
		fileSha = pm.Media.FileSHA256
		fileEncSha = pm.Media.FileEncSHA256
		fileLen = pm.Media.FileLength
	}

	displayText := a.buildDisplayText(ctx, pm)

	return a.db.UpsertMessage(store.UpsertMessageParams{
		ChatJID:         chatJID,
		ChatName:        chatName,
		MsgID:           pm.ID,
		SenderJID:       senderJID,
		SenderName:      senderName,
		Timestamp:       pm.Timestamp,
		FromMe:          pm.FromMe,
		Text:            pm.Text,
		DisplayText:     displayText,
		MediaType:       mediaType,
		MediaCaption:    caption,
		Filename:        filename,
		MimeType:        mimeType,
		DirectPath:      directPath,
		MediaKey:        mediaKey,
		FileSHA256:      fileSha,
		FileEncSHA256:   fileEncSha,
		FileLength:      fileLen,
		ReactionToMsgID: pm.ReactionToID,
		ReactionEmoji:   pm.ReactionEmoji,
		ReplyToMsgID:    pm.ReplyToID,
		MsgOrderID:      pm.MsgOrderID,
		IsLive:          pm.IsLive,
	})
}

func (a *App) buildDisplayText(ctx context.Context, pm wa.ParsedMessage) string {
	base := baseDisplayText(pm)

	if pm.ReactionToID != "" || strings.TrimSpace(pm.ReactionEmoji) != "" {
		target := strings.TrimSpace(pm.ReactionToID)
		display := ""
		if target != "" {
			display = a.lookupMessageDisplayText(pm.Chat.String(), target)
		}
		if display == "" {
			display = "message"
		}
		emoji := strings.TrimSpace(pm.ReactionEmoji)
		if emoji != "" {
			return fmt.Sprintf("Reacted %s to %s", emoji, display)
		}
		return fmt.Sprintf("Reacted to %s", display)
	}

	if pm.ReplyToID != "" {
		quoted := strings.TrimSpace(pm.ReplyToDisplay)
		if quoted == "" {
			quoted = a.lookupMessageDisplayText(pm.Chat.String(), pm.ReplyToID)
		}
		if quoted == "" {
			quoted = "message"
		}
		if base == "" {
			base = "(message)"
		}
		return fmt.Sprintf("> %s\n%s", quoted, base)
	}

	if base == "" {
		base = "(message)"
	}
	return base
}

func baseDisplayText(pm wa.ParsedMessage) string {
	if pm.Media != nil {
		return "Sent " + mediaLabel(pm.Media.Type)
	}
	if text := strings.TrimSpace(pm.Text); text != "" {
		return text
	}
	return ""
}

func (a *App) lookupMessageDisplayText(chatJID, msgID string) string {
	if strings.TrimSpace(chatJID) == "" || strings.TrimSpace(msgID) == "" {
		return ""
	}
	msg, err := a.db.GetMessage(chatJID, msgID)
	if err != nil {
		return ""
	}
	if text := strings.TrimSpace(msg.DisplayText); text != "" {
		return text
	}
	if text := strings.TrimSpace(msg.Text); text != "" {
		return text
	}
	if strings.TrimSpace(msg.MediaType) != "" {
		return "Sent " + mediaLabel(msg.MediaType)
	}
	return ""
}

func mediaLabel(mediaType string) string {
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	switch mt {
	case "gif":
		return "gif"
	case "image":
		return "image"
	case "video":
		return "video"
	case "audio":
		return "audio"
	case "sticker":
		return "sticker"
	case "document":
		return "document"
	case "location":
		return "location"
	case "contact":
		return "contact"
	case "contacts":
		return "contacts"
	case "":
		return "message"
	default:
		return mt
	}
}

// backfillDetectedGaps scans all chats for message gaps >6 hours and sends
// on-demand history sync requests to the primary device via the existing
// WhatsApp connection. Responses are handled by the HistorySync event handler
// that's already registered in Sync().
func (a *App) backfillDetectedGaps(ctx context.Context) {
	chats, err := a.db.ListChatsWithMessages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[backfill] Failed to list chats: %v\n", err)
		return
	}

	const minGapSecs = 21600       // 6 hours
	const maxBackfillRequests = 20 // cap to avoid rate limiting / ban risk
	var totalGaps int
	var requestsSent int

	for _, chat := range chats {
		if ctx.Err() != nil {
			return
		}
		if requestsSent >= maxBackfillRequests {
			break
		}

		gaps, err := a.db.DetectGaps(chat.JID, minGapSecs)
		if err != nil || len(gaps) == 0 {
			continue
		}

		totalGaps += len(gaps)
		fmt.Fprintf(os.Stderr, "[backfill] %s: %d gap(s) detected\n", chat.Name, len(gaps))

		// Request history for the most recent gap only (to avoid flooding).
		// The response will fill in messages, and subsequent restarts will
		// catch remaining gaps.
		gap := gaps[len(gaps)-1]
		msgInfo, err := a.db.GetMessageInfoNear(chat.JID, gap.AfterTS)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[backfill] %s: failed to get message info: %v\n", chat.Name, err)
			continue
		}

		chatJID, err := types.ParseJID(msgInfo.ChatJID)
		if err != nil {
			continue
		}

		reqInfo := types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chatJID,
				IsFromMe: msgInfo.FromMe,
			},
			ID:        types.MessageID(msgInfo.MsgID),
			Timestamp: msgInfo.Timestamp,
		}

		fmt.Fprintf(os.Stderr, "[backfill] %s: requesting 50 messages before gap at %s\n",
			chat.Name, msgInfo.Timestamp.Format("15:04:05"))

		if _, err := a.wa.RequestHistorySyncOnDemand(ctx, reqInfo, 50); err != nil {
			fmt.Fprintf(os.Stderr, "[backfill] %s: request failed: %v\n", chat.Name, err)
			continue
		}
		requestsSent++

		// Delay between requests to avoid rate limiting.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}

	if totalGaps > 0 {
		skipped := totalGaps - requestsSent
		fmt.Fprintf(os.Stderr, "[backfill] Sent %d request(s) for %d gap(s).", requestsSent, totalGaps)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, " Skipped %d (rate limit cap %d).", skipped, maxBackfillRequests)
		}
		fmt.Fprintln(os.Stderr, " Responses arrive via history sync events (best-effort).")
	}
}
