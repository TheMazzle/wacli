// Package ipc provides Unix socket IPC for wacli sync/send coordination.
package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	socketName   = "wacli.sock"
	readTimeout  = 30 * time.Second
	writeTimeout = 30 * time.Second
)

// Request represents a command sent to the sync daemon.
type Request struct {
	Command      string `json:"command"` // "send_text", "send_file", "send_reaction", "mark_read", "backfill", "media_download", "forward_message", "ping", "connection_status", "start_pairing", "pairing_qr"
	To           string `json:"to,omitempty"`
	Message      string `json:"message,omitempty"`
	File         string `json:"file,omitempty"`
	Caption      string `json:"caption,omitempty"`
	ChatJID      string `json:"chat_jid,omitempty"`        // for mark_read, backfill, media_download, send_reaction
	MsgID        string `json:"msg_id,omitempty"`          // for media_download, send_reaction
	BeforeTS     int64  `json:"before_ts,omitempty"`       // for backfill: timestamp of gap boundary
	Count        int    `json:"count,omitempty"`           // for backfill: messages to request (default 50)
	ReplyToMsgID string `json:"reply_to_msg_id,omitempty"` // for send_text: reply to a specific message
	Emoji        string `json:"emoji,omitempty"`           // for send_reaction: the emoji to react with (empty = remove)
	FromMe       bool   `json:"from_me,omitempty"`         // for send_reaction: whether the target message is from the sender
	ForwardText  string `json:"forward_text,omitempty"`    // for forward_message: text content to forward
}

// Response represents the result from the sync daemon.
type Response struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// SendTextResult is returned for send_text commands.
type SendTextResult struct {
	To    string `json:"to"`
	MsgID string `json:"msg_id"`
}

// Daemon states reported by connection_status.
const (
	// StateSyncing means the daemon is authenticated and running its sync loop.
	StateSyncing = "syncing"
	// StatePairing means a QR pairing attempt is in progress.
	StatePairing = "pairing"
	// StateNeedsPairing means the device is not linked and nothing is being
	// synced. The daemon stays alive in this state precisely so a client can
	// start pairing over this socket — on a headless machine it is the only way.
	StateNeedsPairing = "needs_pairing"
)

// ConnectionStatusResult describes whether the sync is actually working.
type ConnectionStatusResult struct {
	State         string `json:"state"`
	Authenticated bool   `json:"authenticated"`
	Connected     bool   `json:"connected"`
	LastMessageTS int64  `json:"last_message_ts"`
	Detail        string `json:"detail,omitempty"`
}

// PairingQRResult carries the current QR code, if any. WhatsApp rotates the
// code every ~20s, so callers should poll and re-render.
type PairingQRResult struct {
	State       string `json:"state"`
	Code        string `json:"code,omitempty"`
	GeneratedAt int64  `json:"generated_at,omitempty"`
}

// Handler processes incoming IPC requests.
type Handler interface {
	SendText(to, message, replyToMsgID string) (msgID string, err error)
	SendReaction(chatJID, msgID, emoji string, targetFromMe bool) error
	ForwardText(to, text string) (msgID string, err error)
	MarkRead(chatJID string) error
	RequestBackfill(chatJID string, beforeTS int64, count int) error
	DownloadMedia(chatJID, msgID string) error

	// ConnectionStatus reports whether messages are actually flowing.
	ConnectionStatus() ConnectionStatusResult
	// StartPairing begins a QR pairing attempt. Idempotent: calling it while
	// already pairing or already linked is not an error.
	StartPairing() error
	// PairingQR returns the current QR code, or an empty code when there is
	// nothing to scan.
	PairingQR() PairingQRResult
}

// Server listens on a Unix socket for IPC requests.
type Server struct {
	storeDir string
	handler  Handler
	listener net.Listener
	wg       sync.WaitGroup
	done     chan struct{}
}

// NewServer creates an IPC server.
func NewServer(storeDir string, handler Handler) *Server {
	return &Server{
		storeDir: storeDir,
		handler:  handler,
		done:     make(chan struct{}),
	}
}

// SocketPath returns the path to the Unix socket.
func SocketPath(storeDir string) string {
	return filepath.Join(storeDir, socketName)
}

