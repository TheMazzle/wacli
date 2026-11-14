package ipc

import (
	"encoding/base64"
	"os"
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
	polls          []string
	uploaded       []byte
}

func (f *fakeHandler) SendText(string, string, string) (string, error) { return "", nil }
func (f *fakeHandler) SendReaction(string, string, string, bool) error { return nil }
func (f *fakeHandler) ForwardText(string, string) (string, error)      { return "", nil }
func (f *fakeHandler) UploadMedia(data []byte, name, mime string) (string, string, error) {
	f.uploaded = data
	return "TOKEN", "image", nil
}
func (f *fakeHandler) SendMedia(to, token, caption string) (string, error) { return "M1", nil }
func (f *fakeHandler) SendLocation(to string, lat, lon float64, name, addr string) (string, error) {
	return "L1", nil
}
func (f *fakeHandler) SendContact(to, jid, name string) (string, error) { return "C1", nil }
func (f *fakeHandler) SendPoll(to, q string, opts []string, sel int) (string, error) {
	f.polls = append(f.polls, q)
	return "POLL1", nil
}
func (f *fakeHandler) MarkRead(string) error                    { return nil }
func (f *fakeHandler) RequestBackfill(string, int64, int) error { return nil }
func (f *fakeHandler) DownloadMedia(string, string) error       { return nil }

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
//
// t.TempDir() is not usable here: its paths blow past the 104-character
// sun_path limit for Unix sockets on macOS, giving "bind: invalid argument".
func newTestServer(t *testing.T, h Handler) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wacli-ipc-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
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

func TestSendPollOverIPC(t *testing.T) {
	h := &fakeHandler{}
	c := newTestServer(t, h)

	resp, err := c.send(Request{Command: "send_poll", To: "g@g.us", PollQuestion: "Etentje?", PollOptions: []string{"Vr", "Za"}, PollSelectable: 1})
	if err != nil || !resp.Success {
		t.Fatalf("send_poll: resp=%+v err=%v", resp, err)
	}
	if len(h.polls) != 1 || h.polls[0] != "Etentje?" {
		t.Fatalf("handler got %v", h.polls)
	}

	resp, _ = c.send(Request{Command: "send_poll", To: "g@g.us", PollQuestion: "Q", PollOptions: []string{"A", "A"}})
	if resp.Success {
		t.Fatal("duplicate options must be rejected")
	}
}

func TestValidatePoll(t *testing.T) {
	cases := []struct {
		q    string
		opts []string
		sel  int
		ok   bool
	}{
		{"Q", []string{"A", "B"}, 1, true},
		{"Q", []string{"A", "B"}, 0, true},
		{"", []string{"A", "B"}, 1, false},
		{"Q", []string{"A"}, 1, false},
		{"Q", []string{"A", " "}, 1, false},
		{"Q", []string{"A", "B"}, 3, false},
		{"Q", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13"}, 1, false},
	}
	for _, c := range cases {
		if err := ValidatePoll(c.q, c.opts, c.sel); (err == nil) != c.ok {
			t.Errorf("ValidatePoll(%q, %v, %d) err=%v, want ok=%v", c.q, c.opts, c.sel, err, c.ok)
		}
	}
}

func TestUploadMediaOverIPC(t *testing.T) {
	h := &fakeHandler{}
	c := newTestServer(t, h)

	// 2 MB: ruim boven een standaard buffer, het hele bestand moet aankomen.
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = byte(i)
	}
	resp, err := c.send(Request{Command: "upload_media", MediaData: base64.StdEncoding.EncodeToString(data), Filename: "a.jpg"})
	if err != nil || !resp.Success {
		t.Fatalf("upload_media: %+v %v", resp, err)
	}
	if len(h.uploaded) != len(data) || h.uploaded[len(data)-1] != data[len(data)-1] {
		t.Fatalf("handler got %d bytes", len(h.uploaded))
	}
	resp, _ = c.send(Request{Command: "upload_media", MediaData: "not base64!"})
	if resp.Success {
		t.Fatal("invalid base64 must be rejected")
	}
	resp, _ = c.send(Request{Command: "send_media", To: "g@g.us"})
	if resp.Success {
		t.Fatal("send_media without token must be rejected")
	}
}
