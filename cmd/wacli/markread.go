package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steipete/wacli/internal/config"
	"github.com/steipete/wacli/internal/ipc"
	"github.com/steipete/wacli/internal/wa"
	"go.mau.fi/whatsmeow/types"
)

func newMarkReadCmd(flags *rootFlags) *cobra.Command {
	var chatJID string

	cmd := &cobra.Command{
		Use:   "mark-read",
		Short: "Mark a chat as read (sends read receipts to WhatsApp)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if chatJID == "" {
				return fmt.Errorf("--chat is required")
			}

			// Resolve store directory
			storeDir := flags.storeDir
			if storeDir == "" {
				storeDir = config.DefaultStoreDir()
			}
			storeDir, _ = filepath.Abs(storeDir)

			// Try IPC first (if sync daemon is running, use its connection)
			client := ipc.NewClient(storeDir)
			if client.IsAvailable() {
				if err := client.MarkRead(chatJID); err != nil {
					fmt.Fprintf(os.Stderr, "IPC mark-read failed (%v), trying direct mode...\n", err)
				} else {
					if flags.asJSON {
						fmt.Fprintf(os.Stdout, `{"marked_read":true,"chat_jid":%q}`+"\n", chatJID)
					} else {
						fmt.Fprintf(os.Stdout, "Marked %s as read (via IPC)\n", chatJID)
					}
					return nil
				}
			}

			// Direct mode: open own connection
			ctx, cancel := context.WithTimeout(context.Background(), flags.timeout)
			defer cancel()

			a, lk, err := newApp(ctx, flags, false, false)
			if err != nil {
				return wrapErr(err, "init")
			}
			defer closeApp(a, lk)

			if err := a.EnsureAuthed(); err != nil {
				return wrapErr(err, "auth")
			}

			if err := a.Connect(ctx, false, nil); err != nil {
				return wrapErr(err, "connect")
			}

			chatJIDParsed, err := wa.ParseUserOrJID(chatJID)
			if err != nil {
				return fmt.Errorf("parse chat JID: %w", err)
			}

			// Get recent unread messages
			msgs, err := a.DB().GetRecentUnreadMessages(chatJID, 10)
			if err != nil || len(msgs) == 0 {
				// No messages to mark — just update the read timestamp
				chat, err := a.DB().GetChat(chatJID)
				if err == nil {
					_ = a.DB().UpdateReadTS(chatJID, chat.LastMessageTS.UTC().Unix())
				}
				if flags.asJSON {
					fmt.Fprintf(os.Stdout, `{"marked_read":true,"chat_jid":%q,"messages":0}`+"\n", chatJID)
				} else {
					fmt.Fprintf(os.Stdout, "No unread messages in %s\n", chatJID)
				}
				return nil
			}

			// Group by sender for group chats
			senderBatches := make(map[string][]string)
			for _, m := range msgs {
				senderBatches[m.SenderJID] = append(senderBatches[m.SenderJID], m.MsgID)
			}

			now := time.Now()
			isGroup := wa.IsGroupJID(chatJIDParsed)
			totalMarked := 0

			for senderJIDStr, msgIDs := range senderBatches {
				var senderJID types.JID
				if isGroup && senderJIDStr != "" {
					senderJID, _ = wa.ParseUserOrJID(senderJIDStr)
				}

				typeIDs := make([]types.MessageID, len(msgIDs))
				for i, id := range msgIDs {
					typeIDs[i] = types.MessageID(id)
				}

				if err := a.WA().MarkRead(ctx, typeIDs, now, chatJIDParsed, senderJID); err != nil {
					fmt.Fprintf(os.Stderr, "[mark-read] failed for sender %s: %v\n", senderJIDStr, err)
				} else {
					totalMarked += len(msgIDs)
				}
			}

			_ = a.DB().UpdateReadTS(chatJID, now.UTC().Unix())

			if flags.asJSON {
				fmt.Fprintf(os.Stdout, `{"marked_read":true,"chat_jid":%q,"messages":%d}`+"\n", chatJID, totalMarked)
			} else {
				fmt.Fprintf(os.Stdout, "Marked %d message(s) as read in %s\n", totalMarked, chatJID)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&chatJID, "chat", "", "chat JID to mark as read (required)")
	_ = cmd.MarkFlagRequired("chat")
	return cmd
}
