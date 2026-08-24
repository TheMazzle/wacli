package main

import (
	"testing"

	"github.com/steipete/wacli/internal/ipc"
)

func TestDaemonStateStartsNeedingPairing(t *testing.T) {
	st := newDaemonState()
	if got := st.snapshot().State; got != ipc.StateNeedsPairing {
		t.Errorf("initial state = %q, want %q", got, ipc.StateNeedsPairing)
	}
}

func TestDaemonStateTransitions(t *testing.T) {
	st := newDaemonState()

	st.setNeedsPairing("device was unlinked")
	snap := st.snapshot()
	if snap.State != ipc.StateNeedsPairing || snap.Detail != "device was unlinked" {
		t.Errorf("after setNeedsPairing: %+v", snap)
	}

	st.setPairing()
	if got := st.snapshot().State; got != ipc.StatePairing {
		t.Errorf("after setPairing: state = %q, want %q", got, ipc.StatePairing)
	}

	st.setSyncing()
	snap = st.snapshot()
	if snap.State != ipc.StateSyncing {
		t.Errorf("after setSyncing: state = %q, want %q", snap.State, ipc.StateSyncing)
	}
	if snap.Detail != "" {
		t.Errorf("after setSyncing: Detail = %q, want empty", snap.Detail)
	}
}

func TestDaemonStateQRLifecycle(t *testing.T) {
	st := newDaemonState()

	if got := st.pairingQR().Code; got != "" {
		t.Errorf("QR before pairing = %q, want empty", got)
	}

	st.setPairing()
	st.setQR("2@abcdef")
	qr := st.pairingQR()
	if qr.Code != "2@abcdef" {
		t.Errorf("Code = %q, want %q", qr.Code, "2@abcdef")
	}
	if qr.GeneratedAt == 0 {
		t.Error("GeneratedAt = 0; the UI needs it to tell a stale code from a fresh one")
	}
	if qr.State != ipc.StatePairing {
		t.Errorf("State = %q, want %q", qr.State, ipc.StatePairing)
	}

	// Once linked there is nothing to scan; a stale code must not linger.
	st.setSyncing()
	if got := st.pairingQR().Code; got != "" {
		t.Errorf("QR after pairing succeeded = %q, want empty", got)
	}
}

func TestDaemonStatePairingRequestIsNonBlockingAndIdempotent(t *testing.T) {
	st := newDaemonState()

	// Repeated requests must never block, even with nobody listening yet.
	for i := 0; i < 5; i++ {
		st.requestPairing()
	}

	select {
	case <-st.pairRequested():
	default:
		t.Fatal("pairRequested() did not fire after requestPairing()")
	}

	// Coalesced: five requests are one pending signal, not five.
	select {
	case <-st.pairRequested():
		t.Fatal("pairRequested() fired twice; requests should coalesce")
	default:
	}
}
