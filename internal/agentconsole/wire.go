// Package agentconsole contains the ephemeral terminal transport. Nothing in this
// package writes terminal input, output or session permits to telemetry/spool.
package agentconsole

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"golang.org/x/net/websocket"
)

const (
	SocketPath   = "/run/monik-console/broker.sock"
	MaxFrame     = 24 << 10
	MaxInput     = 4096
	MaxOutput    = 8192
	OutputBudget = 16 << 20
	Idle         = 10 * time.Minute
	Lifetime     = time.Hour
)

type Message struct {
	Type    string `json:"type"`
	Session string `json:"session,omitempty"`
	Ticket  string `json:"ticket,omitempty"`
	Data    string `json:"data,omitempty"`
	User    string `json:"user,omitempty"`
	Message string `json:"message,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
}

func Size(cols, rows int) bool { return cols >= 20 && cols <= 300 && rows >= 5 && rows <= 120 }
func ID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func Decode(b []byte) (Message, error) {
	var m Message
	if len(b) > MaxFrame || !utf8.Valid(b) || jsonutil.Validate(b) != nil {
		return m, fmt.Errorf("invalid terminal frame")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return m, fmt.Errorf("terminal object required")
	}
	for k, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return m, fmt.Errorf("null terminal field")
		}
		switch k {
		case "type", "session", "ticket", "data", "user", "message", "cols", "rows", "seq":
		default:
			return m, fmt.Errorf("unknown terminal field")
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return m, fmt.Errorf("invalid terminal field type")
	}
	if len(m.Type) > 24 || len(m.Session) > 64 || len(m.Ticket) > 64 || len(m.Data) > 12000 || len(m.User) > 64 || len(m.Message) > 256 || m.Seq < 0 {
		return m, fmt.Errorf("terminal field limit")
	}
	return m, nil
}

// ValidateInput permits no target, command, cwd, environment or executable field.
func ValidateInput(m Message, last int64) bool {
	if m.Ticket != "" || m.User != "" || m.Message != "" || m.Seq != last+1 {
		return false
	}
	switch m.Type {
	case "input":
		return len(m.Data) > 0 && len(m.Data) <= MaxInput && m.Cols == 0 && m.Rows == 0
	case "resize":
		return m.Data == "" && Size(m.Cols, m.Rows)
	case "close":
		return m.Data == "" && m.Cols == 0 && m.Rows == 0
	}
	return false
}
func OutputBytes(m Message) ([]byte, error) {
	if m.Type != "output" || m.Ticket != "" || m.User != "" || m.Message != "" || m.Cols != 0 || m.Rows != 0 || m.Seq != 0 {
		return nil, fmt.Errorf("invalid output")
	}
	b, e := base64.StdEncoding.Strict().DecodeString(m.Data)
	if e != nil || len(b) > MaxOutput {
		return nil, fmt.Errorf("output limit")
	}
	return b, nil
}

// Socket wraps exactly one live connection with bounded reads and serialized
// writes. Timeouts fail the session instead of dropping/replaying keystrokes.
type Socket struct {
	WS *websocket.Conn
	mu sync.Mutex
}

func (s *Socket) Read() (Message, error) {
	var b []byte
	s.WS.MaxPayloadBytes = MaxFrame
	if e := websocket.Message.Receive(s.WS, &b); e != nil {
		return Message{}, e
	}
	return Decode(b)
}
func (s *Socket) Send(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.WS.SetWriteDeadline(time.Now().Add(5 * time.Second))
	b, e := json.Marshal(m)
	if e != nil || len(b) > MaxFrame {
		return fmt.Errorf("frame limit")
	}
	return websocket.Message.Send(s.WS, string(b))
}
func (s *Socket) Close() error { return s.WS.Close() }

type Local struct {
	Conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
}

func NewLocal(c net.Conn) *Local { return &Local{Conn: c, r: bufio.NewReaderSize(c, MaxFrame+1)} }
func (l *Local) Read() (Message, error) {
	b, e := l.r.ReadSlice('\n')
	if e != nil {
		return Message{}, e
	}
	return Decode(b)
}
func (l *Local) Send(m Message) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	b, e := json.Marshal(m)
	if e != nil || len(b) > MaxFrame {
		return fmt.Errorf("frame limit")
	}
	b = append(b, '\n')
	for len(b) > 0 {
		n, e := l.Conn.Write(b)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func (l *Local) Close() error { return l.Conn.Close() }
