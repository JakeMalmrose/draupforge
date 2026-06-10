package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

const testTimeout = 5 * time.Second

// startInstance boots a fast-ticking instance on an ephemeral port.
func startInstance(t *testing.T, spawns []protocol.ScriptSpawn) net.Addr {
	t.Helper()
	in, err := server.New(content.DB(), server.Config{
		Addr:         "127.0.0.1:0",
		Seed:         1,
		TickInterval: 2 * time.Millisecond,
		Spawns:       spawns,
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

type testClient struct {
	t     *testing.T
	conn  net.Conn
	sc    *bufio.Scanner
	actor uint64
}

func dial(t *testing.T, addr net.Addr) *testClient {
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
	c.actor = msg.Actor
	return c
}

func (c *testClient) next() protocol.ServerMsg {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(testTimeout))
	if !c.sc.Scan() {
		c.t.Fatalf("connection ended: %v", c.sc.Err())
	}
	var msg protocol.ServerMsg
	if err := json.Unmarshal(c.sc.Bytes(), &msg); err != nil {
		c.t.Fatalf("bad frame: %v", err)
	}
	return msg
}

func (c *testClient) send(cmd protocol.Command) {
	c.t.Helper()
	raw, _ := json.Marshal(cmd)
	if _, err := c.conn.Write(append(raw, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// waitSnapshot reads frames until pred is satisfied or the deadline hits.
func (c *testClient) waitSnapshot(desc string, pred func(*protocol.Snapshot) bool) *protocol.Snapshot {
	c.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		msg := c.next()
		if msg.Type != "snapshot" || msg.Snapshot == nil {
			continue
		}
		if pred(msg.Snapshot) {
			return msg.Snapshot
		}
	}
	c.t.Fatalf("timed out waiting for: %s", desc)
	return nil
}

func findActor(snap *protocol.Snapshot, id uint64) *protocol.ActorSnap {
	for i := range snap.Actors {
		if snap.Actors[i].ID == id {
			return &snap.Actors[i]
		}
	}
	return nil
}

func TestJoinMoveAndSnapshot(t *testing.T) {
	addr := startInstance(t, []protocol.ScriptSpawn{{Def: "training_dummy", X: 50000, Y: 0}})
	c := dial(t, addr)

	// The scenario spawn and our player are both in the world.
	c.waitSnapshot("player and dummy present", func(s *protocol.Snapshot) bool {
		return findActor(s, c.actor) != nil && len(s.Actors) == 2
	})

	c.send(protocol.Command{Kind: "move", X: 3000, Y: 4000})
	c.waitSnapshot("player moved toward (3,4)", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		return a != nil && a.Pos.Y > 1000
	})
}

func TestServerOverridesActorField(t *testing.T) {
	addr := startInstance(t, nil)
	a := dial(t, addr)
	b := dial(t, addr)

	// A claims to command B. The server must reassign the command to A.
	a.send(protocol.Command{Actor: b.actor, Kind: "move", X: 0, Y: 9000})

	a.waitSnapshot("A moved (command reassigned to sender)", func(s *protocol.Snapshot) bool {
		me := findActor(s, a.actor)
		return me != nil && me.Pos.Y > 1000
	})
	snap := a.waitSnapshot("B visible", func(s *protocol.Snapshot) bool {
		return findActor(s, b.actor) != nil
	})
	if bb := findActor(snap, b.actor); bb.Pos.Y != 0 {
		t.Errorf("B moved to y=%d off A's forged command — authority leak", bb.Pos.Y)
	}
}

func TestDisconnectDespawns(t *testing.T) {
	addr := startInstance(t, nil)
	a := dial(t, addr)
	b := dial(t, addr)

	a.waitSnapshot("both players present", func(s *protocol.Snapshot) bool {
		return findActor(s, b.actor) != nil
	})
	b.conn.Close()
	a.waitSnapshot("B despawned after disconnect", func(s *protocol.Snapshot) bool {
		return findActor(s, b.actor) == nil
	})
}

// TestCommandBeforeWelcomeIsNotLost: a client that fires commands the
// instant it connects (racing its own spawn) must still see them applied.
// This is exactly what `echo cmd | nc` does.
func TestCommandBeforeWelcomeIsNotLost(t *testing.T) {
	addr := startInstance(t, nil)

	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// Send before reading anything — no welcome handshake.
	if _, err := conn.Write([]byte(`{"kind":"move","x":0,"y":9000}` + "\n")); err != nil {
		t.Fatal(err)
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 4*1024*1024)
	c := &testClient{t: t, conn: conn, sc: sc}
	msg := c.next()
	if msg.Type != "welcome" {
		t.Fatalf("first frame = %q, want welcome", msg.Type)
	}
	c.actor = msg.Actor

	c.waitSnapshot("pre-welcome move command applied", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		return a != nil && a.Pos.Y > 1000
	})
}

func TestCombatHappensLive(t *testing.T) {
	// A zombie near the spawn point should aggro and hurt the player
	// without any client input — the sim runs server-side regardless.
	addr := startInstance(t, []protocol.ScriptSpawn{{Def: "zombie", X: 4000, Y: 0}})
	c := dial(t, addr)

	c.waitSnapshot("zombie damaged the idle player", func(s *protocol.Snapshot) bool {
		a := findActor(s, c.actor)
		return a != nil && a.Life < a.MaxLife
	})
}
