// The descent — the host-layer run loop (DESIGN.md §14: transfers happen at
// the host layer between ticks; the sim never participates). A run owns a
// seed; each floor is a whole fresh World derived from (run seed, floor
// index). Floor swaps extract every client to character state, build the
// new world, inject, and re-welcome — the same full-reset machinery a
// reconnect would use.
//
// Run rules: the portal is the death anchor — it starts on floor 1 and can
// be re-planted wherever you stand. Death costs XP (never below the current
// level's floor) and ejects everyone to the portal, consuming one portal
// use; a death with none left ends the run (depth was the score; a new run
// starts at floor 1 on a fresh seed — the character survives). Entering the
// planted portal travels to the hideout (floor 0, a small safe world, one
// use); stepping back through is free. Numbers (penalty, pack scaling,
// portal budget) are open for tuning.
package server

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

var (
	// descendRange / portalRange: how close an actor must stand to use the
	// stairs or the portal, center to center (stairs sit on a tile center).
	descendRange = fm.FromInt(2)
	portalRange  = fm.FromInt(2)
)

// deathXPPenaltyDiv: death costs 1/5 of the current level's XP requirement,
// clamped so a level's progress never goes negative (no de-leveling).
const deathXPPenaltyDiv = 5

// deriveSeed mixes a salt into a base seed (splitmix finalizer), so run
// seeds derive from the config seed and floor seeds derive from the run
// seed — whole descents are replayable floor by floor.
func deriveSeed(base, salt uint64) uint64 {
	st := base ^ (salt * 0x9E3779B97F4A7C15)
	return core.SplitMix64(&st)
}

// farthestWalkable is where the stairs stand: the walkable tile farthest
// from the spawn room, so a floor is crossed before it is left. Walkable
// tiles are reachable by construction (mapgen prunes the rest), and the
// scan order is row-major — deterministic for a given grid.
func farthestWalkable(g *space.Grid) space.Vec2 {
	best, bd := g.Spawn, fm.Fixed(-1)
	for _, p := range g.WalkableCenters() {
		if d := space.Dist(p, g.Spawn); d > bd {
			best, bd = p, d
		}
	}
	return best
}

// buildFloor constructs floor N of the current run: terrain from the floor
// seed, the scenario's fixed spawns, and scatter packs leveled and
// thickened by depth.
func (in *Instance) buildFloor(floor int) (*sim.Sim, error) {
	s := sim.New(in.db, deriveSeed(in.runSeed, uint64(floor)))
	s.GenerateMap(space.MapSpec{
		Width: in.cfg.Map.Width, Height: in.cfg.Map.Height, Rooms: in.cfg.Map.Rooms,
	})
	for _, sp := range in.cfg.Spawns {
		if _, err := s.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y))); err != nil {
			return nil, fmt.Errorf("server: floor %d spawn: %w", floor, err)
		}
	}
	for _, sc := range in.cfg.Scatter {
		if err := s.ScatterSpawnLeveled(sc.Def, sc.Count+floor-1, floor); err != nil {
			return nil, fmt.Errorf("server: floor %d scatter: %w", floor, err)
		}
	}
	return s, nil
}

// buildHideout constructs the hideout: one small safe room, no monsters.
// Seeded off the config seed alone so it is the same home every run.
func (in *Instance) buildHideout() *sim.Sim {
	s := sim.New(in.db, deriveSeed(in.cfg.Seed, 0))
	s.GenerateMap(space.MapSpec{Width: 16, Height: 12, Rooms: 1})
	return s
}

// beginRun initializes run bookkeeping over the current world, which must
// be floor 1 with terrain installed.
func (in *Instance) beginRun() {
	g := in.sim.W.Grid
	in.floor = 1
	if in.best < 1 {
		in.best = 1
	}
	in.stairs = farthestWalkable(g)
	in.portalFloor, in.portalPos = 1, g.Spawn
	in.portalsLeft = in.cfg.Portals
}

