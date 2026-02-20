package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	appPkg "github.com/steipete/wacli/internal/app"
	"github.com/steipete/wacli/internal/ipc"
	"github.com/steipete/wacli/internal/out"
	"github.com/steipete/wacli/internal/store"
	"github.com/steipete/wacli/internal/wa"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// syncHandler implements ipc.Handler for the sync daemon.
type syncHandler struct {
	app *appPkg.App
}

func (h *syncHandler) SendText(to, message, replyToMsgID string) (string, error) {
	if h.app == nil {
		return "", fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return "", fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return "", fmt.Errorf("whatsapp not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	toJID, err := wa.ParseUserOrJID(to)
	if err != nil {
		return "", fmt.Errorf("parse recipient: %w", err)
	}

	var msgID types.MessageID
	if replyToMsgID != "" {
		msgID, err = h.app.WA().SendReply(ctx, toJID, message, replyToMsgID)
	} else {
		msgID, err = h.app.WA().SendText(ctx, toJID, message)
	}
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}

	// Store the message in the local DB
	now := time.Now().UTC()
	chat := toJID
	chatName := h.app.WA().ResolveChatName(ctx, chat, "")
	kind := chatKindFromJID(chat)
	_ = h.app.DB().UpsertChat(chat.String(), kind, chatName, now)
	_ = h.app.DB().UpsertMessage(store.UpsertMessageParams{
		ChatJID:      chat.String(),
		ChatName:     chatName,
		MsgID:        string(msgID),
		SenderJID:    "",
		SenderName:   "me",
		Timestamp:    now,
		FromMe:       true,
		Text:         message,
		ReplyToMsgID: replyToMsgID,
	})

	return string(msgID), nil
}

func (h *syncHandler) SendReaction(chatJID, msgID, emoji string, targetFromMe bool) error {
	if h.app == nil {
		return fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return fmt.Errorf("whatsapp not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	chatJIDParsed, err := wa.ParseUserOrJID(chatJID)
	if err != nil {
		return fmt.Errorf("parse chat JID: %w", err)
	}

	reactionMsgID, err := h.app.WA().SendReaction(ctx, chatJIDParsed, msgID, emoji, targetFromMe)
	if err != nil {
		return fmt.Errorf("send reaction: %w", err)
	}

	// Store the reaction in the local DB (empty emoji = remove, still store for tracking)
	now := time.Now().UTC()
	_ = h.app.DB().UpsertMessage(store.UpsertMessageParams{
		ChatJID:         chatJIDParsed.String(),
		MsgID:           string(reactionMsgID),
		SenderJID:       "",
		SenderName:      "me",
		Timestamp:       now,
		FromMe:          true,
		ReactionToMsgID: msgID,
		ReactionEmoji:   emoji,
		IsLive:          true,
	})

	return nil
}

func (h *syncHandler) ForwardText(to, text string) (string, error) {
	if h.app == nil {
		return "", fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return "", fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return "", fmt.Errorf("whatsapp not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	toJID, err := wa.ParseUserOrJID(to)
	if err != nil {
		return "", fmt.Errorf("parse recipient: %w", err)
	}

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				IsForwarded:     proto.Bool(true),
				ForwardingScore: proto.Uint32(1),
			},
		},
	}

	msgID, err := h.app.WA().SendProtoMessage(ctx, toJID, msg)
	if err != nil {
		return "", fmt.Errorf("forward: %w", err)
	}

	now := time.Now().UTC()
	chatName := h.app.WA().ResolveChatName(ctx, toJID, "")
	kind := chatKindFromJID(toJID)
	_ = h.app.DB().UpsertChat(toJID.String(), kind, chatName, now)
	_ = h.app.DB().UpsertMessage(store.UpsertMessageParams{
		ChatJID:         toJID.String(),
		ChatName:        chatName,
		MsgID:           string(msgID),
		SenderJID:       "",
		SenderName:      "me",
		Timestamp:       now,
		FromMe:          true,
		Text:            text,
		IsForwarded:     true,
		ForwardingScore: 1,
	})

	return string(msgID), nil
}

func (h *syncHandler) MarkRead(chatJID string) error {
	if h.app == nil {
		return fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return fmt.Errorf("whatsapp not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	chatJIDParsed, err := wa.ParseUserOrJID(chatJID)
	if err != nil {
		return fmt.Errorf("parse chat JID: %w", err)
	}

	// Get recent unread messages to mark as read
	msgs, err := h.app.DB().GetRecentUnreadMessages(chatJID, 10)
	if err != nil || len(msgs) == 0 {
		// No messages to mark — just update the read timestamp
		chat, err := h.app.DB().GetChat(chatJID)
		if err != nil {
			return nil // silently ignore if chat not found
		}
		_ = h.app.DB().UpdateReadTS(chatJID, chat.LastMessageTS.UTC().Unix())
		return nil
	}

	// Group by sender (WhatsApp requires same-sender batches for groups)
	senderBatches := make(map[string][]string) // sender_jid -> []msg_id
	for _, m := range msgs {
		senderBatches[m.SenderJID] = append(senderBatches[m.SenderJID], m.MsgID)
	}

	now := time.Now()
	isGroup := wa.IsGroupJID(chatJIDParsed)

	for senderJIDStr, msgIDs := range senderBatches {
		var senderJID types.JID
		if isGroup && senderJIDStr != "" {
			senderJID, _ = wa.ParseUserOrJID(senderJIDStr)
		}

		typeIDs := make([]types.MessageID, len(msgIDs))
		for i, id := range msgIDs {
			typeIDs[i] = types.MessageID(id)
		}

		if err := h.app.WA().MarkRead(ctx, typeIDs, now, chatJIDParsed, senderJID); err != nil {
			fmt.Fprintf(os.Stderr, "[mark-read] failed for %s: %v\n", chatJID, err)
		}
	}

	// Update local read timestamp
	_ = h.app.DB().UpdateReadTS(chatJID, now.UTC().Unix())

	return nil
}

func (h *syncHandler) RequestBackfill(chatJID string, beforeTS int64, count int) error {
	if h.app == nil {
		return fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return fmt.Errorf("whatsapp not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find the message at or after the gap boundary to use as anchor
	msgInfo, err := h.app.DB().GetMessageInfoNear(chatJID, beforeTS)
	if err != nil {
		return fmt.Errorf("no messages near timestamp %d for %s: %w", beforeTS, chatJID, err)
	}

	chatJIDParsed, err := types.ParseJID(msgInfo.ChatJID)
	if err != nil {
		return fmt.Errorf("parse chat JID: %w", err)
	}

	reqInfo := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chatJIDParsed,
			IsFromMe: msgInfo.FromMe,
		},
		ID:        types.MessageID(msgInfo.MsgID),
		Timestamp: msgInfo.Timestamp,
	}

	fmt.Fprintf(os.Stderr, "[backfill-ipc] Requesting %d messages for %s before %s\n",
		count, chatJID, msgInfo.Timestamp.Format("2006-01-02 15:04:05"))

	if _, err := h.app.WA().RequestHistorySyncOnDemand(ctx, reqInfo, count); err != nil {
		return fmt.Errorf("request history sync: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[backfill-ipc] Request sent (response arrives via history sync events)\n")
	return nil
}

func (h *syncHandler) DownloadMedia(chatJID, msgID string) error {
	if h.app == nil {
		return fmt.Errorf("app not initialized")
	}
	if h.app.WA() == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}
	if !h.app.WA().IsConnected() {
		return fmt.Errorf("whatsapp not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Fprintf(os.Stderr, "[media-ipc] Downloading media for %s/%s\n", chatJID, msgID)

	info, err := h.app.DB().GetMediaDownloadInfo(chatJID, msgID)
	if err != nil {
		return fmt.Errorf("get media info: %w", err)
	}

	if info.LocalPath != "" {
		return nil // already downloaded
	}

	targetPath, err := h.app.ResolveMediaOutputPath(info, "")
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0700); err != nil {
		return fmt.Errorf("create media dir: %w", err)
	}

	if _, err := h.app.WA().DownloadMediaToFile(ctx, info.DirectPath, info.FileEncSHA256, info.FileSHA256, info.MediaKey, info.FileLength, info.MediaType, "", targetPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	now := time.Now().UTC()
	if err := h.app.DB().MarkMediaDownloaded(chatJID, msgID, targetPath, now); err != nil {
		return fmt.Errorf("mark downloaded: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[media-ipc] Downloaded %s -> %s\n", msgID, targetPath)
	return nil
}

func newSyncCmd(flags *rootFlags) *cobra.Command {
	var once bool
	var follow bool
	var idleExit time.Duration
	var downloadMedia bool
	var refreshContacts bool
	var refreshGroups bool
	var refreshAvatars bool
	var enableIPC bool
	var backfillGaps bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync messages (requires prior auth; never shows QR)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			a, lk, err := newApp(ctx, flags, true, false)
			if err != nil {
				return err
			}
			defer closeApp(a, lk)

			if err := a.EnsureAuthed(); err != nil {
				return err
			}

			mode := appPkg.SyncModeFollow
			if once {
				mode = appPkg.SyncModeOnce
			} else if follow {
				mode = appPkg.SyncModeFollow
			} else {
				mode = appPkg.SyncModeOnce
			}

			// Start IPC server if enabled (default for --follow mode)
			var ipcServer *ipc.Server
			if enableIPC && mode == appPkg.SyncModeFollow {
				handler := &syncHandler{app: a}
				ipcServer = ipc.NewServer(a.StoreDir(), handler)
				if err := ipcServer.Start(); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to start IPC server: %v\n", err)
				} else {
					defer ipcServer.Stop()
				}
			}

			res, err := a.Sync(ctx, appPkg.SyncOptions{
				Mode:            mode,
				AllowQR:         false,
				DownloadMedia:   downloadMedia,
				RefreshContacts: refreshContacts,
				RefreshGroups:   refreshGroups,
				RefreshAvatars:  refreshAvatars,
				BackfillGaps:    backfillGaps,
				IdleExit:        idleExit,
			})
			if err != nil {
				return err
			}

			if flags.asJSON {
				return out.WriteJSON(os.Stdout, map[string]any{
					"synced":          true,
					"messages_stored": res.MessagesStored,
				})
			}
			fmt.Fprintf(os.Stdout, "Messages stored: %d\n", res.MessagesStored)
			return nil
		},
	}

	cmd.Flags().BoolVar(&once, "once", false, "sync until idle and exit")
	cmd.Flags().BoolVar(&follow, "follow", true, "keep syncing until Ctrl+C")
	cmd.Flags().DurationVar(&idleExit, "idle-exit", 30*time.Second, "exit after being idle (once mode)")
	cmd.Flags().BoolVar(&downloadMedia, "download-media", false, "download media in the background during sync")
	cmd.Flags().BoolVar(&refreshContacts, "refresh-contacts", false, "refresh contacts from session store into local DB")
	cmd.Flags().BoolVar(&refreshGroups, "refresh-groups", false, "refresh joined groups (live) into local DB")
	cmd.Flags().BoolVar(&enableIPC, "enable-ipc", true, "enable IPC socket for send commands (--follow mode only)")
	cmd.Flags().BoolVar(&refreshAvatars, "refresh-avatars", true, "fetch profile pictures for contacts and groups on connect")
	cmd.Flags().BoolVar(&backfillGaps, "backfill-gaps", true, "detect and backfill message gaps on connect and reconnect (requires phone online)")
	return cmd
}
