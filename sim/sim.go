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
	"github.com/JakeMalmrose/draupforge/sim/skills"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

type Sim struct {
	W *core.World
}

func New(db *core.ContentDB, seed uint64) *Sim {
	return &Sim{W: core.NewWorld(db, seed)}
}

// Spawn places an actor by content ID and returns its entity ID.
func (s *Sim) Spawn(defID string, pos space.Vec2) (core.EntityID, error) {
	def := s.W.Content.Actors[defID]
	if def == nil {
		return 0, fmt.Errorf("sim: unknown actor def %q", defID)
	}
	return s.W.SpawnActor(def, pos).ID, nil
}

// Step advances exactly one tick. The phase order below is the determinism
// contract — fixed, every tick, no exceptions. Callers must pass commands in
// a deterministic order (the server's job; tests and scripts are ordered by
// construction).
func (s *Sim) Step(cmds []core.Command) {
	w := s.W
	w.BeginTick()

	combat.Upkeep(w)              // regen, so this tick's casts see fresh mana
	applyCommands(w, cmds)        // player/network intent
	applyCommands(w, ai.Decide(w)) // monster intent, same validation gate
	skills.AdvanceActions(w)      // movement + windup/recovery; effects queue hits
	skills.UpdateProjectiles(w)   // flight + collision; impacts queue hits
	combat.ResolveHits(w)         // the damage pipeline, in queue order
	combat.TickDoTs(w)            // ignites and friends
	items.RollLoot(w)             // reacts to this tick's death events

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
			a.Action = core.Action{Kind: core.ActionMove, MoveTarget: c.Point}

		case core.CmdStop:
			if a.Action.Kind == core.ActionMove {
				a.Action = core.Action{}
			}

		case core.CmdEquip:
			if a.Action.Kind == core.ActionSkill {
				continue // no swapping rings mid-swing
			}
			items.Equip(w, a, c.TargetID)

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