// Start begins listening for connections.
func (s *Server) Start() error {
	sockPath := SocketPath(s.storeDir)

	// Remove stale socket if exists
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	s.listener = listener

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop shuts down the server.
func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()
	_ = os.Remove(SocketPath(s.storeDir))
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// Recover from panics in the handler
	defer func() {
		if r := recover(); r != nil {
			s.writeResponse(conn, Response{Success: false, Error: fmt.Sprintf("internal error: %v", r)})
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		s.writeResponse(conn, Response{Success: false, Error: fmt.Sprintf("read error: %v", err)})
		return
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeResponse(conn, Response{Success: false, Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	resp := s.processRequest(req)
	s.writeResponse(conn, resp)
}

func (s *Server) processRequest(req Request) Response {
	switch req.Command {
	case "ping":
		return Response{Success: true, Data: "pong"}

	case "connection_status":
		return Response{Success: true, Data: s.handler.ConnectionStatus()}

	case "start_pairing":
		if err := s.handler.StartPairing(); err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: s.handler.ConnectionStatus()}

	case "pairing_qr":
		return Response{Success: true, Data: s.handler.PairingQR()}

	case "send_text":
		if req.To == "" || req.Message == "" {
			return Response{Success: false, Error: "to and message are required"}
		}
		msgID, err := s.handler.SendText(req.To, req.Message, req.ReplyToMsgID)
		if err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: SendTextResult{To: req.To, MsgID: msgID}}

	case "send_reaction":
		if req.ChatJID == "" || req.MsgID == "" {
			return Response{Success: false, Error: "chat_jid and msg_id are required"}
		}
		if err := s.handler.SendReaction(req.ChatJID, req.MsgID, req.Emoji, req.FromMe); err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: map[string]string{"chat_jid": req.ChatJID, "msg_id": req.MsgID, "emoji": req.Emoji}}

	case "forward_message":
		if req.To == "" || req.ForwardText == "" {
			return Response{Success: false, Error: "to and forward_text are required"}
		}
		msgID, err := s.handler.ForwardText(req.To, req.ForwardText)
		if err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: SendTextResult{To: req.To, MsgID: msgID}}

	case "mark_read":
		if req.ChatJID == "" {
			return Response{Success: false, Error: "chat_jid is required"}
		}
		if err := s.handler.MarkRead(req.ChatJID); err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: map[string]string{"chat_jid": req.ChatJID}}

	case "backfill":
		if req.ChatJID == "" {
			return Response{Success: false, Error: "chat_jid is required"}
		}
		count := req.Count
		if count <= 0 {
			count = 50
		}
		if err := s.handler.RequestBackfill(req.ChatJID, req.BeforeTS, count); err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: map[string]any{"chat_jid": req.ChatJID, "count": count}}

	case "media_download":
		if req.ChatJID == "" || req.MsgID == "" {
			return Response{Success: false, Error: "chat_jid and msg_id are required"}
		}
		if err := s.handler.DownloadMedia(req.ChatJID, req.MsgID); err != nil {
			return Response{Success: false, Error: err.Error()}
		}
		return Response{Success: true, Data: map[string]string{"chat_jid": req.ChatJID, "msg_id": req.MsgID}}

	default:
		return Response{Success: false, Error: fmt.Sprintf("unknown command: %s", req.Command)}
	}
}

func (s *Server) writeResponse(conn net.Conn, resp Response) {
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// Client connects to a running sync daemon.
type Client struct {
	storeDir string
}

// NewClient creates an IPC client.
func NewClient(storeDir string) *Client {
	return &Client{storeDir: storeDir}
}

// IsAvailable checks if the sync daemon socket exists.
func (c *Client) IsAvailable() bool {
	sockPath := SocketPath(c.storeDir)
	_, err := os.Stat(sockPath)
	return err == nil
}

// SendText sends a text message via the sync daemon.
func (c *Client) SendText(to, message string) (*SendTextResult, error) {
	req := Request{
		Command: "send_text",
		To:      to,
		Message: message,
	}

	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	// Parse the result
	data, _ := json.Marshal(resp.Data)
	var result SendTextResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}

// MarkRead marks a chat as read via the sync daemon.
func (c *Client) MarkRead(chatJID string) error {
	req := Request{
		Command: "mark_read",
		ChatJID: chatJID,
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// ConnectionStatus asks the daemon whether the sync is actually working.
func (c *Client) ConnectionStatus() (*ConnectionStatusResult, error) {
	var result ConnectionStatusResult
	if err := c.sendInto(Request{Command: "connection_status"}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// StartPairing asks the daemon to begin a QR pairing attempt.
func (c *Client) StartPairing() error {
	resp, err := c.send(Request{Command: "start_pairing"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// PairingQR fetches the current QR code. Poll this while pairing; WhatsApp
// rotates the code roughly every 20 seconds.
func (c *Client) PairingQR() (*PairingQRResult, error) {
	var result PairingQRResult
	if err := c.sendInto(Request{Command: "pairing_qr"}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// sendInto sends req and decodes a successful response's Data into out.
func (c *Client) sendInto(req Request, out any) error {
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// Ping checks if the daemon is responsive.
func (c *Client) Ping() error {
	req := Request{Command: "ping"}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func (c *Client) send(req Request) (*Response, error) {
	sockPath := SocketPath(c.storeDir)
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &resp, nil
}
