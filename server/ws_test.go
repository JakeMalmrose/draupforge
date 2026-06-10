package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

// TestWebSocketClient runs the join/move flow over the WS transport: same
// frames as TCP, different wire.
func TestWebSocketClient(t *testing.T) {
	in, err := server.New(content.DB(), server.Config{
		Addr:         "127.0.0.1:0",
		Seed:         1,
		TickInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go in.ListenAndServe(ctx)
	if in.Addr() == nil {
		t.Fatal("server failed to listen")
	}

	hs := httptest.NewServer(http.HandlerFunc(in.HandleWS))
	t.Cleanup(hs.Close)
	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http")

	dialCtx, dialCancel := context.WithTimeout(ctx, testTimeout)
	defer dialCancel()
	ws, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })

	read := func() protocol.ServerMsg {
		t.Helper()
		rctx, rcancel := context.WithTimeout(ctx, testTimeout)
		defer rcancel()
		_, data, err := ws.Read(rctx)
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		var msg protocol.ServerMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("bad ws frame: %v", err)
		}
		return msg
	}

	welcome := read()
	if welcome.Type != "welcome" || welcome.Actor == 0 {
		t.Fatalf("first frame = %+v, want welcome", welcome)
	}

	wctx, wcancel := context.WithTimeout(ctx, testTimeout)
	defer wcancel()
	if err := ws.Write(wctx, websocket.MessageText, []byte(`{"kind":"move","x":0,"y":5000}`)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		msg := read()
		if msg.Type != "snapshot" || msg.Snapshot == nil {
			continue
		}
		if a := findActor(msg.Snapshot, welcome.Actor); a != nil && a.Pos.Y > 1000 {
			if a.Radius == 0 {
				t.Error("actor snapshot missing radius (renderer needs it)")
			}
			return // moved: full round trip over WS works
		}
	}
	t.Fatal("timed out waiting for movement over websocket")
}
