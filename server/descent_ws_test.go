package server_test

// End-to-end descent tests over the real WS binary wire: a client that
// walks, descends, dies, and travels exactly like web/client.js does —
// including the full-reset re-welcome handling.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

// nextAny reads one frame of either kind. Binary views are decoded against
// the view history and acked with the current welcome generation; text
// frames are returned as messages, and a welcome among them resets the
// decoder state (views, actor, gen) the way the real client resets.
func (c *wsClient) nextAny() (*protocol.Snapshot, *protocol.ServerMsg) {
	c.t.Helper()
	kind, data := c.read()
	if kind == websocket.MessageText {
		var msg protocol.ServerMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			c.t.Fatalf("bad text frame: %v", err)
		}
		if msg.Type == "welcome" {
			c.actor = msg.Actor
			c.gen = msg.Gen
			c.views = map[uint64]*protocol.Snapshot{}
			c.welcome = msg
		}
		return nil, &msg
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
		c.send(fmt.Sprintf(`{"kind":"ack","tick":%d,"gen":%d}`, view.Tick, c.gen))
	}
	return &view, nil
}

// waitWelcome pumps frames (acking views) until a welcome satisfies pred.
func (c *wsClient) waitWelcome(desc string, timeout time.Duration, pred func(*protocol.ServerMsg) bool) *protocol.ServerMsg {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, msg := c.nextAny(); msg != nil && msg.Type == "welcome" && pred(msg) {
			return msg
		}
	}
	c.t.Fatalf("timed out waiting for welcome: %s", desc)
	return nil
}

// waitViewAny is waitView but welcome-tolerant: floor swaps mid-wait are
// applied instead of tripping the decoder.
func (c *wsClient) waitViewAny(desc string, pred func(*protocol.Snapshot) bool) *protocol.Snapshot {
	c.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if v, _ := c.nextAny(); v != nil && pred(v) {
			return v
		}
	}
	c.t.Fatalf("timed out waiting for: %s", desc)
	return nil
}

func TestDescentOverTheWire(t *testing.T) {
	url := startWS(t, server.Config{
		Seed: 3,
		// SendEvery 5: at the test's 2ms tick a per-tick send rate outruns
		// what this decode-and-ack loop can drain, and the backlog makes
		// every view stale. The real cadence is what ships anyway.
		SendEvery:  5,
		Map:        &protocol.MapSpec{Width: 20, Height: 20, Rooms: 4},
		Scatter:    []protocol.Scatter{{Def: "training_dummy", Count: 2}},
		Portals:    3,
		StartFloor: 1,
	})
	c := dialWS(t, url, true)

	// The first welcome advertises the descent: generation, stairs, run.
	if c.welcome.Gen != 1 {
		t.Errorf("welcome gen = %d, want 1", c.welcome.Gen)
	}
	if c.welcome.Stairs == nil {
		t.Fatal("descent welcome has no stairs")
	}
	run := c.welcome.Run
	if run == nil || run.Floor != 1 || run.Run != 1 || run.Portal == nil {
		t.Fatalf("welcome run = %+v, want run 1 floor 1 with a portal", run)
	}

	// Walk to the stairs, re-issuing the move as the client would; send
	// descend once in range and expect the re-welcome for floor 2.
	stairs := *c.welcome.Stairs
	arrived := false
	deadline := time.Now().Add(20 * time.Second)
	var welcome2 *protocol.ServerMsg
	lastMove := time.Time{}
	for welcome2 == nil && time.Now().Before(deadline) {
		if time.Since(lastMove) > 200*time.Millisecond {
			c.send(fmt.Sprintf(`{"kind":"move","x":%d,"y":%d}`, stairs.X, stairs.Y))
			lastMove = time.Now()
		}
		view, msg := c.nextAny()
		if msg != nil && msg.Type == "welcome" && msg.Gen == 2 {
			welcome2 = msg
			break
		}
		if view == nil {
			continue
		}
		if a := findActor(view, c.actor); a != nil {
			dx, dy := a.Pos.X-stairs.X, a.Pos.Y-stairs.Y
			if dx*dx+dy*dy < 1_900*1_900 {
				arrived = true
				c.send(`{"kind":"descend"}`)
			}
		}
	}
	if !arrived || welcome2 == nil {
		t.Fatalf("never descended (arrived=%v)", arrived)
	}
	if welcome2.Run == nil || welcome2.Run.Floor != 2 || welcome2.Run.Best != 2 {
		t.Fatalf("floor-2 welcome run = %+v", welcome2.Run)
	}
	if welcome2.Stairs == nil || welcome2.Map == nil {
		t.Fatal("floor-2 welcome missing stairs or map")
	}

	// The first frame of the new world must be a keyframe (encoder reset),
	// the pack must be floor-scaled, and commands must still work.
	view, baseTick := c.nextView()
	if baseTick != 0 {
		t.Fatalf("first post-swap frame deltas against tick %d, want keyframe", baseTick)
	}
	self := findActor(view, c.actor)
	if self == nil {
		t.Fatal("own actor missing after the swap")
	}
	monsters := 0
	c.waitViewAny("floor-2 view with the scaled pack", func(s *protocol.Snapshot) bool {
		monsters = 0
		for i := range s.Actors {
			if s.Actors[i].Def == "training_dummy" {
				monsters++
				if s.Actors[i].Level != 2 {
					t.Fatalf("floor-2 dummy level = %d, want 2", s.Actors[i].Level)
				}
			}
		}
		return monsters == 3 // count 2 + 1 per floor past 1
	})
	// Move toward the new floor's stairs — always walkable, always far from
	// the spawn room (a fixed offset could land inside a wall and no-op).
	c.send(fmt.Sprintf(`{"kind":"move","x":%d,"y":%d}`, welcome2.Stairs.X, welcome2.Stairs.Y))
	start := self.Pos
	c.waitViewAny("movement works in the new world", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		return a != nil && (a.Pos.X != start.X || a.Pos.Y != start.Y)
	})
}

