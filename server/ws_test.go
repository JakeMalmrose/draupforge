package server_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// startWS boots an instance plus an HTTP server for its WS endpoint.
func startWS(t *testing.T, cfg server.Config) string {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	if cfg.TickInterval == 0 {
		cfg.TickInterval = 2 * time.Millisecond
	}
	in, err := server.New(content.DB(), cfg)
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
	return "ws" + strings.TrimPrefix(hs.URL, "http")
}

// wsClient mirrors what web/net.js does: decode binary view frames against
// a history of received views, ack every view.
type wsClient struct {
	t       *testing.T
	ws      *websocket.Conn
	actor   uint64
	welcome protocol.ServerMsg
	views   map[uint64]*protocol.Snapshot
	acks    bool // whether to ack received views
}

func dialWS(t *testing.T, url string, acks bool) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })
	c := &wsClient{t: t, ws: ws, views: map[uint64]*protocol.Snapshot{}, acks: acks}

	kind, data := c.read()
	if kind != websocket.MessageText {
		t.Fatalf("first frame is %v, want a text welcome", kind)
	}
	if err := json.Unmarshal(data, &c.welcome); err != nil {
		t.Fatal(err)
	}
	if c.welcome.Type != "welcome" || c.welcome.Actor == 0 {
		t.Fatalf("first frame = %+v, want a welcome with an actor", c.welcome)
	}
	c.actor = c.welcome.Actor
	return c
}

func (c *wsClient) read() (websocket.MessageType, []byte) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	kind, data, err := c.ws.Read(ctx)
	if err != nil {
		c.t.Fatalf("ws read: %v", err)
	}
	return kind, data
}

func (c *wsClient) send(raw string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		c.t.Fatal(err)
	}
}

// nextView reads, decodes, stores, and (optionally) acks one binary view
// frame, returning it along with its baseline tick.
func (c *wsClient) nextView() (*protocol.Snapshot, uint64) {
	c.t.Helper()
	for {
		kind, data := c.read()
		if kind != websocket.MessageBinary {
			continue // stray text frame (shouldn't happen post-welcome)
		}
		baseTick, err := protocol.BaselineTick(data)
		if err != nil {
			c.t.Fatal(err)
		}
		view, err := protocol.DecodeViewFrame(data, c.views[baseTick])
		if err != nil {
			c.t.Fatalf("decoding view frame: %v", err)
		}
		c.views[view.Tick] = &view
		if c.acks {
			c.send(fmt.Sprintf(`{"kind":"ack","tick":%d}`, view.Tick))
		}
		return &view, baseTick
	}
}

func (c *wsClient) waitView(desc string, pred func(*protocol.Snapshot) bool) *protocol.Snapshot {
	c.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if v, _ := c.nextView(); pred(v) {
			return v
		}
	}
	c.t.Fatalf("timed out waiting for: %s", desc)
	return nil
}

// TestWebSocketBinaryDeltaFlow drives the real client wire end to end:
// versioned welcome, keyframe first, deltas once acks flow, and a full
// command round trip on top of reconstructed views.
func TestWebSocketBinaryDeltaFlow(t *testing.T) {
	url := startWS(t, server.Config{Seed: 1})
	c := dialWS(t, url, true)

	if c.welcome.V != protocol.Version {
		t.Errorf("welcome.V = %d, want %d", c.welcome.V, protocol.Version)
	}
	if c.welcome.TickHz == 0 || c.welcome.SendEvery == 0 {
		t.Errorf("welcome missing cadence: %+v", c.welcome)
	}

	view, baseTick := c.nextView()
	if baseTick != 0 {
		t.Fatalf("first view frame has baseline %d, want keyframe (0)", baseTick)
	}
	if findActor(view, c.actor) == nil {
		t.Fatalf("own actor missing from first view")
	}

	// With acks flowing, the server must switch to deltas.
	sawDelta := false
	for i := 0; i < 20 && !sawDelta; i++ {
		_, bt := c.nextView()
		sawDelta = bt != 0
	}
	if !sawDelta {
		t.Error("server never sent a delta despite acks")
	}

	// Commands still work over reconstructed views.
	c.send(`{"kind":"move","x":0,"y":5000}`)
	c.waitView("player moved via delta-reconstructed views", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		return a != nil && a.Pos.Y > 1000 && a.Radius != 0
	})
}

