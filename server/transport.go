package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/protocol"
)

// transport is one client connection, whatever the wire. Inbound frames are
// JSON protocol.Commands; outbound frames are JSON protocol.ServerMsgs or,
// when binary is set, protocol binary view frames (WS only).
type transport interface {
	ReadFrame() ([]byte, error)
	WriteFrame(frame []byte, binary bool) error
	Close() error
}

// tcpTransport frames with newlines (NDJSON) — the nc-able wire.
type tcpTransport struct {
	conn net.Conn
	sc   *bufio.Scanner
}

func newTCPTransport(conn net.Conn) *tcpTransport {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), maxLineBytes)
	return &tcpTransport{conn: conn, sc: sc}
}

func (t *tcpTransport) ReadFrame() ([]byte, error) {
	if !t.sc.Scan() {
		if err := t.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return t.sc.Bytes(), nil
}

func (t *tcpTransport) WriteFrame(frame []byte, binary bool) error {
	if binary {
		return errors.New("server: tcp transport is NDJSON-only")
	}
	t.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := t.conn.Write(frame); err != nil {
		return err
	}
	_, err := t.conn.Write([]byte{'\n'})
	return err
}

func (t *tcpTransport) Close() error { return t.conn.Close() }

// wsTransport frames with WebSocket text messages — the browser wire.
type wsTransport struct {
	conn *websocket.Conn
}

func (t *wsTransport) ReadFrame() ([]byte, error) {
	_, data, err := t.conn.Read(context.Background())
	return data, err
}

func (t *wsTransport) WriteFrame(frame []byte, binary bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	kind := websocket.MessageText
	if binary {
		kind = websocket.MessageBinary
	}
	return t.conn.Write(ctx, kind, frame)
}

func (t *wsTransport) Close() error {
	return t.conn.Close(websocket.StatusNormalClosure, "")
}

// HandleWS upgrades an HTTP request to a WebSocket client of this instance.
// It blocks until the client disconnects, like any connection read loop.
// ?format=json swaps the binary delta wire for full-JSON views (debug);
// ?guest=1 ignores any identity cookie and plays ephemerally. A token
// cookie (identity.go) resumes that identity's character — unless the
// identity is already connected, which is refused: one session per name.
func (in *Instance) HandleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Dev server: accept any origin so LAN machines can join.
		OriginPatterns: []string{"*"},
		// permessage-deflate; context takeover compresses across frames,
		// which suits view frames' heavy cross-frame redundancy.
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		return
	}
	m := modeBinary
	if r.URL.Query().Get("format") == "json" {
		m = modeJSONView
	}
	c := &client{tr: &wsTransport{conn: ws}, mode: m}
	if tok := cookieToken(r); tok != "" && r.URL.Query().Get("guest") == "" {
		name, char, ok, dup := in.ids.Connect(tok)
		switch {
		case dup:
			frame, _ := json.Marshal(protocol.ServerMsg{
				Type: "error", Error: fmt.Sprintf("%s is already connected", name),
			})
			c.send(frame, false)
			c.tr.Close()
			return
		case ok:
			c.name, c.token = name, tok
			if char != nil {
				c.lastChar, c.hasChar = *char, true
			}
		}
		// Unknown token: a stale cookie (wiped store). Play as guest; the
		// join screen offers a fresh claim next time whoami comes up empty.
	}
	in.mu.Lock()
	in.joins = append(in.joins, c)
	in.mu.Unlock()
	in.readLoop(r.Context(), c)
}