func TestDeathEjectAndRunOverOverTheWire(t *testing.T) {
	url := startWS(t, server.Config{
		Seed:       5,
		SendEvery:  1,
		Map:        &protocol.MapSpec{Width: 16, Height: 16, Rooms: 3},
		Scatter:    []protocol.Scatter{{Def: "zombie", Count: 6}},
		Portals:    1,
		StartFloor: 1,
	})
	c := dialWS(t, url, true)
	if c.welcome.Run == nil || c.welcome.Run.Portals != 1 {
		t.Fatalf("welcome run = %+v, want 1 portal", c.welcome.Run)
	}

	// Stand there and let the pack work. First death: ejected to the
	// portal, one portal use spent.
	sawEject := false
	w2 := c.waitWelcome("death eject", 30*time.Second, func(m *protocol.ServerMsg) bool { return m.Gen == 2 })
	if w2.Run == nil || w2.Run.Portals != 0 || w2.Run.Floor != 1 {
		t.Fatalf("eject welcome run = %+v, want floor 1 with 0 portals", w2.Run)
	}
	// The client is alive again on the rebuilt floor.
	c.waitViewAny("respawned at the portal", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		if a == nil {
			return false
		}
		if a.Life <= 0 {
			t.Fatal("respawned dead")
		}
		return true
	})

	// Second death: no portals left — the run is over, a new one begins.
	w3 := c.waitWelcome("run over", 30*time.Second, func(m *protocol.ServerMsg) bool { return m.Gen == 3 })
	if w3.Run == nil || w3.Run.Run != 2 || w3.Run.Floor != 1 || w3.Run.Portals != 1 {
		t.Fatalf("new-run welcome run = %+v, want run 2, floor 1, portals restocked", w3.Run)
	}
	_ = sawEject
}

func TestHideoutRoundTripOverTheWire(t *testing.T) {
	url := startWS(t, server.Config{
		Seed:       3,
		SendEvery:  1,
		Map:        &protocol.MapSpec{Width: 20, Height: 20, Rooms: 4},
		Scatter:    []protocol.Scatter{{Def: "training_dummy", Count: 1}},
		Portals:    2,
		StartFloor: 1,
	})
	c := dialWS(t, url, true)

	// The portal starts at the spawn room, where we stand. Enter it.
	c.send(`{"kind":"enter_portal"}`)
	w2 := c.waitWelcome("hideout welcome", 10*time.Second, func(m *protocol.ServerMsg) bool { return m.Gen == 2 })
	if w2.Run == nil || w2.Run.Floor != 0 || w2.Run.Portals != 1 {
		t.Fatalf("hideout welcome run = %+v, want floor 0 with 1 portal left", w2.Run)
	}
	if w2.Stairs != nil {
		t.Error("the hideout has stairs?")
	}
	c.waitViewAny("hideout is safe", func(s *protocol.Snapshot) bool {
		for i := range s.Actors {
			if s.Actors[i].Team != 1 {
				t.Fatalf("monster in the hideout: %+v", s.Actors[i])
			}
		}
		return findActor(s, c.actor) != nil
	})

	// Step back through: free, back on floor 1.
	c.send(`{"kind":"enter_portal"}`)
	w3 := c.waitWelcome("return welcome", 10*time.Second, func(m *protocol.ServerMsg) bool { return m.Gen == 3 })
	if w3.Run == nil || w3.Run.Floor != 1 || w3.Run.Portals != 1 {
		t.Fatalf("return welcome run = %+v, want floor 1 still with 1 portal", w3.Run)
	}
}