// TestNoAcksMeansKeyframes: a client that never acks must keep getting
// self-contained keyframes, not deltas it can't decode.
func TestNoAcksMeansKeyframes(t *testing.T) {
	url := startWS(t, server.Config{Seed: 1})
	c := dialWS(t, url, false)
	for i := 0; i < 5; i++ {
		if _, baseTick := c.nextView(); baseTick != 0 {
			t.Fatalf("view %d is a delta (baseline %d) but we never acked", i, baseTick)
		}
	}
}

// TestAckZeroResetsToKeyframe: ack 0 (the client's "I lost my state" signal)
// must force the next view back to a keyframe.
func TestAckZeroResetsToKeyframe(t *testing.T) {
	url := startWS(t, server.Config{Seed: 1})
	c := dialWS(t, url, true)

	// Get into the delta regime first.
	c.nextView()
	deadline := time.Now().Add(testTimeout)
	for {
		if _, bt := c.nextView(); bt != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never reached delta regime")
		}
	}

	c.acks = false
	c.send(`{"kind":"ack","tick":0}`)
	deadline = time.Now().Add(testTimeout)
	for {
		_, bt := c.nextView()
		if bt == 0 {
			return // keyframe arrived
		}
		if time.Now().After(deadline) {
			t.Fatal("no keyframe after ack 0")
		}
	}
}

// TestInterestCulling: with a small radius, a far-away monster must not
// appear in the client's view; the client itself always does. Uses the
// ?format=json debug mode, which shares the culled-view path.
func TestInterestCulling(t *testing.T) {
	url := startWS(t, server.Config{
		Seed:           1,
		InterestRadius: 10_000, // 10 units
		Spawns: []protocol.ScriptSpawn{
			{Def: "training_dummy", X: 50_000, Y: 0}, // 50 units out
			{Def: "training_dummy", X: 3_000, Y: 0},  // 3 units: in range
		},
	}) + "?format=json"

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })

	var actor uint64
	deadline := time.Now().Add(testTimeout)
	checked := 0
	for time.Now().Before(deadline) && checked < 5 {
		rctx, rcancel := context.WithTimeout(context.Background(), testTimeout)
		_, data, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatal(err)
		}
		var msg protocol.ServerMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		switch msg.Type {
		case "welcome":
			actor = msg.Actor
		case "snapshot":
			if findActor(msg.Snapshot, actor) == nil {
				continue // not spawned into a view yet
			}
			if len(msg.Snapshot.Actors) != 2 {
				t.Fatalf("view has %d actors, want 2 (self + near dummy): %+v",
					len(msg.Snapshot.Actors), msg.Snapshot.Actors)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("never saw a view containing our actor")
	}
}

// TestSendRateAndEventAccumulation: views arrive every SendEvery ticks, and
// events from the skipped ticks are not lost — a zombie beating on the
// player generates hit events on ticks that never get their own view.
func TestSendRateAndEventAccumulation(t *testing.T) {
	url := startWS(t, server.Config{
		Seed:      1,
		SendEvery: 5,
		Spawns:    []protocol.ScriptSpawn{{Def: "zombie", X: 4_000, Y: 0}},
	})
	c := dialWS(t, url, true)

	var last uint64
	gotHit := false
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) && !gotHit {
		view, _ := c.nextView()
		if last != 0 && view.Tick-last != 5 {
			t.Errorf("consecutive views at ticks %d and %d, want spacing 5", last, view.Tick)
		}
		last = view.Tick
		for _, ev := range view.Events {
			if ev.Kind == "hit" && ev.Other == c.actor {
				gotHit = true
			}
		}
	}
	if !gotHit {
		t.Error("never received the zombie's hit events")
	}
}
