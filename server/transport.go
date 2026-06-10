package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// transport is one client connection, whatever the wire. A frame is one JSON
// document: a protocol.Command inbound, a protocol.ServerMsg outbound.
type transport interface {
	ReadFrame() ([]byte, error)
	WriteFrame([]byte) error
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

func (t *tcpTransport) WriteFrame(frame []byte) error {
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

func (t *wsTransport) WriteFrame(frame []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return t.conn.Write(ctx, websocket.MessageText, frame)
}

func (t *wsTransport) Close() error {
	return t.conn.Close(websocket.StatusNormalClosure, "")
}

// HandleWS upgrades an HTTP request to a WebSocket client of this instance.
// It blocks until the client disconnects, like any connection read loop.
func (in *Instance) HandleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Dev server: accept any origin so LAN machines can join.
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	c := &client{tr: &wsTransport{conn: ws}}
	in.mu.Lock()
	in.joins = append(in.joins, c)
	in.mu.Unlock()
	in.readLoop(r.Context(), c)
}
