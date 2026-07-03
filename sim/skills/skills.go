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
	if a.Action.Skill.Kind == core.SkillStaged {
		stepStaged(w, a)
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

// BeginStaged arms a staged skill action: binds every stage duration at use
// time (chill mid-sequence can't stretch a committed attack, same rule as
// RecoveryTicks) and locks stage 0's aim. The caller has already validated
// the cast and paid its cost.
func BeginStaged(w *core.World, a *core.Actor, sk *core.SkillDef, ctx core.GemCtx, aim space.Vec2, target core.EntityID, speed fm.Fixed) {
	act := core.Action{
		Kind: core.ActionSkill, Skill: sk, AimPoint: aim, TargetID: target, Gem: ctx,
		StageTicks: make([]uint32, len(sk.Stages)),
	}
	for i, st := range sk.Stages {
		act.StageTicks[i] = ScaleTicks(st.Ticks, speed)
	}
	act.TicksLeft = act.StageTicks[0]
	a.Action = act
	lockStageAim(w, a)
}

// stepStaged runs a staged action's countdown boundary: fire the finished
// stage's effect, then start the next stage (or finish the action). The
// next stage's aim locks now — its telegraph shows where things stand at
// this instant, and the zone doesn't move until it fires.
func stepStaged(w *core.World, a *core.Actor) {
	fireStage(w, a, &a.Action.Skill.Stages[a.Action.Stage])
	a.Action.Stage++
	if a.Action.Stage >= len(a.Action.Skill.Stages) {
		a.Action = core.Action{}
		return
	}
	a.Action.TicksLeft = a.Action.StageTicks[a.Action.Stage]
	lockStageAim(w, a)
}

// lockStageAim resolves and pins the current stage's aim point.
func lockStageAim(w *core.World, a *core.Actor) {
	st := &a.Action.Skill.Stages[a.Action.Stage]
	switch st.Aim {
	case core.StageAimSelf:
		a.Action.StageAim = a.Pos
	case core.StageAimPoint:
		a.Action.StageAim = a.Action.AimPoint
	default: // StageAimTarget
		if tgt := w.ActorByID(a.Action.TargetID); tgt != nil && !tgt.Dead {
			a.Action.StageAim = tgt.Pos
		} else {
			a.Action.StageAim = a.Action.AimPoint
		}
	}
}

// ringSteps is a full circle in fan steps (12° each).
const ringSteps = 30

// fireStage is a staged skill's effect point.
func fireStage(w *core.World, a *core.Actor, st *core.SkillStage) {
	sk := a.Action.Skill
	switch st.Effect {
	case core.StageBlast:
		// Full damage everywhere inside the zone — a telegraph is a binary
		// dodge, not a falloff gradient. Walls don't block it (like novas).
		for _, tgt := range w.Actors {
			if tgt.Dead || tgt.Team == a.Team || tgt.Team == core.TeamNone {
				continue
			}
			if space.Dist(a.Action.StageAim, tgt.Pos) > st.Radius+tgt.Def.Radius {
				continue
			}
			w.QueueHit(core.Hit{
				Attacker:    a.ID,
				Defender:    tgt.ID,
				Skill:       sk,
				Tags:        sk.Tags.With(stats.TagHit),
				Gem:         a.Action.Gem,
				AreaScale:   st.DamageScale,
				Telegraphed: true,
			})
		}
	case core.StageRing:
		step := st.RingStep
		if step < 1 || step > len(fanCos)-1 {
			step = len(fanCos) - 1 // one rotate() hop caps at 36°
		}
		dir := a.Action.StageAim.Sub(a.Pos).Normalize()
		if dir == (space.Vec2{}) {
			dir = space.V(fm.One, 0)
		}
		vel := dir.Scale(fm.Div(sk.ProjSpeed, fm.FromInt(core.TicksPerSecond)))
		for i := 0; i < st.RingSkew; i++ {
			vel = rotate(vel, 1)
		}
		// Successive single rotations compound milli-rounding, but that is
		// deterministic and invisible at gameplay scale.
		for fired := 0; fired < ringSteps; fired += step {
			w.SpawnProjectileGem(a, sk, a.Pos, vel, a.Action.Gem)
			vel = rotate(vel, step)
		}
	}
}

// ScaleTicks divides a base tick count by a speed multiplier. Anything an
// actor does takes at least one tick; zero-length phases stay zero.
func ScaleTicks(base uint32, speed fm.Fixed) uint32 {
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
	case core.SkillChain:
		// Hitscan: strike the enemy nearest the aim point (within Range of
		// the caster, LoS-gated), then chain outward. Every link is a full
		// independent hit; all targets are picked at the effect point, so
		// the whole zap lands this tick. Nothing in range = the cast fizzles
		// (mana spent — aim near something).
		tgt := acquireChainStart(w, a, sk)
		var hitIDs []core.EntityID
		for n := 1 + sk.Chains + a.Action.Gem.Chains(); tgt != nil && n > 0; n-- {
			w.QueueHit(core.Hit{
				Attacker: a.ID,
				Defender: tgt.ID,
				Skill:    sk,
				Tags:     sk.Tags.With(stats.TagHit),
				Gem:      a.Action.Gem,
			})
			hitIDs = append(hitIDs, tgt.ID)
			tgt = nextChainTarget(w, tgt.Pos, a.Team, hitIDs)
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
// single-target: first contact consumes them (explosions splash from the
// impact, bouncers reflect off walls instead of dying there).
func UpdateProjectiles(w *core.World) {
	for _, p := range w.Projectiles {
		if p.Dead {
			continue
		}
		wiggle(w, p)
		next := p.Pos.Add(p.Vel)

		// Walls clip the sweep first: an actor standing behind one can't be
		// hit by a projectile that never reaches it. Wall impacts kill the
		// projectile with no hit (and no event — clients just see it stop) —
		// unless it bounces.
		wallT := fm.One + 1
		wallNX, wallNY := 0, 0
		if w.Grid != nil {
			if t, nx, ny, hit := w.Grid.SegmentHitN(p.Pos, next); hit {
				wallT, wallNX, wallNY = t, nx, ny
			}
		}

		var best *core.Actor
		var bestT fm.Fixed
		var bestPt space.Vec2
		for _, a := range w.Actors {
			if a.Dead || a.Team == p.Team || hitBefore(p.HitIDs, a.ID) {
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
		if best != nil {
			p.Pos = bestPt
			w.QueueHit(core.Hit{
				Attacker: p.Source,
				Defender: best.ID,
				Skill:    p.Skill,
				Tags:     p.Skill.Tags.With(stats.TagHit),
				Gem:      p.Gem,
			})
			explode(w, p, best)
			// Chain: redirect at the impact point toward the nearest fresh
			// enemy in range and line of sight; no candidate ends the flight.
			if p.ChainsLeft > 0 {
				p.HitIDs = append(p.HitIDs, best.ID)
				if tgt := nextChainTarget(w, p.Pos, p.Team, p.HitIDs); tgt != nil {
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
		if wallT <= fm.One {
			// Bouncers reflect off the crossed wall face and keep flying from
			// their pre-impact position — skipping the partial step costs at
			// most one tick of travel and can never end up inside the wall.
			// A (0,0) normal means the flight started inside a wall; no face
			// to reflect against, so those die like everything else.
			if p.Skill.Bounce && (wallNX != 0 || wallNY != 0) {
				if wallNX != 0 {
					p.Vel.X = -p.Vel.X
				}
				if wallNY != 0 {
					p.Vel.Y = -p.Vel.Y
				}
			} else {
				p.Pos = p.Pos.Add(next.Sub(p.Pos).Scale(wallT))
				p.Dead = true
				continue
			}
		} else {
			p.Pos = next
		}
		if p.TicksLeft == 0 {
			p.Dead = true
			continue
		}
		p.TicksLeft--
	}
}

// wiggle nudges a wiggling projectile's heading by a random fan step in
// [-2, +2] (up to ±24°) every WigglePeriod ticks of flight — spark's
// drunken zigzag. One combat-stream draw per nudge, projectile slice order.
func wiggle(w *core.World, p *core.Projectile) {
	period := p.Skill.WigglePeriod
	if period == 0 {
		return
	}
	elapsed := p.Skill.ProjTTL - p.TicksLeft
	if elapsed == 0 || elapsed%period != 0 {
		return
	}
	step := int(w.RNGCombat.Uint64n(5)) - 2
	p.Vel = rotate(p.Vel, step)
}

// explode queues a projectile impact's splash: every other enemy within
// ExplodeRadius of the impact point takes the hit again, scaled down
// linearly from full at the center to nothing at the edge (Hit.AreaScale).
// Distance is measured to the target's circle edge so fat targets aren't
// shortchanged. Like novas, the splash ignores walls.
func explode(w *core.World, p *core.Projectile, direct *core.Actor) {
	r := p.Skill.ExplodeRadius
	if r <= 0 {
		return
	}
	for _, a := range w.Actors {
		if a.Dead || a.ID == direct.ID || a.Team == p.Team || a.Team == core.TeamNone {
			continue
		}
		d := space.Dist(p.Pos, a.Pos) - a.Def.Radius
		if d >= r {
			continue
		}
		scale := fm.One
		if d > 0 {
			scale = fm.One - fm.Div(d, r)
		}
		w.QueueHit(core.Hit{
			Attacker:  p.Source,
			Defender:  a.ID,
			Skill:     p.Skill,
			Tags:      p.Skill.Tags.With(stats.TagHit),
			Gem:       p.Gem,
			AreaScale: scale,
		})
	}
}

// hitBefore reports whether a chain has struck this actor already.
// Non-chain projectiles carry no history — zero cost.
func hitBefore(ids []core.EntityID, id core.EntityID) bool {
	for _, h := range ids {
		if h == id {
			return true
		}
	}
	return false
}

// acquireChainStart picks a chain skill's first victim: the enemy nearest
// the aim point among those within Range of the caster (to their circle
// edge) and in line of sight — the click picks the pack member, the range
// gates the cast. Slice order + strict < keeps ties deterministic.
func acquireChainStart(w *core.World, a *core.Actor, sk *core.SkillDef) *core.Actor {
	var best *core.Actor
	var bestD fm.Fixed
	for _, tgt := range w.Actors {
		if tgt.Dead || tgt.Team == a.Team || tgt.Team == core.TeamNone {
			continue
		}
		if space.Dist(a.Pos, tgt.Pos)-tgt.Def.Radius > sk.Range {
			continue
		}
		if w.Grid != nil {
			if _, blocked := w.Grid.SegmentHit(a.Pos, tgt.Pos); blocked {
				continue
			}
		}
		d := space.Dist(a.Action.AimPoint, tgt.Pos)
		if best == nil || d < bestD {
			best, bestD = tgt, d
		}
	}
	return best
}

// nextChainTarget picks the nearest living enemy within chainRange of the
// last strike point that the chain hasn't struck, requiring line of sight
// on grid worlds. Shared by chaining projectiles and chain skills. Slice
// order + strict < keeps ties deterministic.
func nextChainTarget(w *core.World, pos space.Vec2, team core.Team, hitIDs []core.EntityID) *core.Actor {
	var best *core.Actor
	var bestD fm.Fixed
	for _, a := range w.Actors {
		if a.Dead || a.Team == team || a.Team == core.TeamNone || hitBefore(hitIDs, a.ID) {
			continue
		}
		d := space.Dist(pos, a.Pos)
		if d > chainRange {
			continue
		}
		if w.Grid != nil {
			if _, blocked := w.Grid.SegmentHit(pos, a.Pos); blocked {
				continue
			}
		}
		if best == nil || d < bestD {
			best, bestD = a, d
		}
	}
	return best
}