// runTick drives the descent between steps: deaths eject through the portal
// (or end the run), stairs and portal travel swap the world, plants move
// the portal anchor. At most one world swap happens per tick; whichever
// fires first wins and the rest of this tick's requests are dropped (their
// validation context is gone with the old world).
func (in *Instance) runTick(fresh []protocol.EventSnap, descends, portals, plants []*client) {
	if in.run == 0 {
		return
	}
	var dead []*client
	for _, ev := range fresh {
		if ev.Kind != "death" {
			continue
		}
		for _, c := range in.clients {
			if uint64(c.actor) == ev.Actor {
				dead = append(dead, c)
			}
		}
	}
	swapped := false
	if len(dead) > 0 {
		in.handleDeaths(dead)
		swapped = true
	}
	if !swapped && in.floor > 0 {
		for _, c := range descends {
			a := in.sim.W.ActorByID(c.actor)
			if a == nil || a.Dead || space.Dist(a.Pos, in.stairs) > descendRange {
				continue
			}
			in.descend()
			swapped = true
			break
		}
	}
	if !swapped {
		for _, c := range portals {
			if in.portalTravel(c) {
				swapped = true
				break
			}
		}
	}
	if !swapped && in.floor > 0 {
		for _, c := range plants {
			a := in.sim.W.ActorByID(c.actor)
			if a == nil || a.Dead {
				continue
			}
			in.portalFloor, in.portalPos = in.floor, a.Pos
			in.syntheticEvent("portal", int64(in.floor)*1000, "planted")
			in.broadcastRun()
		}
	}
	// Keep the freshest character copy per client: death compacts the actor
	// away inside Step, so eject/respawn works from this (at most one tick
	// stale — the death tick itself).
	for _, c := range in.clients {
		if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
			c.lastChar, c.hasChar = core.ExtractCharacter(a), true
		}
	}
}

// handleDeaths applies the run's death rules for client actors that died
// this tick: XP penalty, then eject everyone to the portal — or, with no
// portal uses left, end the run and start the next one.
func (in *Instance) handleDeaths(dead []*client) {
	for _, c := range dead {
		if !c.hasChar {
			continue
		}
		pen := progress.XPToNext(c.lastChar.Level) / deathXPPenaltyDiv
		if pen > c.lastChar.XP {
			pen = c.lastChar.XP
		}
		c.lastChar.XP -= pen
		// The zone projection died; the character respawns refilled.
		c.lastChar.Life, c.lastChar.Mana, c.lastChar.ES = 0, 0, 0
	}
	if in.portalsLeft > 0 {
		in.portalsLeft--
		s, err := in.buildFloor(in.portalFloor)
		if err != nil {
			log.Printf("server: death eject: %v", err)
			return
		}
		in.swapWorld(s, in.portalFloor, in.portalPos)
		in.syntheticEvent("death_eject", int64(in.portalsLeft)*1000, "")
		return
	}
	depth := in.floor
	in.run++
	in.runSeed = deriveSeed(in.cfg.Seed, uint64(in.run))
	s, err := in.buildFloor(1)
	if err != nil {
		log.Printf("server: new run: %v", err)
		return
	}
	in.portalFloor, in.portalPos = 1, s.W.Grid.Spawn
	in.portalsLeft = in.cfg.Portals
	in.swapWorld(s, 1, s.W.Grid.Spawn)
	in.syntheticEvent("run_over", int64(depth)*1000, "")
}

// descend swaps the instance one floor deeper, entering at the new floor's
// spawn room.
func (in *Instance) descend() {
	s, err := in.buildFloor(in.floor + 1)
	if err != nil {
		log.Printf("server: descend: %v", err)
		return
	}
	in.swapWorld(s, in.floor+1, s.W.Grid.Spawn)
	in.syntheticEvent("descend", int64(in.floor)*1000, "")
}

