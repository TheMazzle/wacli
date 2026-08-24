package ipc

import (
	"sync"
	"testing"
)

// fakeHandler implements Handler for tests. Only the connection/pairing methods
// carry behaviour; the messaging methods are inert.
type fakeHandler struct {
	mu             sync.Mutex
	status         ConnectionStatusResult
	qr             PairingQRResult
	startPairCalls int
	startPairErr   error
}

func (f *fakeHandler) SendText(string, string, string) (string, error) { return "", nil }
func (f *fakeHandler) SendReaction(string, string, string, bool) error { return nil }
func (f *fakeHandler) ForwardText(string, string) (string, error)      { return "", nil }
func (f *fakeHandler) MarkRead(string) error                           { return nil }
func (f *fakeHandler) RequestBackfill(string, int64, int) error        { return nil }
func (f *fakeHandler) DownloadMedia(string, string) error              { return nil }

func (f *fakeHandler) ConnectionStatus() ConnectionStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeHandler) StartPairing() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startPairCalls++
	return f.startPairErr
}

func (f *fakeHandler) PairingQR() PairingQRResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.qr
}

// newTestServer starts an IPC server on a temp store dir and returns a client.
func newTestServer(t *testing.T, h Handler) *Client {
	t.Helper()
	dir := t.TempDir()
	srv := NewServer(dir, h)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(srv.Stop)
	return NewClient(dir)
}

func TestConnectionStatusOverIPC(t *testing.T) {
	h := &fakeHandler{status: ConnectionStatusResult{
		State:         StateSyncing,
		Authenticated: true,
		Connected:     true,
		LastMessageTS: 1787600896,
	}}
	c := newTestServer(t, h)

	got, err := c.ConnectionStatus()
	if err != nil {
		t.Fatalf("ConnectionStatus: %v", err)
	}
	if got.State != StateSyncing {
		t.Errorf("State = %q, want %q", got.State, StateSyncing)
	}
	if !got.Authenticated || !got.Connected {
		t.Errorf("Authenticated=%t Connected=%t, want both true", got.Authenticated, got.Connected)
	}
	if got.LastMessageTS != 1787600896 {
		t.Errorf("LastMessageTS = %d, want 1787600896", got.LastMessageTS)
	}
}

// The whole point of the pairing flow is that it works while the daemon is NOT
// authenticated — that is exactly when the user needs it.
func TestConnectionStatusReportsNeedsPairing(t *testing.T) {
	h := &fakeHandler{status: ConnectionStatusResult{
		State:  StateNeedsPairing,
		Detail: "device was unlinked by WhatsApp",
	}}
	c := newTestServer(t, h)

	got, err := c.ConnectionStatus()
	if err != nil {
		t.Fatalf("ConnectionStatus: %v", err)
	}
	if got.State != StateNeedsPairing {
		t.Errorf("State = %q, want %q", got.State, StateNeedsPairing)
	}
	if got.Detail == "" {
		t.Error("Detail is empty; the UI needs a reason to show")
	}
}

func TestStartPairingOverIPC(t *testing.T) {
	h := &fakeHandler{}
	c := newTestServer(t, h)

	if err := c.StartPairing(); err != nil {
		t.Fatalf("StartPairing: %v", err)
	}
	// Idempotent: asking twice must not be an error.
	if err := c.StartPairing(); err != nil {
		t.Fatalf("second StartPairing: %v", err)
	}
	h.mu.Lock()
	calls := h.startPairCalls
	h.mu.Unlock()
	if calls != 2 {
		t.Errorf("handler called %d times, want 2", calls)
	}
}

func TestPairingQROverIPC(t *testing.T) {
	h := &fakeHandler{qr: PairingQRResult{
		State:       StatePairing,
		Code:        "2@abcdef",
		GeneratedAt: 1787600896,
	}}
	c := newTestServer(t, h)

	got, err := c.PairingQR()
	if err != nil {
		t.Fatalf("PairingQR: %v", err)
	}
	if got.Code != "2@abcdef" {
		t.Errorf("Code = %q, want %q", got.Code, "2@abcdef")
	}
	if got.State != StatePairing {
		t.Errorf("State = %q, want %q", got.State, StatePairing)
	}
}

// While syncing there is no QR to show; the client must get an empty code and
// the current state rather than an error.
func TestPairingQREmptyWhenSyncing(t *testing.T) {
	h := &fakeHandler{qr: PairingQRResult{State: StateSyncing}}
	c := newTestServer(t, h)

	got, err := c.PairingQR()
	if err != nil {
		t.Fatalf("PairingQR: %v", err)
	}
	if got.Code != "" {
		t.Errorf("Code = %q, want empty while syncing", got.Code)
	}
	if got.State != StateSyncing {
		t.Errorf("State = %q, want %q", got.State, StateSyncing)
	}
}
