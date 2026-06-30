package server

import (
	"fmt"
	"log"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// The descent (ROADMAP.md phase 1, DESIGN.md §14): floors escalate via
// stairs, death costs XP and ejects you to a safe hideout instead of back
// into whatever killed you, and a run grants a limited number of
// hideout-rescues before the next death ends it and starts a fresh one —
// "push one more floor or bank it" is the whole game in one decision. The
// sim never knows any of this; it only offers the geometry (Grid.Stairs)
// and the validated commands (move, equip, ...) this package drives. All of
// the numbers below were explicitly left open by design — tune freely.
const (
	// descendRange is how close an actor must stand to the stairs to descend
	// (in the dungeon) or to leave the hideout.
	descendRange = fm.Fixed(2000) // 2 units, matches items.PickupRange's feel
	// portalChargesStart is how many death-rescues a fresh run grants before
	// the next death ends it (Jake, 2026-06-30: "fresh run after X deaths").
	portalChargesStart = 3
)

// hideoutMapSpec is a small, calm, always-monster-free room — fixed
// regardless of the dungeon's own Map template, since it exists purely as a
// safe landing spot, not content to clear.
var hideoutMapSpec = space.MapSpec{Width: 16, Height: 16, Rooms: 1}

// runState is host-layer bookkeeping for one descent: which floor (or the
// hideout), the score, and the portal/death economy. It never touches the
// sim — transfers happen entirely at this layer (DESIGN.md §14).
type runState struct {
	seed      uint64 // advances (never reused) on a hard reset
	inHideout bool
	// started is false until the run's dungeon has been entered for the
	// first time — distinguishes "nothing to resume yet" (descending from
	// the hideout begins floor 1) from "paused mid-run" (it resumes at
	// portalFloor/portalPos instead).
	started     bool
	floor       int
	maxFloor    int
	portalFloor int
	portalPos   space.Vec2
	portalUses  int
}

func newRunState(seed uint64) *runState {
	return &runState{seed: seed, inHideout: true, floor: 1, maxFloor: 1, portalFloor: 1, portalUses: portalChargesStart}
}

// floorSeed derives floor N's world seed from the run seed, so whole
// descents (and revisits via the portal) are replayable floor by floor and
// portal-eject coordinates — captured on a floor the player already
// stood on — land on the same legal ground when that floor regenerates.
func floorSeed(runSeed uint64, floor int) uint64 {
	st := runSeed + uint64(floor)
	return core.SplitMix64(&st)
}

// newFloorSim is sim.New plus one BeginTick to move off Tick 0 before
// anything is ever sent to a client. The binary wire's keyframe sentinel is
// baseline-tick 0 (protocol/binary.go), and the client echoes a view's own
// tick back as its ack; a fresh World's first view would carry Tick 0 too,
// so the server would read that ack as the unrelated "client lost its
// state" signal instead of normal baseline bookkeeping. BeginTick only
// advances the counter and clears the (still-empty) event buffer — safe to
// call before any actor exists.
func newFloorSim(db *core.ContentDB, seed uint64) *sim.Sim {
	s := sim.New(db, seed)
	s.W.BeginTick()
	return s
}

// buildFloor builds one floor's World: a fresh map off cfg.Map's footprint,
// cfg.Spawns at their literal coordinates, and cfg.Scatter scaled to the
// floor's depth — monster level == floor index, the simplest reading of
// "packs scale with floor depth" (ROADMAP.md phase 1) and exactly the
// ActorDef.Level/PerLevel machinery already built for it.
func buildFloor(db *core.ContentDB, cfg Config, seed uint64, floor int) (*sim.Sim, error) {
	s := newFloorSim(db, seed)
	s.GenerateMap(space.MapSpec{Width: cfg.Map.Width, Height: cfg.Map.Height, Rooms: cfg.Map.Rooms})
	for _, sp := range cfg.Spawns {
		if _, err := s.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y))); err != nil {
			return nil, fmt.Errorf("server: floor %d scenario spawn: %w", floor, err)
		}
	}
	for _, sc := range cfg.Scatter {
		if err := s.ScatterSpawnLeveled(sc.Def, sc.Count, floor); err != nil {
			return nil, fmt.Errorf("server: floor %d scatter spawn: %w", floor, err)
		}
	}
	return s, nil
}

