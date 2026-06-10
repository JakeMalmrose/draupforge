// Package skills advances actor actions (movement and skill use through
// windup → effect point → recovery) and flies projectiles. Effects produce
// Hits; the combat package resolves them.
package skills

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// meleeGrace extends melee reach at the effect point so a target that
// shuffled slightly during windup doesn't whiff the swing.
var meleeGrace = fm.FromMilli(500)

// AdvanceActions steps every living actor's current action by one tick,
// in actor slice order.
func AdvanceActions(w *core.World) {
	for _, a := range w.Actors {
		if a.Dead {
			continue
		}
		switch a.Action.Kind {
		case core.ActionMove:
			stepMove(a)
		case core.ActionSkill:
			stepSkill(w, a)
		}
	}
}

func stepMove(a *core.Actor) {
	speed := a.Sheet.Eval(stats.MoveSpeed, 0)
	if speed <= 0 {
		a.Action = core.Action{}
		return
	}
	step := fm.Div(speed, fm.FromInt(core.TicksPerSecond))
	delta := a.Action.MoveTarget.Sub(a.Pos)
	if delta.Len() <= step {
		a.Pos = a.Action.MoveTarget
		a.Action = core.Action{}
		return
	}
	a.Pos = a.Pos.Add(delta.Normalize().Scale(step))
}

func stepSkill(w *core.World, a *core.Actor) {
	if a.Action.TicksLeft > 0 {
		a.Action.TicksLeft--
	}
	if a.Action.TicksLeft > 0 {
		return
	}
	switch a.Action.Phase {
	case core.PhaseWindup:
		fire(w, a)
		if a.Action.RecoveryTicks == 0 {
			a.Action = core.Action{}
			return
		}
		a.Action.Phase = core.PhaseRecovery
		a.Action.TicksLeft = a.Action.RecoveryTicks
	case core.PhaseRecovery:
		a.Action = core.Action{}
	}
}

// fire is the effect point: the moment the skill actually happens.
func fire(w *core.World, a *core.Actor) {
	sk := a.Action.Skill
	switch sk.Kind {
	case core.SkillProjectile:
		dir := a.Action.AimPoint.Sub(a.Pos).Normalize()
		if dir == (space.Vec2{}) {
			dir = space.V(fm.One, 0) // aiming at your own feet fires +X
		}
		vel := dir.Scale(fm.Div(sk.ProjSpeed, fm.FromInt(core.TicksPerSecond)))
		w.SpawnProjectile(a, sk, a.Pos, vel)
	case core.SkillMelee:
		tgt := w.ActorByID(a.Action.TargetID)
		if tgt == nil || tgt.Dead {
			return // swing at a corpse: whiff, no refund
		}
		reach := sk.Range + a.Def.Radius + tgt.Def.Radius + meleeGrace
		if space.Dist(a.Pos, tgt.Pos) > reach {
			return
		}
		w.QueueHit(core.Hit{
			Attacker: a.ID,
			Defender: tgt.ID,
			Skill:    sk,
			Tags:     sk.Tags.With(stats.TagHit),
		})
	}
}

// UpdateProjectiles moves every live projectile one tick and queues a hit on
// the earliest hostile actor its swept path touches. Projectiles are
// single-target: first contact consumes them.
func UpdateProjectiles(w *core.World) {
	for _, p := range w.Projectiles {
		if p.Dead {
			continue
		}
		next := p.Pos.Add(p.Vel)

		var best *core.Actor
		var bestT fm.Fixed
		var bestPt space.Vec2
		for _, a := range w.Actors {
			if a.Dead || a.Team == p.Team {
				continue
			}
			pt, t, ok := space.SegCircleHit(p.Pos, next, a.Pos, a.Def.Radius+p.Skill.ProjRadius)
			if !ok {
				continue
			}
			// Strict < keeps the earlier-spawned actor on exact ties.
			if best == nil || t < bestT {
				best, bestT, bestPt = a, t, pt
			}
		}
		if best != nil {
			p.Pos = bestPt
			p.Dead = true
			w.QueueHit(core.Hit{
				Attacker: p.Source,
				Defender: best.ID,
				Skill:    p.Skill,
				Tags:     p.Skill.Tags.With(stats.TagHit),
			})
			continue
		}

		p.Pos = next
		if p.TicksLeft == 0 {
			p.Dead = true
			continue
		}
		p.TicksLeft--
	}
}
