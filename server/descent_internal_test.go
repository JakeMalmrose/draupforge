package server

// Run-rule unit tests: drive the descent state machine directly on the tick
// goroutine's data (no ticker running, no sockets — a recorded fake
// transport catches the welcomes). The WS descent tests cover the same flow
// over the real wire; these pin the rules deterministically.

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// fakeTransport records outbound frames.
type fakeTransport struct {
	mu     sync.Mutex
	frames [][]byte
}

func (f *fakeTransport) ReadFrame() ([]byte, error) { select {} }
func (f *fakeTransport) WriteFrame(frame []byte, binary bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(frame))
	copy(cp, frame)
	f.frames = append(f.frames, cp)
	return nil
}
func (f *fakeTransport) Close() error { return nil }

// lastWelcome parses the newest welcome frame the transport saw.
func (f *fakeTransport) lastWelcome(t *testing.T) *protocol.ServerMsg {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.frames) - 1; i >= 0; i-- {
		var msg protocol.ServerMsg
		if err := json.Unmarshal(f.frames[i], &msg); err != nil {
			continue
		}
		if msg.Type == "welcome" {
			return &msg
		}
	}
	t.Fatal("no welcome frame recorded")
	return nil
}

// descentInstance builds a descent world and joins one fake client at pos
// (or the spawn room when pos is zero), mirroring what spawnClient does.
func descentInstance(t *testing.T, portals int) (*Instance, *client, *fakeTransport) {
	t.Helper()
	in, err := New(content.DB(), Config{
		Seed:    9,
		Map:     &protocol.MapSpec{Width: 20, Height: 20, Rooms: 4},
		Scatter: []protocol.Scatter{{Def: "training_dummy", Count: 2}},
		Portals: portals,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := &fakeTransport{}
	c := &client{tr: tr, mode: modeBinary, gen: 1}
	id, err := in.sim.Spawn(in.cfg.PlayerDef, in.sim.W.Grid.Spawn)
	if err != nil {
		t.Fatal(err)
	}
	c.actor = id
	in.clients = append(in.clients, c)
	// Prime the character copy the way runTick does each tick.
	a := in.sim.W.ActorByID(id)
	c.lastChar, c.hasChar = core.ExtractCharacter(a), true
	return in, c, tr
}

// killActor tombstones and compacts a client's actor, the state the world
// is really in when handleDeaths runs (death compacts inside Step).
func killActor(in *Instance, c *client) {
	in.sim.W.ActorByID(c.actor).Dead = true
	in.sim.W.EndTick()
}

func TestHandleDeathsEjectsAndPenalizes(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	c.lastChar.Level = 3
	c.lastChar.XP = 500
	in.floor, in.portalFloor = 4, 2 // died deep, portal planted on floor 2
	in.portalPos = in.sim.W.Grid.Spawn
	killActor(in, c)

	in.handleDeaths([]*client{c})

	if in.portalsLeft != 2 {
		t.Errorf("portalsLeft = %d, want 2", in.portalsLeft)
	}
	if in.floor != 2 {
		t.Errorf("floor = %d, want the portal floor 2", in.floor)
	}
	wantXP := int64(500 - progress.XPToNext(3)/deathXPPenaltyDiv)
	a := in.sim.W.ActorByID(c.actor)
	if a == nil {
		t.Fatal("dead client was not respawned at the portal")
	}
	if a.XP != wantXP || a.Level != 3 {
		t.Errorf("respawn level/xp = %d/%d, want 3/%d", a.Level, a.XP, wantXP)
	}
	if a.Life != a.MaxLife() {
		t.Errorf("respawn life = %v, want full %v", a.Life, a.MaxLife())
	}
	w := tr.lastWelcome(t)
	if w.Gen != 2 || w.Run == nil || w.Run.Floor != 2 || w.Run.Portals != 2 {
		t.Errorf("welcome = gen %d run %+v, want gen 2 on floor 2 with 2 portals", w.Gen, w.Run)
	}
}

func TestHandleDeathsNeverDeLevels(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	c.lastChar.Level = 3
	c.lastChar.XP = 10 // less than the penalty
	killActor(in, c)

	in.handleDeaths([]*client{c})

	a := in.sim.W.ActorByID(c.actor)
	if a.Level != 3 || a.XP != 0 {
		t.Errorf("level/xp = %d/%d, want 3/0 (penalty floors at the level)", a.Level, a.XP)
	}
}

func TestHandleDeathsRunOver(t *testing.T) {
	in, c, tr := descentInstance(t, 0) // no portal budget: first death ends the run
	in.floor = 3
	in.best = 3
	killActor(in, c)

	in.handleDeaths([]*client{c})

	if in.run != 2 {
		t.Errorf("run = %d, want 2 (a fresh run began)", in.run)
	}
	if in.floor != 1 || in.portalFloor != 1 {
		t.Errorf("floor/portalFloor = %d/%d, want 1/1", in.floor, in.portalFloor)
	}
	if in.best != 3 {
		t.Errorf("best = %d, want the old depth 3 kept as the score", in.best)
	}
	w := tr.lastWelcome(t)
	if w.Run == nil || w.Run.Run != 2 || w.Run.Floor != 1 || w.Run.Best != 3 {
		t.Errorf("welcome run = %+v, want run 2, floor 1, best 3", w.Run)
	}
	found := false
	for _, ev := range in.eventBuf {
		if ev.Kind == "run_over" && ev.Amount == 3000 {
			found = true
		}
	}
	if !found {
		t.Errorf("no run_over event with the depth reached; events = %+v", in.eventBuf)
	}
}

func TestDescendScalesTheNextFloor(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	// Stand on the stairs and take them.
	in.sim.W.ActorByID(c.actor).Pos = in.stairs
	in.runTick(nil, []*client{c}, nil, nil)

	if in.floor != 2 || in.best != 2 {
		t.Fatalf("floor/best = %d/%d, want 2/2", in.floor, in.best)
	}
	dummies := 0
	for _, a := range in.sim.W.Actors {
		if a.Def.ID != "training_dummy" {
			continue
		}
		dummies++
		if a.Level != 2 {
			t.Errorf("floor-2 monster level = %d, want 2", a.Level)
		}
	}
	if dummies != 3 { // scatter count 2, +1 per floor past 1
		t.Errorf("floor-2 pack size = %d, want 3", dummies)
	}
	w := tr.lastWelcome(t)
	if w.Gen != 2 || w.Stairs == nil || w.Run == nil || w.Run.Floor != 2 {
		t.Errorf("welcome = gen %d stairs %v run %+v, want gen 2 with stairs on floor 2", w.Gen, w.Stairs, w.Run)
	}
	// The player transferred: same character, new world.
	if a := in.sim.W.ActorByID(c.actor); a == nil || a.Def.ID != "player" {
		t.Errorf("player did not arrive on floor 2")
	}
}

func TestDescendRequiresStandingAtStairs(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	// The spawn room is far from the stairs by construction.
	in.runTick(nil, []*client{c}, nil, nil)
	if in.floor != 1 {
		t.Errorf("descended from across the map: floor = %d", in.floor)
	}
}

func TestPortalTravelHideoutAndBack(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	// The portal starts at the floor-1 spawn, where the player stands.
	in.runTick(nil, nil, []*client{c}, nil)

	if in.floor != 0 {
		t.Fatalf("floor = %d, want 0 (the hideout)", in.floor)
	}
	if in.portalsLeft != 2 {
		t.Errorf("portalsLeft = %d, want 2 (hideout trip costs one)", in.portalsLeft)
	}
	if len(in.sim.W.Actors) != 1 {
		t.Errorf("hideout has %d actors, want just the player (it is safe)", len(in.sim.W.Actors))
	}
	w := tr.lastWelcome(t)
	if w.Stairs != nil {
		t.Errorf("hideout welcome advertises stairs at %v", w.Stairs)
	}
	if w.Run == nil || w.Run.Floor != 0 || w.Run.Portal == nil {
		t.Errorf("hideout welcome run = %+v, want floor 0 with a portal", w.Run)
	}

	// Step back through: free, and back on the anchor floor.
	in.runTick(nil, nil, []*client{c}, nil)
	if in.floor != 1 || in.portalsLeft != 2 {
		t.Errorf("floor/portals = %d/%d after return, want 1/2 (return is free)", in.floor, in.portalsLeft)
	}
	if a := in.sim.W.ActorByID(c.actor); a == nil {
		t.Error("player did not return from the hideout")
	}
}

func TestPortalTravelNeedsUsesLeft(t *testing.T) {
	in, c, _ := descentInstance(t, 0)
	in.runTick(nil, nil, []*client{c}, nil)
	if in.floor != 0 && in.floor != 1 {
		t.Fatalf("unexpected floor %d", in.floor)
	}
	if in.floor == 0 {
		t.Error("traveled to the hideout with no portal uses left")
	}
}

func TestPlantPortalMovesTheAnchor(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	// Descend twice by teleporting to the stairs (host-level test shortcut).
	for i := 0; i < 2; i++ {
		in.sim.W.ActorByID(c.actor).Pos = in.stairs
		in.runTick(nil, []*client{c}, nil, nil)
	}
	if in.floor != 3 {
		t.Fatalf("floor = %d, want 3", in.floor)
	}
	a := in.sim.W.ActorByID(c.actor)
	in.runTick(nil, nil, nil, []*client{c})
	if in.portalFloor != 3 || in.portalPos != a.Pos {
		t.Errorf("portal = floor %d at %v, want floor 3 at %v", in.portalFloor, in.portalPos, a.Pos)
	}
	// Death now ejects to floor 3, not floor 1.
	killActor(in, c)
	in.handleDeaths([]*client{c})
	if in.floor != 3 || in.portalsLeft != 2 {
		t.Errorf("after death: floor/portals = %d/%d, want 3/2", in.floor, in.portalsLeft)
	}
}

// TestMoveAfterDescend: the world must stay fully playable after a swap —
// a move toward the new floor's stairs (always walkable) actually walks.
// This is the regression test for the tick-0 baseline collision's sibling
// failure mode: commands silently dying against a swapped world.
func TestMoveAfterDescend(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	in.sim.W.ActorByID(c.actor).Pos = in.stairs
	in.runTick(nil, []*client{c}, nil, nil) // descend to floor 2

	a := in.sim.W.ActorByID(c.actor)
	start := a.Pos
	in.sim.Step([]core.Command{{Actor: c.actor, Kind: core.CmdMove, Point: in.stairs}})
	for i := 0; i < 30; i++ {
		in.sim.Step(nil)
	}
	if a.Pos == start {
		t.Fatal("player never moved after the floor swap")
	}
}

func TestFloorSeedsAreReplayable(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	s1, err := in.buildFloor(5)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := in.buildFloor(5)
	if err != nil {
		t.Fatal(err)
	}
	if s1.W.Hash() != s2.W.Hash() {
		t.Error("rebuilding the same floor produced a different world")
	}
	s3, err := in.buildFloor(6)
	if err != nil {
		t.Fatal(err)
	}
	if s1.W.Hash() == s3.W.Hash() {
		t.Error("adjacent floors hashed identically — seeds are not deriving")
	}
	if space.Dist(farthestWalkable(s1.W.Grid), s1.W.Grid.Spawn) < fm.FromInt(5) {
		t.Error("stairs suspiciously close to the spawn room")
	}
}