// portalTravel handles one client's enter_portal request. From a dungeon
// floor, standing at the planted portal travels to the hideout and consumes
// a portal use; from the hideout, standing at its portal returns to the
// anchor floor for free. Reports whether a world swap happened.
func (in *Instance) portalTravel(c *client) bool {
	a := in.sim.W.ActorByID(c.actor)
	if a == nil || a.Dead {
		return false
	}
	if in.floor == 0 {
		if space.Dist(a.Pos, in.sim.W.Grid.Spawn) > portalRange {
			return false
		}
		s, err := in.buildFloor(in.portalFloor)
		if err != nil {
			log.Printf("server: portal return: %v", err)
			return false
		}
		in.swapWorld(s, in.portalFloor, in.portalPos)
		in.syntheticEvent("portal", int64(in.portalFloor)*1000, "return")
		return true
	}
	if in.floor != in.portalFloor || space.Dist(a.Pos, in.portalPos) > portalRange {
		return false
	}
	if in.portalsLeft == 0 {
		in.syntheticEvent("portal", 0, "exhausted")
		return false
	}
	in.portalsLeft--
	s := in.buildHideout()
	in.swapWorld(s, 0, s.W.Grid.Spawn)
	in.syntheticEvent("portal", int64(in.portalsLeft)*1000, "hideout")
	return true
}

// swapWorld is the transfer itself: reduce every client to character state,
// install the new world (floor 0 = hideout), inject everyone at pos (fanned
// out; injection clamps to walkable ground), and re-welcome each client —
// which resets their delta encoder and bumps their welcome generation.
func (in *Instance) swapWorld(s *sim.Sim, floor int, at space.Vec2) {
	for _, c := range in.clients {
		if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
			c.lastChar, c.hasChar = core.ExtractCharacter(a), true
		}
	}
	in.sim = s
	in.floor = floor
	in.mapSnap = s.EncodeMap()
	if floor > in.best {
		in.best = floor
	}
	if floor > 0 {
		in.stairs = farthestWalkable(s.W.Grid)
	}
	// Old-world events still buffered reference entities that no longer
	// exist; the synthetic run events narrate the transition instead.
	in.eventBuf = nil
	ids := make([]core.EntityID, len(in.clients))
	for i, c := range in.clients {
		pos := at.Add(space.V(fm.FromInt(int64(i%4)), 0))
		if c.hasChar {
			if a, err := core.InjectCharacter(s.W, c.lastChar, pos); err == nil {
				ids[i] = a.ID
			} else {
				log.Printf("server: inject character: %v", err)
			}
		}
		if ids[i] == 0 {
			ids[i], _ = s.Spawn(in.cfg.PlayerDef, pos) // no character yet: fresh spawn
		}
	}
	// One atomic cutover: actor IDs, welcome generations, ack state, and the
	// pending queue flip together. Commands readLoop tagged with old-world
	// IDs are either in the queue we drop here or arrive after and get the
	// new IDs — nothing ever drives whichever actor wears an old ID now.
	in.mu.Lock()
	for i, c := range in.clients {
		c.actor = ids[i]
		c.gen++
		c.ack, c.ackDirty = 0, false
	}
	in.pending = nil
	in.mu.Unlock()
	for _, c := range in.clients {
		c.baseline, c.sent, c.sentTicks = nil, nil, nil
		if !c.send(in.welcomeFrame(c), false) {
			c.tr.Close()
		}
	}
}

// runSnap is the wire form of the run state. Floor 0 is the hideout; the
// portal position is included when the portal stands on the current world.
func (in *Instance) runSnap() *protocol.RunSnap {
	rs := &protocol.RunSnap{Floor: in.floor, Portals: in.portalsLeft, Run: in.run, Best: in.best}
	var pp *space.Vec2
	if in.floor == 0 {
		p := in.sim.W.Grid.Spawn
		pp = &p
	} else if in.floor == in.portalFloor {
		pp = &in.portalPos
	}
	if pp != nil {
		rs.Portal = &protocol.Vec{X: pp.X.Milli(), Y: pp.Y.Milli()}
	}
	return rs
}

// broadcastRun announces a run-state change that comes without a new world
// (portal planted). Swaps don't need it — their welcomes carry the run.
func (in *Instance) broadcastRun() {
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "run", Run: in.runSnap()})
	for _, c := range in.clients {
		if !c.send(frame, false) {
			c.tr.Close()
		}
	}
}

// syntheticEvent queues a host-layer wire event. Participant-less, so
// interest filtering treats it as global — run flow (descents, portal
// travel, deaths) is narrated this way, since the sim doesn't know runs
// exist.
func (in *Instance) syntheticEvent(kind string, amount int64, note string) {
	in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: kind, Amount: amount, Note: note})
}
