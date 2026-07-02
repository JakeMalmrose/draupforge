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

// Multi-projectile fans spread symmetrically around the aim direction in
// fanStep increments. The cos/sin table is hardcoded fixed-point (12° per
// step — fixmath has no trig, and a fan needs none); its depth caps a fan
// at maxFanProjectiles.
var (
	fanCos = [4]fm.Fixed{fm.One, fm.FromMilli(978), fm.FromMilli(914), fm.FromMilli(809)}
	fanSin = [4]fm.Fixed{0, fm.FromMilli(208), fm.FromMilli(407), fm.FromMilli(588)}
)

const maxFanProjectiles = 7 // LMP + GMP together

// chainRange is how far a chaining projectile can jump from its impact.
var chainRange = fm.FromInt(7)

// rotate turns v by step fan increments (negative = clockwise).
func rotate(v space.Vec2, step int) space.Vec2 {
	if step == 0 {
		return v
	}
	neg := step < 0
	if neg {
		step = -step
	}
	c, s := fanCos[step], fanSin[step]
	if neg {
		s = -s
	}
	return space.V(
		fm.Mul(v.X, c)-fm.Mul(v.Y, s),
		fm.Mul(v.X, s)+fm.Mul(v.Y, c),
	)
}

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

// stepMove advances toward the current waypoint. Open-plane actions have no
// Path and head straight for MoveTarget; pathed actions walk the waypoint
// chain the pathfinder produced (already clearance-checked against terrain,
// which is immutable — no re-validation per tick). Reaching a waypoint
// consumes the whole tick's step: a corner costs at most one tick.
func stepMove(a *core.Actor) {
	speed := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{})
	if speed <= 0 {
		a.Action = core.Action{}
		return
	}
	step := fm.Div(speed, fm.FromInt(core.TicksPerSecond))
	target := a.Action.MoveTarget
	if len(a.Action.Path) > 0 {
		target = a.Action.Path[a.Action.PathStep]
	}
	delta := target.Sub(a.Pos)
	if delta.Len() <= step {
		a.Pos = target
		if a.Action.PathStep < len(a.Action.Path)-1 {
			a.Action.PathStep++
			return
		}
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
		n := 1 + a.Action.Gem.ExtraProjectiles()
		if n > maxFanProjectiles {
			n = maxFanProjectiles
		}
		// Left-to-right fan centered on the aim; n==1 keeps the exact
		// pre-gem math (rotate(v, 0) is identity).
		for i := 0; i < n; i++ {
			w.SpawnProjectileGem(a, sk, a.Pos, rotate(vel, i-(n-1)/2), a.Action.Gem)
		}
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
			Gem:      a.Action.Gem,
		})
	case core.SkillBuff:
		if def := w.Content.Buffs[sk.SelfBuff]; def != nil {
			w.QueueBuff(core.PendingBuff{Target: a.ID, Buff: def, Source: a.ID})
		}
	case core.SkillNova:
		// One independent hit per target (own damage roll, own crit roll),
		// queued in actor slice order.
		for _, tgt := range w.Actors {
			if tgt.Dead || tgt.Team == a.Team || tgt.Team == core.TeamNone {
				continue
			}
			if space.Dist(a.Pos, tgt.Pos) > sk.AoERadius+tgt.Def.Radius {
				continue
			}
			w.QueueHit(core.Hit{
				Attacker: a.ID,
				Defender: tgt.ID,
				Skill:    sk,
				Tags:     sk.Tags.With(stats.TagHit),
				Gem:      a.Action.Gem,
			})
		}
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

		// Walls clip the sweep first: an actor standing behind one can't be
		// hit by a projectile that never reaches it. Wall impacts kill the
		// projectile with no hit (and no event — clients just see it stop).
		wallT := fm.One + 1
		if w.Grid != nil {
			if t, hit := w.Grid.SegmentHit(p.Pos, next); hit {
				wallT = t
			}
		}

		var best *core.Actor
		var bestT fm.Fixed
		var bestPt space.Vec2
		for _, a := range w.Actors {
			if a.Dead || a.Team == p.Team || alreadyHit(p, a.ID) {
				continue
			}
			pt, t, ok := space.SegCircleHit(p.Pos, next, a.Pos, a.Def.Radius+p.Skill.ProjRadius)
			if !ok || t >= wallT {
				continue
			}
			// Strict < keeps the earlier-spawned actor on exact ties.
			if best == nil || t < bestT {
				best, bestT, bestPt = a, t, pt
			}
		}
		if best == nil && wallT <= fm.One {
			p.Pos = p.Pos.Add(next.Sub(p.Pos).Scale(wallT))
			p.Dead = true
			continue
		}
		if best != nil {
			p.Pos = bestPt
			w.QueueHit(core.Hit{
				Attacker: p.Source,
				Defender: best.ID,
				Skill:    p.Skill,
				Tags:     p.Skill.Tags.With(stats.TagHit),
				Gem:      p.Gem,
			})
			// Chain: redirect at the impact point toward the nearest fresh
			// enemy in range and line of sight; no candidate ends the flight.
			if p.ChainsLeft > 0 {
				p.HitIDs = append(p.HitIDs, best.ID)
				if tgt := chainTarget(w, p); tgt != nil {
					dir := tgt.Pos.Sub(p.Pos).Normalize()
					if dir != (space.Vec2{}) {
						p.ChainsLeft--
						p.Vel = dir.Scale(fm.Div(p.Skill.ProjSpeed, fm.FromInt(core.TicksPerSecond)))
						p.TicksLeft = p.Skill.ProjTTL
						continue
					}
				}
			}
			p.Dead = true
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

// alreadyHit reports whether a chaining projectile has struck this actor
// before. Non-chain projectiles carry no history — zero cost.
func alreadyHit(p *core.Projectile, id core.EntityID) bool {
	for _, h := range p.HitIDs {
		if h == id {
			return true
		}
	}
	return false
}

// chainTarget picks the nearest living enemy within chainRange of the
// projectile's impact point that it hasn't struck, requiring line of sight
// on grid worlds. Slice order + strict < keeps ties deterministic.
func chainTarget(w *core.World, p *core.Projectile) *core.Actor {
	var best *core.Actor
	var bestD fm.Fixed
	for _, a := range w.Actors {
		if a.Dead || a.Team == p.Team || a.Team == core.TeamNone || alreadyHit(p, a.ID) {
			continue
		}
		d := space.Dist(p.Pos, a.Pos)
		if d > chainRange {
			continue
		}
		if w.Grid != nil {
			if _, blocked := w.Grid.SegmentHit(p.Pos, a.Pos); blocked {
				continue
			}
		}
		if best == nil || d < bestD {
			best, bestD = a, d
		}
	}
	return best
}
