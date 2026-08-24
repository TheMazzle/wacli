package app

import (
	"fmt"

	"go.mau.fi/whatsmeow/types/events"
)

// FatalConnectionError marks a WhatsApp connection state that reconnecting will
// never resolve: the client is rejected, unlinked, replaced or banned. Only a
// human can fix these (rebuild with a newer whatsmeow, re-scan the QR, ...).
//
// Background: on 2026-06-17 WhatsApp started rejecting our outdated client with
// a 405 and later unlinked the device. wacli swallowed both, stayed alive, and
// synced nothing for 68 days without a single error in the log. Fatal states now
// abort the sync loop so the process exits non-zero and monitoring can see it.
type FatalConnectionError struct {
	// Reason is a short, human-readable explanation of why sync cannot continue.
	Reason string
	// Action tells the operator what to do about it.
	Action string
	// Event is the whatsmeow event that triggered this.
	Event interface{}
}

func (e *FatalConnectionError) Error() string {
	return fmt.Sprintf("fatal WhatsApp connection state: %s — %s", e.Reason, e.Action)
}

// fatalConnectionEvent maps a whatsmeow event to a FatalConnectionError, or
// returns nil if the event is recoverable (ordinary disconnects, network blips).
func fatalConnectionEvent(evt interface{}) *FatalConnectionError {
	switch v := evt.(type) {
	case *events.ClientOutdated:
		return &FatalConnectionError{
			Reason: "WhatsApp rejected this client as outdated (405)",
			Action: "update whatsmeow and rebuild: cd ~/Projects/wacli && go get go.mau.fi/whatsmeow@latest && make -C infra build reinstall",
			Event:  evt,
		}
	case *events.LoggedOut:
		return &FatalConnectionError{
			Reason: fmt.Sprintf("device was unlinked by WhatsApp (on_connect=%t, reason=%s)", v.OnConnect, v.Reason),
			Action: "re-pair the device: run `wacli auth` and scan the QR code with your phone",
			Event:  evt,
		}
	case *events.StreamReplaced:
		return &FatalConnectionError{
			Reason: "another client connected with the same device keys",
			Action: "make sure only one wacli instance runs against this store, then restart the daemon",
			Event:  evt,
		}
	case *events.TemporaryBan:
		return &FatalConnectionError{
			Reason: fmt.Sprintf("account temporarily banned by WhatsApp (code=%d, expire=%s)", v.Code, v.Expire),
			Action: "wait for the ban to expire before restarting the daemon",
			Event:  evt,
		}
	case *events.ConnectFailure:
		// Recoverable connect failures surface as their own typed events above;
		// anything left here is a hard server-side rejection.
		return &FatalConnectionError{
			Reason: fmt.Sprintf("WhatsApp refused the connection (reason=%s, message=%q)", v.Reason, v.Message),
			Action: "check the wacli log and WhatsApp's linked devices screen",
			Event:  evt,
		}
	}
	return nil
}