// buildHideout builds the safe zone a run starts in and every death returns
// to: hideoutMapSpec's room, no spawns or scatter at all.
func buildHideout(db *core.ContentDB, seed uint64) *sim.Sim {
	s := newFloorSim(db, seed)
	s.GenerateMap(hideoutMapSpec)
	return s
}

// character is a portable snapshot of everything about an actor that
// survives a zone transfer (DESIGN.md §14): identity, level, XP, bag,
// equipment. Position, action, buffs, DoTs deliberately don't travel — the
// stat sheet rebuilds from def + level + equipment at injection.
type character struct {
	defID     string
	level     int
	xp        int64
	equipment [core.EquipSlotCount]*core.Item
	inventory []core.Item
}

func extractCharacter(a *core.Actor) character {
	c := character{defID: a.Def.ID, level: a.Level, xp: a.XP}
	for slot, item := range a.Equipment {
		if item != nil {
			cp := *item
			c.equipment[slot] = &cp
		}
	}
	c.inventory = append([]core.Item(nil), a.Inventory...)
	return c
}

// injectCharacter spawns a fresh actor for c in w, re-minting every item's
// ID (DESIGN.md §14: item IDs are world-local and double as sheet mod
// sources, so they'd collide in a fresh World) and granting equipped items'
// modifiers under the new IDs.
func injectCharacter(w *core.World, c character, pos space.Vec2) (*core.Actor, error) {
	def := w.Content.Actors[c.defID]
	if def == nil {
		return nil, fmt.Errorf("server: character references unknown actor def %q", c.defID)
	}
	if w.Grid != nil {
		if p, ok := w.Grid.NearestWalkable(pos); ok {
			pos = p
		}
	}
	a := w.SpawnActor(def, pos)
	a.SetLevel(c.level)
	a.XP = c.xp
	for slot, item := range c.equipment {
		if item == nil {
			continue
		}
		minted := *item
		minted.ID = w.AllocID()
		a.Equipment[slot] = &minted
		items.GrantItemMods(a, &minted)
	}
	for _, item := range c.inventory {
		minted := item
		minted.ID = w.AllocID()
		a.Inventory = append(a.Inventory, minted)
	}
	// Full heal at zone entry — the same "ding heal" precedent as a level-up
	// (sim/progress), and simplest: zone-local damage never traveled anyway.
	a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
	return a, nil
}

// deathXPPenalty halves progress into the current level. Since Actor.XP is
// progress-into-level rather than a cumulative total (sim/progress), halving
// a non-negative value can never cross the current level's floor — exactly
// Jake's 2026-06-12 rule ("never below the current level's floor") without
// needing to know the curve.
func deathXPPenalty(xp int64) int64 {
	return xp / 2
}

// transitionToFloor swaps the instance onto a freshly built floor: every
// connected client's character is extracted from the outgoing world (or,
// for an actor that died this very tick and is already compacted away, from
// its last-known-alive snapshot — client.lastChar) and injected into the
// new one, then the floor and score update. pos nil means "the new floor's
// entrance" (a descend); non-nil places everyone at that exact spot (a
// portal eject). The caller is responsible for re-welcoming every client
// afterward (DESIGN.md §14: "zone transfer = re-welcome on the same socket"
// — the same machinery either way, so it isn't duplicated per caller).
func (in *Instance) transitionToFloor(floor int, pos *space.Vec2) error {
	s, err := buildFloor(in.sim.W.Content, in.cfg, floorSeed(in.run.seed, floor), floor)
	if err != nil {
		return err
	}
	arrival := s.W.Grid.Spawn
	if pos != nil {
		arrival = *pos
	}
	for _, c := range in.clients {
		if c.actor == 0 {
			continue
		}
		ch := c.lastChar
		if a := in.sim.W.ActorByID(c.actor); a != nil {
			extracted := extractCharacter(a)
			ch = &extracted
		}
		if ch == nil {
			continue // never finished spawning (race with a join); nothing to carry
		}
		a, err := injectCharacter(s.W, *ch, arrival)
		if err != nil {
			return err
		}
		c.actor = a.ID
		c.resetWire()
	}
	in.sim = s
	in.mapSnap = in.sim.EncodeMap()
	in.run.inHideout = false
	in.run.floor = floor
	if floor > in.run.maxFloor {
		in.run.maxFloor = floor
	}
	return nil
}

