package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// Regression test for the 2026-06-17 silent-failure incident: WhatsApp rejected
// the outdated client with a 405 and later unlinked the device. wacli kept
// running as if healthy and synced nothing for 68 days.
//
// Fatal, non-recoverable connection states MUST abort the follow loop with an
// error so the process exits non-zero and launchd/monitoring can see it.
func TestSyncAbortsOnFatalConnectionEvents(t *testing.T) {
	cases := []struct {
		name string
		evt  interface{}
	}{
		{"client outdated (405)", &events.ClientOutdated{}},
		{"logged out / device unlinked", &events.LoggedOut{Reason: events.ConnectFailureLoggedOut}},
		{"stream replaced", &events.StreamReplaced{}},
		{"temporary ban", &events.TemporaryBan{Code: events.TempBanBlockedByUsers}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			f := newFakeWA()
			a.wa = f
			f.connectEvents = []interface{}{tc.evt}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := a.Sync(ctx, SyncOptions{Mode: SyncModeFollow})
			if err == nil {
				t.Fatalf("Sync returned nil error on %T; fatal state must abort the sync loop", tc.evt)
			}
			var fatal *FatalConnectionError
			if !errors.As(err, &fatal) {
				t.Fatalf("Sync error = %v (%T); want *FatalConnectionError", err, err)
			}
			if fatal.Reason == "" {
				t.Errorf("FatalConnectionError.Reason is empty; the log must say why sync died")
			}
		})
	}
}

// Only LoggedOut is fixable by scanning a QR. The daemon must stay alive for
// that one so a client (Whatslack on the MacBook) can drive pairing over IPC;
// the others need a human at the CLI, so exiting loudly is correct.
func TestFatalConnectionErrorNeedsPairing(t *testing.T) {
	cases := []struct {
		name string
		evt  interface{}
		want bool
	}{
		{"logged out", &events.LoggedOut{Reason: events.ConnectFailureLoggedOut}, true},
		{"client outdated", &events.ClientOutdated{}, false},
		{"stream replaced", &events.StreamReplaced{}, false},
		{"temporary ban", &events.TemporaryBan{Code: events.TempBanBlockedByUsers}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fatal := fatalConnectionEvent(tc.evt)
			if fatal == nil {
				t.Fatalf("fatalConnectionEvent(%T) = nil; want a fatal error", tc.evt)
			}
			if got := fatal.NeedsPairing(); got != tc.want {
				t.Errorf("NeedsPairing() = %t, want %t", got, tc.want)
			}
		})
	}
}
