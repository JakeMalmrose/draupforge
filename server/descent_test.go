package server_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

// nextWelcome drains binary view frames (banking them as baselines, exactly
// like nextView) until the next "welcome" text frame arrives — the signal
// of a floor transition (DESIGN.md §14: zone transfer = re-welcome).
func (c *wsClient) nextWelcome() protocol.ServerMsg {
	c.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		kind, data := c.read()
		if kind == websocket.MessageBinary {
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
			continue
		}
		var msg protocol.ServerMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			c.t.Fatal(err)
		}
		if msg.Type == "welcome" {
			c.welcome = msg
			c.actor = msg.Actor
			c.views = map[uint64]*protocol.Snapshot{} // fresh world: old baselines are dead
			return msg
		}
	}
	c.t.Fatal("timed out waiting for a welcome frame")
	return protocol.ServerMsg{}
}

// walkToStairsAndDescend moves to the terrain's stairs marker, sends
// "descend", and returns the resulting re-welcome — used for both the
// hideout's exit and an ordinary in-dungeon floor transition, since the
// wire protocol treats them identically (DESIGN.md §14: zone transfer =
// re-welcome).
func walkToStairsAndDescend(t *testing.T, c *wsClient, stairs protocol.Vec) protocol.ServerMsg {
	t.Helper()
	c.send(fmt.Sprintf(`{"kind":"move","x":%d,"y":%d}`, stairs.X, stairs.Y))
	c.waitView("player reaches the stairs", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		if a == nil {
			return false
		}
		dx, dy := a.Pos.X-stairs.X, a.Pos.Y-stairs.Y
		return dx*dx+dy*dy <= 1_000*1_000 // well inside the server's 2-unit descend range
	})
	c.send(`{"kind":"descend"}`)
	welcome := c.nextWelcome()
	if welcome.Actor == 0 {
		t.Fatal("re-welcome missing an actor")
	}
	c.waitView("alive after the transition", func(s *protocol.Snapshot) bool {
		return findActor(s, welcome.Actor) != nil
	})
	return welcome
}

// TestDescentStairsAdvanceFloor drives the real WS wire end to end: connect
// to a descent instance (landing in the hideout), leave it into floor 1,
// then descend again into floor 2 — confirming a full re-welcome each time
// with the score updated and our (re-minted) actor alive on the new map.
func TestDescentStairsAdvanceFloor(t *testing.T) {
	url := startWS(t, server.Config{
		Seed:    1,
		Descent: true,
		Map:     &protocol.MapSpec{Width: 28, Height: 28, Rooms: 3},
	})
	c := dialWS(t, url, true)

	if c.welcome.Floor != 0 {
		t.Fatalf("initial welcome floor = %d, want 0 (the hideout)", c.welcome.Floor)
	}
	if c.welcome.PortalCharges == 0 {
		t.Error("initial welcome has no portal charges")
	}
	if c.welcome.Map == nil {
		t.Fatal("descent welcome missing terrain")
	}

	welcome := walkToStairsAndDescend(t, c, c.welcome.Map.Stairs)
	if welcome.Floor != 1 || welcome.MaxFloor != 1 {
		t.Fatalf("welcome after leaving the hideout: floor/max = %d/%d, want 1/1", welcome.Floor, welcome.MaxFloor)
	}
	if welcome.Map == nil {
		t.Fatal("floor 1 welcome missing terrain")
	}

	welcome = walkToStairsAndDescend(t, c, welcome.Map.Stairs)
	if welcome.Floor != 2 || welcome.MaxFloor != 2 {
		t.Fatalf("welcome after descending: floor/max = %d/%d, want 2/2", welcome.Floor, welcome.MaxFloor)
	}
}
