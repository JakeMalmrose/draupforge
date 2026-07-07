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
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/combat"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
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

// descentInstance builds a descent world starting directly on floor 1 (the
// run rules under test don't care where the run began) and joins one fake
// client at the spawn room, mirroring what spawnClient does.
func descentInstance(t *testing.T, portals int) (*Instance, *client, *fakeTransport) {
	return descentInstanceAt(t, portals, 1)
}

// descentInstanceAt is descentInstance with an explicit start floor —
// 0 exercises the default hideout start.
func descentInstanceAt(t *testing.T, portals, startFloor int) (*Instance, *client, *fakeTransport) {
	t.Helper()
	in, err := New(content.DB(), Config{
		Seed:       9,
		Map:        &protocol.MapSpec{Width: 20, Height: 20, Rooms: 4},
		Scatter:    []protocol.Scatter{{Def: "training_dummy", Count: 2}},
		Portals:    portals,
		StartFloor: startFloor,
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
	// Died deep (row 2's first floor = global 4), portal planted on the
	// entry node's second floor (global 2).
	in.node, in.fin, in.floor = trunkNodeAt(in.runSeed, 2), 1, 4
	in.portalNode, in.portalFin = trunkNodeAt(in.runSeed, 1), 2
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
	if in.floor != 1 || globalFloor(in.portalNode.Row, in.portalFin) != 1 {
		t.Errorf("floor/portal anchor = %d/%v+%d, want both at global floor 1",
			in.floor, in.portalNode, in.portalFin)
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
	in.runTick(nil, runWants{descends: []*client{c}})

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
	in.runTick(nil, runWants{descends: []*client{c}})
	if in.floor != 1 {
		t.Errorf("descended from across the map: floor = %d", in.floor)
	}
}

func TestPortalTravelHideoutAndBack(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	// The portal starts at the floor-1 spawn, where the player stands.
	in.runTick(nil, runWants{portals: []*client{c}})

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
	in.runTick(nil, runWants{portals: []*client{c}})
	if in.floor != 1 || in.portalsLeft != 2 {
		t.Errorf("floor/portals = %d/%d after return, want 1/2 (return is free)", in.floor, in.portalsLeft)
	}
	if a := in.sim.W.ActorByID(c.actor); a == nil {
		t.Error("player did not return from the hideout")
	}
}

func TestPortalTravelNeedsUsesLeft(t *testing.T) {
	in, c, _ := descentInstance(t, 0)
	in.runTick(nil, runWants{portals: []*client{c}})
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
		in.runTick(nil, runWants{descends: []*client{c}})
	}
	if in.floor != 3 {
		t.Fatalf("floor = %d, want 3", in.floor)
	}
	a := in.sim.W.ActorByID(c.actor)
	in.runTick(nil, runWants{plants: []*client{c}})
	if in.portalNode != in.node || in.portalFin != in.fin || in.portalPos != a.Pos {
		t.Errorf("portal = %v+%d at %v, want %v+%d at %v",
			in.portalNode, in.portalFin, in.portalPos, in.node, in.fin, a.Pos)
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
	in.runTick(nil, runWants{descends: []*client{c}}) // descend to floor 2

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
	n := trunkNodeAt(in.runSeed, 2)
	s1, err := in.buildFloor(n, 2)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := in.buildFloor(n, 2)
	if err != nil {
		t.Fatal(err)
	}
	if s1.W.Hash() != s2.W.Hash() {
		t.Error("rebuilding the same floor produced a different world")
	}
	s3, err := in.buildFloor(n, 3)
	if err != nil {
		t.Fatal(err)
	}
	if s1.W.Hash() == s3.W.Hash() {
		t.Error("adjacent floors hashed identically — seeds are not deriving")
	}
	// A different node at the same depth diverges too.
	s4, err := in.buildFloor(nodeAddr{n.Row, n.Col + 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if s1.W.Hash() == s4.W.Hash() {
		t.Error("sibling nodes hashed identically — the column is not in the salt")
	}
	if space.Dist(farthestWalkable(s1.W.Grid), s1.W.Grid.Spawn) < fm.FromInt(5) {
		t.Error("stairs suspiciously close to the spawn room")
	}
}

// TestDeathEjectGrantsGrace: a death eject arrives shielded — hits deal
// nothing until the portal-grace buff runs out, then hurt normally.
func TestDeathEjectGrantsGrace(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	killActor(in, c)
	in.handleDeaths([]*client{c})

	w := in.sim.W
	a := w.ActorByID(c.actor)
	if a == nil {
		t.Fatal("no respawned actor")
	}
	grace := w.Content.Buffs["portal_grace"]
	found := false
	for _, s := range a.Statuses {
		if s.Buff == grace {
			found = true
		}
	}
	if !found {
		t.Fatal("death eject did not grant portal_grace")
	}

	zid, err := in.sim.Spawn("zombie", a.Pos)
	if err != nil {
		t.Fatal(err)
	}
	slam := w.Content.Skills["zombie_slam"]
	hit := func() {
		w.QueueHit(core.Hit{
			Attacker: zid, Defender: a.ID, Skill: slam,
			Tags: slam.Tags.With(stats.TagHit),
		})
		combat.ResolveHits(w)
	}
	hit()
	if a.Life != a.MaxLife() {
		t.Errorf("hit during grace dealt damage: life %v / %v", a.Life, a.MaxLife())
	}

	for i := uint32(0); i <= grace.DurationTicks; i++ {
		combat.TickStatuses(w)
	}
	// A few swings: any single hit can miss on the evasion roll — grace
	// ending means damage lands within a handful, not on a specific one.
	for i := 0; i < 8 && a.Life == a.MaxLife(); i++ {
		hit()
	}
	if a.Life == a.MaxLife() {
		t.Error("hits after grace expiry dealt nothing — grace never ended")
	}
}

// TestSetPieceGuardsTheLastFloor: a node's first two floors are open; the
// third stakes the row's set-piece — a rare Bone Colossus on regular rows —
// on the stairs, two levels hot.
func TestSetPieceGuardsTheLastFloor(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	find := func(s *sim.Sim, def string) *core.Actor {
		for _, a := range s.W.Actors {
			if a.Def.ID == def {
				return a
			}
		}
		return nil
	}
	n1 := trunkNodeAt(in.runSeed, 1)
	s2, err := in.buildFloor(n1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if find(s2, guardianDef) != nil {
		t.Error("a mid-node floor spawned a set-piece; wanted none")
	}
	s3, err := in.buildFloor(n1, 3)
	if err != nil {
		t.Fatal(err)
	}
	g := find(s3, guardianDef)
	if g == nil {
		t.Fatal("row 1's last floor has no guardian")
	}
	if g.Rarity != core.RarityRare || len(g.Mods) != 2 {
		t.Errorf("guardian rarity %v with %d mods, want rare with 2", g.Rarity, len(g.Mods))
	}
	if g.Level != 5 {
		t.Errorf("guardian level = %d, want floor+2 = 5", g.Level)
	}
	if d := space.Dist(g.Pos, farthestWalkable(s3.W.Grid)); d > fm.FromInt(2) {
		t.Errorf("guardian %v from the stairs, want parked on them", d)
	}
}

// TestSetPieceCadence: every bossRows-th row ends in the Barrow King or
// the Ashen Warden (alternating), every apexRows-th in the Grave Tyrant,
// the rest in the guardian — and exactly one set-piece per node.
func TestSetPieceCadence(t *testing.T) {
	cases := []struct {
		row  int
		want string
	}{
		{1, guardianDef}, {2, guardianDef}, {3, bossDef}, {4, guardianDef},
		{5, guardianDef}, {6, bossDef2}, {9, bossDef}, {10, apexDef},
		{12, bossDef2}, {20, apexDef}, {30, apexDef},
	}
	for _, c := range cases {
		if got := setPieceFor(c.row); got != c.want {
			t.Errorf("setPieceFor(%d) = %q, want %q", c.row, got, c.want)
		}
	}

	// And the apex builds hot: floor+3 against the guardian's floor+2.
	in, _, _ := descentInstance(t, 3)
	s, err := in.buildFloor(nodeAddr{Row: 10, Col: delveEntryCol}, 3)
	if err != nil {
		t.Fatal(err)
	}
	setPieces := 0
	for _, a := range s.W.Actors {
		if isSetPiece(a.Def.ID) {
			setPieces++
			if a.Def.ID != apexDef {
				t.Errorf("row 10 staked %q, want the apex", a.Def.ID)
			}
			if a.Level != globalFloor(10, 3)+3 {
				t.Errorf("apex level = %d, want floor+3 = %d", a.Level, globalFloor(10, 3)+3)
			}
		}
	}
	if setPieces != 1 {
		t.Errorf("row 10's last floor holds %d set-pieces, want exactly 1", setPieces)
	}
}

// TestRunSaveRoundTrip: the run envelope restores a mid-run instance —
// floor, portal anchor, budget, best — around a bit-identical world;
// legacy bare-world saves still load as floor 1 of a fresh run.
func TestRunSaveRoundTrip(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	in.descend()
	in.descend() // floor 3: the entry node's last floor
	in.cleared[in.node] = true
	in.portalsLeft = 1
	in.portalNode, in.portalFin, in.portalPos = in.node, 3, in.sim.W.Grid.Spawn

	world, err := in.sim.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := in.encodeRunSave(world)
	if err != nil {
		t.Fatal(err)
	}

	in2, err := New(content.DB(), Config{Seed: 9, Load: blob, Portals: 3})
	if err != nil {
		t.Fatal(err)
	}
	if in2.floor != 3 || in2.portalsLeft != 1 || in2.best != 3 || in2.run != in.run {
		t.Errorf("restored run = floor %d portals %d best %d run %d, want 3/1/3/%d",
			in2.floor, in2.portalsLeft, in2.best, in2.run, in.run)
	}
	if in2.node != in.node || in2.fin != 3 {
		t.Errorf("restored node = %v+%d, want %v+3", in2.node, in2.fin, in.node)
	}
	if in2.portalNode != in.node || in2.portalFin != 3 || in2.portalPos != in.portalPos || !in2.portalPlaced {
		t.Errorf("restored portal anchor = %v+%d %v (placed %v)",
			in2.portalNode, in2.portalFin, in2.portalPos, in2.portalPlaced)
	}
	if !in2.visited[in.node] || !in2.cleared[in.node] {
		t.Errorf("restored chart lost the bookkeeping: visited=%v cleared=%v",
			in2.visited[in.node], in2.cleared[in.node])
	}
	if in2.stairs == (space.Vec2{}) {
		t.Error("restored instance has no stairs")
	}

	// Legacy path: a bare world file resumes as a fresh run.
	in3, err := New(content.DB(), Config{Seed: 9, Load: world, Portals: 3})
	if err != nil {
		t.Fatal(err)
	}
	if in3.run != 1 || in3.floor != 1 || in3.portalsLeft != 3 {
		t.Errorf("legacy load = run %d floor %d portals %d, want 1/1/3", in3.run, in3.floor, in3.portalsLeft)
	}
	// Both paths load the same world bytes (player reclamation included),
	// so the worlds themselves must match bit for bit.
	if in2.sim.W.Hash() != in3.sim.W.Hash() {
		t.Error("envelope and legacy loads produced different worlds")
	}
}

// TestRunStartsInHideout: the default run begins at home — floor 0, safe,
// no portal use spent going down, the anchor landing on floor 1's spawn.
func TestRunStartsInHideout(t *testing.T) {
	in, c, tr := descentInstanceAt(t, 3, 0)

	if in.floor != 0 {
		t.Fatalf("floor = %d, want 0 (the hideout)", in.floor)
	}
	if in.portalPlaced {
		t.Error("fresh run already has a placed portal anchor")
	}
	if in.best != 0 {
		t.Errorf("best = %d before any descent, want 0", in.best)
	}
	if n := len(in.sim.W.Actors); n != 1 { // just the joined player
		t.Errorf("hideout holds %d actors, want 1 (it is safe)", n)
	}

	// The player joins standing on the home portal; entering it begins the
	// run on floor 1 — free, arriving at the floor's spawn.
	in.runTick(nil, runWants{portals: []*client{c}})
	if in.floor != 1 || in.best != 1 {
		t.Fatalf("floor/best = %d/%d after the home portal, want 1/1", in.floor, in.best)
	}
	if in.portalsLeft != 3 {
		t.Errorf("portalsLeft = %d, want 3 (leaving home is free)", in.portalsLeft)
	}
	if !in.portalPlaced || globalFloor(in.portalNode.Row, in.portalFin) != 1 ||
		in.portalPos != in.sim.W.Grid.Spawn {
		t.Errorf("portal anchor = %v+%d at %v (placed %v), want global floor 1 at its spawn",
			in.portalNode, in.portalFin, in.portalPos, in.portalPlaced)
	}
	w := tr.lastWelcome(t)
	if w.Run == nil || w.Run.Floor != 1 || w.Stairs == nil {
		t.Errorf("floor-1 welcome = run %+v stairs %v, want floor 1 with stairs", w.Run, w.Stairs)
	}

	// And back through: a hideout trip still costs one.
	in.runTick(nil, runWants{portals: []*client{c}})
	if in.floor != 0 || in.portalsLeft != 2 {
		t.Errorf("floor/portals = %d/%d after retreating home, want 0/2", in.floor, in.portalsLeft)
	}
}

// TestRunOverReturnsHome: with no portal budget, death ends the run and the
// next one starts back in the hideout with a fresh, unplaced anchor.
func TestRunOverReturnsHome(t *testing.T) {
	in, c, _ := descentInstanceAt(t, 0, 0)
	in.runTick(nil, runWants{portals: []*client{c}}) // step through the home portal
	if in.floor != 1 {
		t.Fatalf("floor = %d, want 1", in.floor)
	}

	killActor(in, c)
	in.handleDeaths([]*client{c})

	if in.run != 2 {
		t.Errorf("run = %d, want 2 (a fresh run began)", in.run)
	}
	if in.floor != 0 {
		t.Errorf("floor = %d, want 0 (a new run starts at home)", in.floor)
	}
	if in.portalPlaced {
		t.Error("new run's portal anchor should wait for the first trip down")
	}
	if in.best != 1 {
		t.Errorf("best = %d, want the old depth 1 kept", in.best)
	}

	// The new run's portal works: down to floor 1 of run 2.
	in.runTick(nil, runWants{portals: []*client{c}})
	if in.floor != 1 {
		t.Errorf("floor = %d after the new run's portal, want 1", in.floor)
	}
}
