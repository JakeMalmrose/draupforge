// Package sim wires the deterministic tick: it owns the phase order and the
// command-validation gate. Data types live in sim/core; system logic lives in
// the leaf packages; nothing below this package knows the phase order.
package sim

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/sim/ai"
	"github.com/JakeMalmrose/draupforge/sim/combat"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/skills"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

type Sim struct {
	W *core.World
}

func New(db *core.ContentDB, seed uint64) *Sim {
	return &Sim{W: core.NewWorld(db, seed)}
}

// Load resumes a sim from a World.Save file. The restored world continues
// bit-exactly: same hashes, same RNG streams, same in-flight actions.
func Load(db *core.ContentDB, data []byte) (*Sim, error) {
	w, err := core.LoadWorld(db, data)
	if err != nil {
		return nil, err
	}
	return &Sim{W: w}, nil
}

// Spawn places an actor by content ID and returns its entity ID. On grid
// worlds the position is clamped to the nearest walkable spot, so scenario
// coordinates authored against a different layout still land somewhere legal.
func (s *Sim) Spawn(defID string, pos space.Vec2) (core.EntityID, error) {
	def := s.W.Content.Actors[defID]
	if def == nil {
		return 0, fmt.Errorf("sim: unknown actor def %q", defID)
	}
	if s.W.Grid != nil {
		p, ok := s.W.Grid.NearestWalkable(pos)
		if !ok {
			return 0, fmt.Errorf("sim: no walkable tile to spawn %q", defID)
		}
		pos = p
	}
	return s.W.SpawnActor(def, pos).ID, nil
}

// GenerateMap rolls terrain from the world's map RNG stream and installs
// it. Call before any spawns; terrain is immutable afterwards.
func (s *Sim) GenerateMap(spec space.MapSpec) {
	s.W.Grid = space.GenerateRooms(spec, s.W.RNGMap)
}

// scatterMinSpawnDist keeps scattered monsters out of the players' entry
// room — far enough to not be hit the instant you load in.
var scatterMinSpawnDist = fm.FromInt(10)

// ScatterSpawn places count actors on random walkable tiles (map RNG
// stream), preferring spots away from the player spawn point.
func (s *Sim) ScatterSpawn(defID string, count int) error {
	return s.scatterSpawn(defID, count, 0)
}

// ScatterSpawnLeveled is ScatterSpawn with every spawned actor's level
// overridden (e.g. floor-depth scaling) instead of the def's authored level.
func (s *Sim) ScatterSpawnLeveled(defID string, count int, level int) error {
	return s.scatterSpawn(defID, count, level)
}

// level 0 means "keep the def's authored level" — SetLevel already treats
// <1 as a no-op clamp, but skipping the call entirely keeps the unscaled
// path byte-identical to before this existed.
func (s *Sim) scatterSpawn(defID string, count int, level int) error {
	g := s.W.Grid
	if g == nil {
		return fmt.Errorf("sim: scatter spawn needs a generated map")
	}
	tiles := g.WalkableCenters()
	if len(tiles) == 0 {
		return fmt.Errorf("sim: map has no walkable tiles")
	}
	for i := 0; i < count; i++ {
		pos := tiles[s.W.RNGMap.Uint64n(uint64(len(tiles)))]
		for try := 0; try < 20 && space.Dist(pos, g.Spawn) < scatterMinSpawnDist; try++ {
			pos = tiles[s.W.RNGMap.Uint64n(uint64(len(tiles)))]
		}
		id, err := s.Spawn(defID, pos)
		if err != nil {
			return err
		}
		if level >= 1 {
			if a := s.W.ActorByID(id); a != nil {
				a.SetLevel(level)
				a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
			}
		}
	}
	return nil
}

