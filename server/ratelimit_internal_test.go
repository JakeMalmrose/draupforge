package server

// Hardening contracts: the command bucket refills at its rate and drops the
// excess, the claim limiter windows per source, and the WS door refuses
// cross-origin browsers while non-browser clients (no Origin) still pass.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/content"
)

func TestCmdBucket(t *testing.T) {
	b := newCmdBucket()
	now := b.last
	for i := 0; i < cmdBurst; i++ {
		if !b.allow(now) {
			t.Fatalf("burst budget refused command %d of %d", i+1, cmdBurst)
		}
	}
	if b.allow(now) {
		t.Fatal("command past the burst budget allowed")
	}
	// One second later the refill covers exactly the sustained rate.
	now = now.Add(time.Second)
	for i := 0; i < cmdRatePerSec; i++ {
		if !b.allow(now) {
			t.Fatalf("refilled budget refused command %d of %d", i+1, cmdRatePerSec)
		}
	}
	if b.allow(now) {
		t.Fatal("command past the refill allowed")
	}
}

func TestClaimLimiter(t *testing.T) {
	cl := newClaimLimiter()
	now := time.Now()
	for i := 0; i < claimMax; i++ {
		if !cl.allow("1.2.3.4", now) {
			t.Fatalf("claim %d of %d refused", i+1, claimMax)
		}
	}
	if cl.allow("1.2.3.4", now) {
		t.Fatal("claim past the window allowed")
	}
	if !cl.allow("5.6.7.8", now) {
		t.Fatal("another source got throttled by the first one's spam")
	}
	if !cl.allow("1.2.3.4", now.Add(claimWindow+time.Second)) {
		t.Fatal("the window never expired")
	}
}

func TestClaimEndpointThrottles(t *testing.T) {
	lb, err := NewLobby(content.DB(), Config{Addr: "127.0.0.1:0", Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(lb.Handler())
	t.Cleanup(ts.Close)
	claimStatus := func(name string) int {
		res, err := http.Post(ts.URL+"/api/claim", "application/json",
			bytes.NewBufferString(`{"name":"`+name+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for i, name := range []string{"Aa", "Bb", "Cc"} {
		if got := claimStatus(name); got != http.StatusOK {
			t.Fatalf("claim %d = %d, want 200", i+1, got)
		}
	}
	if got := claimStatus("Dd"); got != http.StatusTooManyRequests {
		t.Fatalf("fourth claim = %d, want 429", got)
	}
}

func TestWSOriginPolicy(t *testing.T) {
	lb, err := NewLobby(content.DB(), Config{
		Addr: "127.0.0.1:0", Seed: 3, TickInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { lb.ListenAndServe(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	if lb.Addr() == nil {
		t.Fatal("lobby failed to listen")
	}
	ts := httptest.NewServer(lb.Handler())
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	dial := func(origin, forwardedHost string) error {
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dcancel()
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		if forwardedHost != "" {
			hdr.Set("X-Forwarded-Host", forwardedHost)
		}
		ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
		if err == nil {
			ws.Close(websocket.StatusNormalClosure, "")
		}
		return err
	}

	if err := dial("", ""); err != nil {
		t.Errorf("originless (non-browser) dial refused: %v", err)
	}
	if err := dial(ts.URL, ""); err != nil {
		t.Errorf("same-origin dial refused: %v", err)
	}
	if err := dial("https://evil.example", ""); err == nil {
		t.Error("cross-origin dial accepted; want a 403")
	}
	// The Host-rewriting-proxy case: Origin matches the forwarded host.
	if err := dial("https://game.example", "game.example"); err != nil {
		t.Errorf("proxied same-origin dial refused: %v", err)
	}
	if err := dial("https://evil.example", "game.example"); err == nil {
		t.Error("cross-origin dial behind a proxy accepted; want a 403")
	}
}
