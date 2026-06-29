package server_test

import (
	"bufio"
	"context"
	"math"
	"net"
	"testing"
	"time"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

// startMapInstance boots a fast-ticking instance on a generated map (no
// scatter, so the lone player walks to the stairs unobstructed).
func startMapInstance(t *testing.T) net.Addr {
	t.Helper()
	in, err := server.New(content.DB(), server.Config{
		Addr:         "127.0.0.1:0",
		Seed:         3,
		TickInterval: 2 * time.Millisecond,
		Map:          &protocol.MapSpec{Width: 30, Height: 30, Rooms: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go in.ListenAndServe(ctx)
	addr := in.Addr()
	if addr == nil {
		t.Fatal("server failed to listen")
	}
	return addr
}

// dialWelcome connects and returns the client together with its full welcome
// frame — unlike dial(), which keeps only the actor ID. The descent test
// needs the welcome's map (for the stairs location) and floor.
func dialWelcome(t *testing.T, addr net.Addr) (*testClient, protocol.ServerMsg) {
	t.Helper()
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 4*1024*1024)
	c := &testClient{t: t, conn: conn, sc: sc}

	msg := c.next()
	if msg.Type != "welcome" || msg.Actor == 0 {
		t.Fatalf("first frame = %+v, want a welcome with an actor", msg)
	}
	if msg.V != protocol.Version {
		t.Fatalf("welcome protocol version = %d, want %d", msg.V, protocol.Version)
	}
	c.actor = msg.Actor
	return c, msg
}

// waitWelcome reads frames until a welcome arrives (skipping the snapshots
// that stream in between).
func (c *testClient) waitWelcome(desc string) protocol.ServerMsg {
	c.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		msg := c.next()
		if msg.Type == "welcome" {
			return msg
		}
	}
	c.t.Fatalf("timed out waiting for: %s", desc)
	return protocol.ServerMsg{}
}

// TestDescendSwapsFloor walks a player onto the stairs, sends descend, and
// checks the full host path: the world swaps, the client is re-welcomed onto
// floor 2 with fresh terrain and a fresh actor ID, and that actor is alive in
// the new world. The end-to-end proof that a character survives a zone
// transfer over the wire.
func TestDescendSwapsFloor(t *testing.T) {
	addr := startMapInstance(t)
	c, welcome := dialWelcome(t, addr)

	if welcome.Floor != 1 {
		t.Fatalf("started on floor %d, want 1", welcome.Floor)
	}
	if welcome.Map == nil {
		t.Fatal("grid instance sent no map in the welcome")
	}
	exit := welcome.Map.Exit

	// Walk to the stairs.
	c.send(protocol.Command{Kind: "move", X: exit.X, Y: exit.Y})
	deadline := time.Now().Add(testTimeout)
	onStairs := false
	for time.Now().Before(deadline) && !onStairs {
		msg := c.next()
		if msg.Type != "snapshot" || msg.Snapshot == nil {
			continue
		}
		if a := findActor(msg.Snapshot, c.actor); a != nil {
			dx := float64(a.Pos.X - exit.X)
			dy := float64(a.Pos.Y - exit.Y)
			onStairs = math.Hypot(dx, dy) <= 2000 // within 2u; server reach is 2.5u
		}
	}
	if !onStairs {
		t.Fatal("player never reached the stairs")
	}

	// Descend.
	c.send(protocol.Command{Kind: "descend"})
	w2 := c.waitWelcome("re-welcome onto floor 2")
	if w2.Floor != 2 {
		t.Fatalf("descended to floor %d, want 2", w2.Floor)
	}
	if w2.Map == nil {
		t.Fatal("floor 2 welcome carried no map")
	}
	if w2.Actor == 0 {
		t.Error("floor 2 welcome assigned no actor")
	}
	// (The actor ID may repeat — a monster-free floor mints the injected
	// player as entity 1 again. Item-ID re-minting is what must not collide;
	// TestCharacterRoundTrip covers that.)
	c.actor = w2.Actor

	c.waitSnapshot("player alive on floor 2", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		return a != nil && a.Life > 0
	})
}