// transitionToHideout swaps the instance into a fresh hideout — every death
// lands here rather than back into whatever just killed the party. Floor,
// maxFloor (the score), and the portal location are untouched: the run is
// merely paused, not over, and resumes from the hideout via processRun's
// descend handling.
func (in *Instance) transitionToHideout() error {
	s := buildHideout(in.sim.W.Content, floorSeed(in.run.seed, 0))
	for _, c := range in.clients {
		if c.actor == 0 {
			continue
		}
		ch := c.lastChar
		if a := in.sim.W.ActorByID(c.actor); a != nil {
			extracted := extractCharacter(a)
			ch = &extracted
		}
		if ch == nil {
			continue
		}
		a, err := injectCharacter(s.W, *ch, s.W.Grid.Spawn)
		if err != nil {
			return err
		}
		c.actor = a.ID
		c.resetWire()
	}
	in.sim = s
	in.mapSnap = in.sim.EncodeMap()
	in.run.inHideout = true
	return nil
}

// resetRun ends the current run (portal exhausted, then one death too many)
// and starts a fresh one: a new seed advanced from the old one — so the
// sequence of runs stays a deterministic function of the instance's
// original seed — full portal charges, and every connected client reduced
// to a brand-new level-1 spawn, landing back in the hideout to start over.
// That's the stakes "depth is the score" promises (ROADMAP.md phase 1): a
// run that ends costs everything carried into it, not just position.
func (in *Instance) resetRun() {
	endedAt := in.run.maxFloor
	in.run = newRunState(core.SplitMix64(&in.run.seed))
	s := buildHideout(in.sim.W.Content, floorSeed(in.run.seed, 0))
	for _, c := range in.clients {
		if c.actor == 0 {
			continue
		}
		id, err := s.Spawn(in.cfg.PlayerDef, s.W.Grid.Spawn)
		if err != nil {
			log.Printf("server: resetting run: respawning client: %v", err)
			continue
		}
		c.actor = id
		c.resetWire()
	}
	in.sim = s
	in.mapSnap = in.sim.EncodeMap()
	in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: "run_over", Amount: int64(endedAt)})
}

// processRun reacts to this tick's death events and any descend/portal
// requests queued this tick, in priority order: a death always wins over a
// pending descend (you don't get to walk through the stairs you just died
// next to), and a portal-plant only matters if nobody died. Returns true if
// the instance's World was swapped (the caller must re-welcome everyone).
func (in *Instance) processRun(descendReqs, portalReqs []*client) bool {
	run := in.run
	w := in.sim.W

	var died []*client
	for _, ev := range w.LastEvents {
		if ev.Kind != core.EvDeath {
			continue
		}
		for _, c := range in.clients {
			if c.actor == ev.Actor {
				died = append(died, c)
			}
		}
	}

	if len(died) > 0 {
		if run.portalUses <= 0 {
			in.resetRun()
			return true
		}
		run.portalUses--
		for _, c := range died {
			if c.lastChar != nil {
				c.lastChar.xp = deathXPPenalty(c.lastChar.xp)
			}
		}
		if err := in.transitionToHideout(); err != nil {
			log.Printf("server: hideout eject: %v", err)
			return false
		}
		in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: "hideout_eject"})
		return true
	}

	if !run.inHideout {
		for _, c := range portalReqs {
			a := w.ActorByID(c.actor)
			if a == nil || a.Dead || a.Action.Kind == core.ActionSkill {
				continue
			}
			run.portalFloor = run.floor
			run.portalPos = a.Pos
			in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: "portal_planted", Actor: uint64(c.actor)})
			break // one plant a tick is plenty; first requester wins
		}
	}

	for _, c := range descendReqs {
		a := w.ActorByID(c.actor)
		if a == nil || a.Dead {
			continue
		}
		if space.Dist(a.Pos, w.Grid.Stairs) > descendRange {
			continue
		}
		if run.inHideout {
			target, pos := 1, (*space.Vec2)(nil)
			if run.started {
				target, pos = run.portalFloor, &run.portalPos
			}
			if err := in.transitionToFloor(target, pos); err != nil {
				log.Printf("server: leaving the hideout to floor %d: %v", target, err)
				return false
			}
			if !run.started {
				run.started = true
				run.portalFloor = run.floor
				run.portalPos = in.sim.W.Grid.Spawn
			}
			in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: "floor_change", Amount: int64(run.floor)})
			return true
		}
		next := run.floor + 1
		if err := in.transitionToFloor(next, nil); err != nil {
			log.Printf("server: descend to floor %d: %v", next, err)
			return false
		}
		in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: "floor_change", Amount: int64(next)})
		return true
	}
	return false
}
