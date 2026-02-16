package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// refreshAvatars fetches profile pictures for contacts and groups,
// prioritized by most recent activity. Uses ExistingID for change detection
// to avoid redundant downloads.
func (a *App) refreshAvatars(ctx context.Context) error {
	chats, err := a.db.ListChatsWithMessages()
	if err != nil {
		return fmt.Errorf("list chats: %w", err)
	}

	existing, err := a.db.GetAvatarIDs()
	if err != nil {
		return fmt.Errorf("get avatar IDs: %w", err)
	}

	avatarDir := filepath.Join(a.opts.StoreDir, "avatars")
	if err := os.MkdirAll(avatarDir, 0700); err != nil {
		return fmt.Errorf("create avatar dir: %w", err)
	}

	const maxFetches = 200
	const fetchDelay = 200 * time.Millisecond
	fetched := 0

	for _, chat := range chats {
		if ctx.Err() != nil {
			break
		}
		if fetched >= maxFetches {
			break
		}

		jid, err := types.ParseJID(chat.JID)
		if err != nil {
			continue
		}

		existingID := existing[chat.JID]
		params := &whatsmeow.GetProfilePictureParams{
			ExistingID: existingID,
		}

		info, err := a.wa.GetProfilePictureInfo(ctx, jid, params)
		if err != nil || info == nil {
			// nil = unchanged or unavailable (privacy); skip silently
			fetched++
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(fetchDelay):
			}
			continue
		}

		// Download the picture
		filename := chat.JID + ".jpg"
		destPath := filepath.Join(avatarDir, filename)
		if err := downloadToFile(info.URL, destPath); err != nil {
			fmt.Fprintf(os.Stderr, "[avatars] %s: download failed: %v\n", chat.JID, err)
			fetched++
			continue
		}

		// Store relative path (avatars/{jid}.jpg) so whatslack can resolve against storeDir
		relPath := filepath.Join("avatars", filename)
		_ = a.db.UpsertAvatar(chat.JID, info.ID, relPath)
		fetched++

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(fetchDelay):
		}
	}

	if fetched > 0 {
		fmt.Fprintf(os.Stderr, "[avatars] Checked %d profile picture(s).\n", fetched)
	}
	return nil
}

// handlePictureUpdate processes a live profile picture change event.
func (a *App) handlePictureUpdate(ctx context.Context, evt *events.Picture) {
	jidStr := evt.JID.ToNonAD().String()
	avatarDir := filepath.Join(a.opts.StoreDir, "avatars")

	if evt.Remove {
		_ = os.Remove(filepath.Join(avatarDir, jidStr+".jpg"))
		_ = a.db.UpsertAvatar(jidStr, "", "")
		return
	}

	info, err := a.wa.GetProfilePictureInfo(ctx, evt.JID, nil)
	if err != nil || info == nil {
		return
	}

	_ = os.MkdirAll(avatarDir, 0700)
	filename := jidStr + ".jpg"
	destPath := filepath.Join(avatarDir, filename)
	if err := downloadToFile(info.URL, destPath); err != nil {
		fmt.Fprintf(os.Stderr, "[avatars] %s: download failed: %v\n", jidStr, err)
		return
	}

	relPath := filepath.Join("avatars", filename)
	_ = a.db.UpsertAvatar(jidStr, info.ID, relPath)
}

// downloadToFile downloads a URL to a local file path atomically.
func downloadToFile(url, destPath string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("empty URL")
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Write to temp file first, then rename for atomicity
	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write: %w", err)
	}
	f.Close()

	return os.Rename(tmpPath, destPath)
}
