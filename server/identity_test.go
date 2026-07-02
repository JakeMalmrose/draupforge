package server_test

// Identity over the real wire: the claim/whoami HTTP API and the cookie →
// WebSocket session flow, including the one-session-per-name refusal.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

// startIdentityServer boots an instance and serves its full HTTP surface
// (WS + identity API), returning the http:// base URL.
func startIdentityServer(t *testing.T) string {
	t.Helper()
	in, err := server.New(content.DB(), server.Config{
		Addr:         "127.0.0.1:0",
		Seed:         1,
		TickInterval: 2 * time.Millisecond,
		IdentityPath: filepath.Join(t.TempDir(), "ids.json"),
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
	hs := httptest.NewServer(in.Handler())
	t.Cleanup(hs.Close)
	return hs.URL
}

// claim posts a name and returns the token cookie.
func claim(t *testing.T, base, name string) *http.Cookie {
	t.Helper()
	resp, err := http.Post(base+"/api/claim", "application/json",
		bytes.NewBufferString(`{"name":"`+name+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim %q: status %d", name, resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "draupforge_token" {
			if !c.HttpOnly {
				t.Error("token cookie is not HttpOnly")
			}
			return c
		}
	}
	t.Fatal("claim set no token cookie")
	return nil
}

func TestClaimAPI(t *testing.T) {
	base := startIdentityServer(t)

	claim(t, base, "Jake")

	// Same name again (any case) is taken.
	resp, err := http.Post(base+"/api/claim", "application/json",
		bytes.NewBufferString(`{"name":"jake"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("dupe claim: status %d, want 409", resp.StatusCode)
	}

	// Whoami echoes the cookie's name; bare requests get {}.
	cookie := claim(t, base, "Someone Else")
	req, _ := http.NewRequest("GET", base+"/api/whoami", nil)
	req.AddCookie(cookie)
	wr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var who struct{ Name string }
	json.NewDecoder(wr.Body).Decode(&who)
	wr.Body.Close()
	if who.Name != "Someone Else" {
		t.Errorf("whoami = %q, want Someone Else", who.Name)
	}
}

// dialCookie opens a WS with the token cookie attached and returns the
// first JSON frame.
func dialCookie(t *testing.T, base string, cookie *http.Cookie) (*websocket.Conn, protocol.ServerMsg) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(base, "http") + "/ws"
	hdr := http.Header{}
	if cookie != nil {
		hdr.Set("Cookie", cookie.String())
	}
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg protocol.ServerMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	return ws, msg
}

func TestNamedSessionOverWire(t *testing.T) {
	base := startIdentityServer(t)
	cookie := claim(t, base, "Wirewalker")

	_, msg := dialCookie(t, base, cookie)
	if msg.Type != "welcome" || msg.Name != "Wirewalker" {
		t.Fatalf("welcome = %+v, want name Wirewalker", msg)
	}
	if msg.Roster[msg.Actor] != "Wirewalker" {
		t.Errorf("own roster entry = %q, want Wirewalker", msg.Roster[msg.Actor])
	}

	// A second session on the same identity is refused with an error frame.
	_, refusal := dialCookie(t, base, cookie)
	if refusal.Type != "error" || !strings.Contains(refusal.Error, "already connected") {
		t.Fatalf("second session got %+v, want an already-connected error", refusal)
	}

	// A guest connection on the same browser (guest=1) is fine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Cookie", cookie.String())
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws?guest=1",
		&websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var guest protocol.ServerMsg
	json.Unmarshal(data, &guest)
	if guest.Type != "welcome" || guest.Name != "" {
		t.Fatalf("guest welcome = %+v, want anonymous", guest)
	}
}
