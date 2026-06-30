package server

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func TestFloorSeedDeterministic(t *testing.T) {
	a := floorSeed(42, 3)
	b := floorSeed(42, 3)
	if a != b {
		t.Fatalf("floorSeed(42,3) = %d then %d, want equal", a, b)
	}
	if floorSeed(42, 3) == floorSeed(42, 4) {
		t.Error("floorSeed(42,3) == floorSeed(42,4): floors must diverge")
	}
	if floorSeed(42, 3) == floorSeed(7, 3) {
		t.Error("floorSeed(42,3) == floorSeed(7,3): runs must diverge")
	}
}

// TestFreshFloorNeverStartsAtTickZero pins a real bug found in manual
// browser testing: the binary wire's keyframe sentinel is baseline-tick 0
// (protocol/binary.go), and the client acks a view by echoing its own tick
// back — so a freshly built World's first view, if it ever carried Tick 0,
// would have its ack misread as the unrelated "client lost its state, send
// a keyframe" signal. That one mistaken keyframe then poisons every
// subsequent delta (each only encodes changes from a base the client never
// actually received in full), so the player's own actor — and everyone
// else's — silently vanished from their view forever after a floor swap,
// even though the server's own world state was completely correct
// (confirmed via the unfiltered ?format=json wire during triage). Caught
// live: a death→hideout eject left "YOU DIED" stuck on screen because the
// hideout's first view had zero actors in it. newFloorSim's BeginTick bump
// is the fix; this test guards the invariant it depends on.
func TestFreshFloorNeverStartsAtTickZero(t *testing.T) {
	cfg := Config{Map: &protocol.MapSpec{Width: 20, Height: 20, Rooms: 3}}
	s, err := buildFloor(content.DB(), cfg, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.W.Tick == 0 {
		t.Error("buildFloor's World starts at Tick 0 — its first view would collide with the binary wire's keyframe/ack sentinel")
	}
	if h := buildHideout(content.DB(), 1); h.W.Tick == 0 {
		t.Error("buildHideout's World starts at Tick 0 — its first view would collide with the binary wire's keyframe/ack sentinel")
	}
}

func TestDeathXPPenalty(t *testing.T) {
	for _, xp := range []int64{0, 1, 7, 99, 100, 100_000} {
		got := deathXPPenalty(xp)
		if got < 0 || got > xp {
			t.Errorf("deathXPPenalty(%d) = %d, want within [0, %d]", xp, got, xp)
		}
	}
	if got := deathXPPenalty(100); got != 50 {
		t.Errorf("deathXPPenalty(100) = %d, want 50", got)
	}
}

// TestCharacterExtractInjectRoundTrip: level, XP, equipment (with its sheet
// modifiers), and inventory all survive an extract → inject hop into a
// brand new World, and every carried item gets a fresh ID — DESIGN.md §14's
// "item IDs are world-local; re-mint at injection".
func TestCharacterExtractInjectRoundTrip(t *testing.T) {
	db := content.DB()
	s := sim.New(db, 1)
	id, err := s.Spawn("player", space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	a := s.W.ActorByID(id)
	a.SetLevel(3)
	a.XP = 50

	belt := core.Item{ID: s.W.AllocID(), Base: db.BaseItems["leather_belt"], Implicit: fm.FromInt(15)}
	a.Inventory = append(a.Inventory, belt)
	if !items.Equip(s.W, a, belt.ID, core.EquipAuto) {
		t.Fatal("equip failed")
	}
	ring := core.Item{ID: s.W.AllocID(), Base: db.BaseItems["iron_ring"], Implicit: fm.FromInt(5)}
	a.Inventory = append(a.Inventory, ring)

	wantLife := a.MaxLife()
	ch := extractCharacter(a)

	s2 := sim.New(db, 2)
	s2.GenerateMap(space.MapSpec{Width: 20, Height: 20, Rooms: 3})
	// Burn through a few of world 2's IDs first, so a re-minted item's ID
	// can't coincidentally land on the same number as world 1's — two
	// independent worlds both start counting at 1, so numeric equality
	// alone wouldn't prove re-minting happened.
	for i := 0; i < 5; i++ {
		s2.W.AllocID()
	}
	injected, err := injectCharacter(s2.W, ch, s2.W.Grid.Spawn)
	if err != nil {
		t.Fatal(err)
	}

	if injected.Level != 3 || injected.XP != 50 {
		t.Errorf("level/xp = %d/%d, want 3/50", injected.Level, injected.XP)
	}
	if injected.MaxLife() != wantLife {
		t.Errorf("MaxLife = %v, want %v — equipped belt's life implicit must carry over", injected.MaxLife(), wantLife)
	}
	eq := injected.Equipment[core.EquipBelt]
	if eq == nil || eq.Base.ID != "leather_belt" {
		t.Fatalf("belt not equipped after injection: %+v", injected.Equipment)
	}
	if eq.ID == belt.ID {
		t.Error("equipped item ID was not re-minted")
	}
	if len(injected.Inventory) != 1 || injected.Inventory[0].Base.ID != "iron_ring" {
		t.Fatalf("inventory not carried: %+v", injected.Inventory)
	}
	if injected.Inventory[0].ID == ring.ID {
		t.Error("inventory item ID was not re-minted")
	}
}

func TestBuildFloorScalesMonsterLevel(t *testing.T) {
	cfg := Config{
		Map:     &protocol.MapSpec{Width: 30, Height: 30, Rooms: 5},
		Scatter: []protocol.Scatter{{Def: "zombie", Count: 3}},
	}
	s, err := buildFloor(content.DB(), cfg, floorSeed(1, 4), 4)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, a := range s.W.Actors {
		if a.Def.ID != "zombie" {
			continue
		}
		found++
		if a.Level != 4 {
			t.Errorf("zombie level = %d, want 4 (floor index)", a.Level)
		}
	}
	if found != 3 {
		t.Errorf("found %d zombies, want 3", found)
	}
}

func newDescentInstance(t *testing.T) *Instance {
	t.Helper()
	in, err := New(content.DB(), Config{
		Seed:    1,
		Descent: true,
		Map:     &protocol.MapSpec{Width: 24, Height: 24, Rooms: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// addTestClient spawns a fresh character into in's current floor and wires
// up a minimal *client (no real transport) the way spawnClient would.
func addTestClient(t *testing.T, in *Instance) *client {
	t.Helper()
	id, err := in.sim.Spawn(in.cfg.PlayerDef, in.sim.W.Grid.Spawn)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{actor: id}
	in.clients = append(in.clients, c)
	ch := extractCharacter(in.sim.W.ActorByID(id))
	c.lastChar = &ch
	return c
}

// killClient simulates "Step just ran and this client's actor died this
// tick": tombstoned and compacted away (so only c.lastChar can recover its
// state), with a matching EvDeath in LastEvents — exactly what processRun
// inspects.
func killClient(in *Instance, c *client) {
	in.sim.W.ActorByID(c.actor).Dead = true
	in.sim.W.EndTick()
	in.sim.W.LastEvents = []core.Event{{Kind: core.EvDeath, Actor: c.actor}}
}

// enterDungeonFromHideout simulates pressing G at the hideout's stairs — the
// only real path into floor 1 — and fails the test if it doesn't land there.
func enterDungeonFromHideout(t *testing.T, in *Instance, c *client) {
	t.Helper()
	if !in.run.inHideout {
		t.Fatal("enterDungeonFromHideout: instance is not in the hideout")
	}
	in.sim.W.ActorByID(c.actor).Pos = in.sim.W.Grid.Stairs
	if !in.processRun([]*client{c}, nil) {
		t.Fatal("processRun did not leave the hideout")
	}
	if in.run.inHideout {
		t.Fatal("processRun left in.run.inHideout = true after entering the dungeon")
	}
}

// newDungeonInstance is newDescentInstance, advanced past the hideout onto
// floor 1 — for tests about ordinary in-dungeon behavior, where the hideout
// itself isn't what's under test.
func newDungeonInstance(t *testing.T) (*Instance, *client) {
	t.Helper()
	in := newDescentInstance(t)
	c := addTestClient(t, in)
	enterDungeonFromHideout(t, in, c)
	return in, c
}

func TestNewDescentInstanceStartsInHideout(t *testing.T) {
	in := newDescentInstance(t)
	if !in.run.inHideout || in.run.started {
		t.Errorf("fresh descent instance: inHideout=%v started=%v, want true/false", in.run.inHideout, in.run.started)
	}
}

func TestProcessRunEnterDungeonFromHideout(t *testing.T) {
	in := newDescentInstance(t)
	c := addTestClient(t, in)
	enterDungeonFromHideout(t, in, c)

	if in.run.floor != 1 || in.run.maxFloor != 1 || !in.run.started {
		t.Errorf("after leaving the hideout: floor=%d maxFloor=%d started=%v, want 1/1/true",
			in.run.floor, in.run.maxFloor, in.run.started)
	}
	if in.run.portalFloor != 1 || in.run.portalPos != in.sim.W.Grid.Spawn {
		t.Errorf("portal not anchored to floor 1's entrance: floor=%d pos=%v, want 1/%v",
			in.run.portalFloor, in.run.portalPos, in.sim.W.Grid.Spawn)
	}
}

func TestProcessRunDeathReturnsToHideoutAndConsumesCharge(t *testing.T) {
	in, c := newDungeonInstance(t)
	in.sim.W.ActorByID(c.actor).XP = 100
	c.lastChar.xp = 100
	wantFloor, wantPortalFloor, startCharges := in.run.floor, in.run.portalFloor, in.run.portalUses

	killClient(in, c)
	if !in.processRun(nil, nil) {
		t.Fatal("processRun did not report a swap on death")
	}
	if !in.run.inHideout {
		t.Error("death did not land the party in the hideout")
	}
	if in.run.portalUses != startCharges-1 {
		t.Errorf("portalUses = %d, want %d", in.run.portalUses, startCharges-1)
	}
	// The run is paused, not lost: floor/score/portal survive a hideout trip.
	if in.run.floor != wantFloor || in.run.portalFloor != wantPortalFloor {
		t.Errorf("floor/portalFloor = %d/%d, want unchanged %d/%d", in.run.floor, in.run.portalFloor, wantFloor, wantPortalFloor)
	}
	na := in.sim.W.ActorByID(c.actor)
	if na == nil {
		t.Fatal("client has no actor after the eject")
	}
	if na.XP != 50 {
		t.Errorf("XP after death = %d, want 50 (halved)", na.XP)
	}
}

func TestProcessRunResumeFromHideoutAfterDeath(t *testing.T) {
	in, c := newDungeonInstance(t)
	// Descend once more and plant a portal at the new floor's stairs (a
	// point guaranteed walkable there, unlike an arbitrary coordinate) —
	// somewhere distinct from floor 2's own entrance, so resuming can prove
	// it actually used portalFloor/portalPos and not just "floor 2's spawn".
	in.sim.W.ActorByID(c.actor).Pos = in.sim.W.Grid.Stairs
	if !in.processRun([]*client{c}, nil) {
		t.Fatal("processRun did not advance to floor 2")
	}
	plantPos := in.sim.W.Grid.Stairs
	if plantPos == in.sim.W.Grid.Spawn {
		t.Skip("floor 2 happened to generate with stairs == spawn (single room)")
	}
	in.sim.W.ActorByID(c.actor).Pos = plantPos
	in.processRun(nil, []*client{c})
	if in.run.floor != 2 || in.run.portalFloor != 2 || in.run.portalPos != plantPos {
		t.Fatalf("setup: floor/portalFloor/portalPos = %d/%d/%v, want 2/2/%v", in.run.floor, in.run.portalFloor, in.run.portalPos, plantPos)
	}

	killClient(in, c)
	if !in.processRun(nil, nil) {
		t.Fatal("processRun did not eject to the hideout")
	}

	enterDungeonFromHideout(t, in, c)
	if in.run.floor != 2 {
		t.Errorf("resume landed on floor %d, want 2 (the planted portal's floor)", in.run.floor)
	}
	na := in.sim.W.ActorByID(c.actor)
	if na == nil || na.Pos != plantPos {
		t.Errorf("resume position = %v, want the planted spot %v", na, plantPos)
	}
}

func TestProcessRunOverWhenChargesExhausted(t *testing.T) {
	in, c := newDungeonInstance(t)
	in.run.portalUses = 0
	in.run.maxFloor = 5 // pretend this run had gotten deep

	killClient(in, c)
	if !in.processRun(nil, nil) {
		t.Fatal("processRun did not report a swap on run-over")
	}
	if !in.run.inHideout || in.run.started {
		t.Errorf("reset run should land fresh in the hideout: inHideout=%v started=%v", in.run.inHideout, in.run.started)
	}
	if in.run.floor != 1 || in.run.maxFloor != 1 || in.run.portalUses != portalChargesStart {
		t.Errorf("run not reset to a fresh start: %+v", in.run)
	}
	na := in.sim.W.ActorByID(c.actor)
	if na == nil || na.Level != 1 || len(na.Inventory) != 0 {
		t.Errorf("client not reduced to a fresh level-1 character: %+v", na)
	}
	found := false
	for _, ev := range in.eventBuf {
		if ev.Kind == "run_over" && ev.Amount == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("missing a run_over event carrying the final score: %+v", in.eventBuf)
	}
}

func TestProcessRunDescendAdvancesFloor(t *testing.T) {
	in, c := newDungeonInstance(t)
	in.sim.W.ActorByID(c.actor).Pos = in.sim.W.Grid.Stairs

	if !in.processRun([]*client{c}, nil) {
		t.Fatal("processRun did not report a swap on descend")
	}
	if in.run.floor != 2 || in.run.maxFloor != 2 {
		t.Errorf("floor/maxFloor = %d/%d, want 2/2", in.run.floor, in.run.maxFloor)
	}
	if in.sim.W.ActorByID(c.actor) == nil {
		t.Fatal("client has no actor on the new floor")
	}
}

func TestProcessRunDescendRequiresRange(t *testing.T) {
	in, c := newDungeonInstance(t)
	a := in.sim.W.ActorByID(c.actor)
	a.Pos = space.V(0, 0)
	if space.Dist(a.Pos, in.sim.W.Grid.Stairs) <= descendRange {
		t.Skip("stairs happened to generate at the origin for this seed")
	}

	if in.processRun([]*client{c}, nil) {
		t.Error("processRun swapped floors despite being out of stairs range")
	}
	if in.run.floor != 1 {
		t.Errorf("floor = %d, want unchanged 1", in.run.floor)
	}
}

func TestProcessRunPortalPlant(t *testing.T) {
	in, c := newDungeonInstance(t)
	pos := space.V(fm.FromInt(3), fm.FromInt(4))
	in.sim.W.ActorByID(c.actor).Pos = pos

	if in.processRun(nil, []*client{c}) {
		t.Error("planting a portal should not swap the world")
	}
	if in.run.portalFloor != 1 || in.run.portalPos != pos {
		t.Errorf("portal not planted at the actor's position: floor=%d pos=%v", in.run.portalFloor, in.run.portalPos)
	}
}

// TestProcessRunPortalPlantIgnoredInHideout: nothing to plant in the
// hideout — there's no run-floor for the portal to anchor to yet.
func TestProcessRunPortalPlantIgnoredInHideout(t *testing.T) {
	in := newDescentInstance(t)
	c := addTestClient(t, in)
	pos := space.V(fm.FromInt(1), fm.FromInt(1))
	in.sim.W.ActorByID(c.actor).Pos = pos

	if in.processRun(nil, []*client{c}) {
		t.Error("a portal-plant request should never swap the world")
	}
	if in.run.portalFloor != 1 || in.run.portalPos != (space.Vec2{}) {
		t.Errorf("portal state changed from a hideout plant attempt: floor=%d pos=%v", in.run.portalFloor, in.run.portalPos)
	}
}