// Step advances exactly one tick. The phase order below is the determinism
// contract — fixed, every tick, no exceptions. Callers must pass commands in
// a deterministic order (the server's job; tests and scripts are ordered by
// construction).
func (s *Sim) Step(cmds []core.Command) {
	w := s.W
	w.BeginTick()

	combat.Upkeep(w)               // regen, so this tick's casts see fresh mana
	applyCommands(w, cmds)         // player/network intent
	applyCommands(w, ai.Decide(w)) // monster intent, same validation gate
	skills.AdvanceActions(w)       // movement + windup/recovery; effects queue hits/buffs
	skills.UpdateProjectiles(w)    // flight + collision; impacts queue hits
	combat.ResolveBuffs(w)         // buff applications land before hits resolve
	combat.ResolveHits(w)          // the damage pipeline, in queue order
	combat.TickDoTs(w)             // ignites and friends
	combat.TickStatuses(w)         // chill/shock timers; modifiers off at expiry
	items.RollLoot(w)              // reacts to this tick's death events
	progress.AwardXP(w)            // ditto: XP and level-ups off the same deaths

	w.EndTick()
}

// applyCommands is the validation gate: every command is checked against the
// actor's actual state. Invalid commands are dropped silently — the sim never
// trusts the sender.
func applyCommands(w *core.World, cmds []core.Command) {
	for _, c := range cmds {
		a := w.ActorByID(c.Actor)
		if a == nil || a.Dead {
			continue
		}
		switch c.Kind {
		case core.CmdMove:
			if a.Action.Kind == core.ActionSkill {
				continue // committed: no canceling windup/recovery in v1
			}
			if w.Grid == nil {
				a.Action = core.Action{Kind: core.ActionMove, MoveTarget: c.Point}
				continue
			}
			// Repath throttle: AI re-issues its chase target every tick; as
			// long as the request stays within half a tile of the current
			// goal, the existing path is still the answer.
			if a.Action.Kind == core.ActionMove && len(a.Action.Path) > 0 &&
				space.Dist(a.Action.MoveTarget, c.Point) <= w.Grid.Tile/2 {
				continue
			}
			path := w.Grid.FindPath(a.Pos, c.Point)
			if len(path) == 0 {
				continue // nowhere legal to go
			}
			a.Action = core.Action{Kind: core.ActionMove, MoveTarget: c.Point, Path: path}

		case core.CmdStop:
			if a.Action.Kind == core.ActionMove {
				a.Action = core.Action{}
			}

		case core.CmdEquip, core.CmdPickup, core.CmdUnequip, core.CmdDropItem:
			if a.Action.Kind == core.ActionSkill {
				continue // no rummaging through the bag mid-swing
			}
			switch c.Kind {
			case core.CmdEquip:
				slot := core.EquipAuto
				if c.HasSlot {
					slot = c.Slot
				}
				items.Equip(w, a, c.TargetID, slot)
			case core.CmdPickup:
				items.Pickup(w, a, c.TargetID)
			case core.CmdUnequip:
				items.Unequip(w, a, c.TargetID)
			case core.CmdDropItem:
				items.DropItem(w, a, c.TargetID)
			}

		case core.CmdUseSkill:
			if a.Action.Kind == core.ActionSkill {
				continue
			}
			sk := w.Content.Skills[c.Skill]
			if sk == nil || !actorKnows(a, c.Skill) {
				continue
			}
			if a.Mana < sk.ManaCost {
				continue
			}
			speed := a.Sheet.Eval(sk.SpeedStat, sk.Tags)
			a.Mana -= sk.ManaCost
			a.Action = core.Action{
				Kind:          core.ActionSkill,
				Skill:         sk,
				AimPoint:      c.Point,
				TargetID:      c.TargetID,
				Phase:         core.PhaseWindup,
				TicksLeft:     scaleTicks(sk.WindupTicks, speed),
				RecoveryTicks: scaleTicks(sk.RecoveryTicks, speed),
			}
		}
	}
}

func actorKnows(a *core.Actor, skillID string) bool {
	for _, id := range a.Def.Skills {
		if id == skillID {
			return true
		}
	}
	return false
}

// scaleTicks divides a base tick count by a speed multiplier. Anything an
// actor does takes at least one tick; zero-length phases stay zero.
func scaleTicks(base uint32, speed fm.Fixed) uint32 {
	if base == 0 {
		return 0
	}
	if speed < fm.FromMilli(100) {
		speed = fm.FromMilli(100) // floor at 10% speed: no infinite windups
	}
	t := fm.Div(fm.FromInt(int64(base)), speed).Int()
	if t < 1 {
		t = 1
	}
	return uint32(t)
}
