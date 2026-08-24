package main

import (
	"sync"
	"time"

	"github.com/steipete/wacli/internal/ipc"
)

// daemonState is the shared truth about what the sync daemon is doing right
// now. The sync loop writes it; the IPC handler reads it.
//
// It exists because of the 2026-06-17 outage: the daemon used to exit the
// moment it was unauthenticated, which also tore down the IPC socket — so the
// one machine that could still be reached (the MacBook, over the SSH tunnel)
// had no way to start pairing. The daemon now stays up in needs_pairing and
// serves this state instead.
type daemonState struct {
	mu     sync.Mutex
	state  string
	detail string
	qrCode string
	qrAt   int64

	// pairReq is buffered with capacity 1 so requestPairing never blocks and
	// repeated requests coalesce into a single pending signal.
	pairReq chan struct{}
}

func newDaemonState() *daemonState {
	return &daemonState{
		state:   ipc.StateNeedsPairing,
		pairReq: make(chan struct{}, 1),
	}
}

// setSyncing marks the daemon as authenticated and syncing, clearing any stale
// pairing detail or QR code.
func (d *daemonState) setSyncing() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = ipc.StateSyncing
	d.detail = ""
	d.qrCode = ""
	d.qrAt = 0
}

// setPairing marks a pairing attempt as in progress.
func (d *daemonState) setPairing() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = ipc.StatePairing
	d.detail = ""
}

// setNeedsPairing records that the device is not linked, with the reason.
func (d *daemonState) setNeedsPairing(detail string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = ipc.StateNeedsPairing
	d.detail = detail
	d.qrCode = ""
	d.qrAt = 0
}

// setQR stores the latest QR code. WhatsApp rotates it every ~20s.
func (d *daemonState) setQR(code string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.qrCode = code
	d.qrAt = time.Now().Unix()
}

// snapshot returns the current state. Authenticated, Connected and
// LastMessageTS are filled in by the caller, which has access to the app.
func (d *daemonState) snapshot() ipc.ConnectionStatusResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ipc.ConnectionStatusResult{State: d.state, Detail: d.detail}
}

// pairingQR returns the current code, or an empty one when there is nothing
// to scan.
func (d *daemonState) pairingQR() ipc.PairingQRResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ipc.PairingQRResult{State: d.state, Code: d.qrCode, GeneratedAt: d.qrAt}
}

// requestPairing asks the sync loop to start pairing. Never blocks.
func (d *daemonState) requestPairing() {
	select {
	case d.pairReq <- struct{}{}:
	default:
	}
}

// pairRequested is the channel the sync loop waits on while idle.
func (d *daemonState) pairRequested() <-chan struct{} { return d.pairReq }
