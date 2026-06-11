// Package ai decides monster behavior. Deciders emit the same Commands
// players send — AI gets no private channel into the sim, so everything it
// does is validated by the same rules.
package ai

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// decider is one behavior: look at the world, emit at most one command.
type decider func(w *core.World, a *core.Actor) (core.Command, bool)

// deciders is the behavior registry, keyed by ActorDef.AI. Lookup-only —
// the decide loop iterates actors, never this map.
var deciders = map[string]decider{
	"melee_chaser": meleeChaser,
	"ranged_kiter": rangedKiter,
}

// Decide produces this tick's AI commands in actor slice order.
func Decide(w *core.World) []core.Command {
	var cmds []core.Command
	for _, a := range w.Actors {
		if a.Dead || a.Def.AI == "" {
			continue
		}
		if decide, ok := deciders[a.Def.AI]; ok {
			if c, ok := decide(w, a); ok {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

// meleeChaser: aggro the nearest enemy in radius, walk into reach, swing.
// Re-decides every tick it isn't mid-swing, so it tracks moving targets;
// walls are the pathfinder's problem (CmdMove routes around them).
func meleeChaser(w *core.World, a *core.Actor) (core.Command, bool) {
	if a.Action.Kind == core.ActionSkill {
		return core.Command{}, false // committed to the swing
	}
	tgt := nearestEnemy(w, a, a.Def.AggroRadius)
	if tgt == nil {
		return core.Command{}, false
	}
	skID := a.Def.Skills[0]
	sk := w.Content.Skills[skID]
	reach := sk.Range + a.Def.Radius + tgt.Def.Radius
	if space.Dist(a.Pos, tgt.Pos) <= reach {
		return core.Command{Actor: a.ID, Kind: core.CmdUseSkill, Skill: skID, TargetID: tgt.ID}, true
	}
	return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: tgt.Pos}, true
}

// rangedKiter: hold a firing position inside PreferredRange. No line of
// sight or out of range → close in (pathing routes around walls); enemy
// inside a third of preferred range → back off to open ground; otherwise
// shoot at where the target stands.
func rangedKiter(w *core.World, a *core.Actor) (core.Command, bool) {
	if a.Action.Kind == core.ActionSkill {
		return core.Command{}, false
	}
	tgt := nearestEnemy(w, a, a.Def.AggroRadius)
	if tgt == nil {
		return core.Command{}, false
	}
	los := true
	if w.Grid != nil {
		_, blocked := w.Grid.SegmentHit(a.Pos, tgt.Pos)
		los = !blocked
	}
	d := space.Dist(a.Pos, tgt.Pos)
	pr := a.Def.PreferredRange
	if !los || d > pr {
		return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: tgt.Pos}, true
	}
	if d < fm.Div(pr, fm.FromInt(3)) {
		if pt, ok := retreatPoint(w, a, tgt); ok {
			return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: pt}, true
		}
		// Cornered: stand and fight.
	}
	return core.Command{
		Actor: a.ID, Kind: core.CmdUseSkill,
		Skill: a.Def.Skills[0], Point: tgt.Pos,
	}, true
}

// retreatDist is how far a kiter backs off per decision — a few tiles, so
// the move outpaces the chaser's per-tick gains but re-decides often.
var retreatDist = fm.FromInt(3)

// retreatPoint picks where a kiter backs off to: straight away from the
// threat, or veering 45°/90° left/right when a wall is in the way. The
// candidate order is fixed — determinism over cleverness.
func retreatPoint(w *core.World, a *core.Actor, tgt *core.Actor) (space.Vec2, bool) {
	away := a.Pos.Sub(tgt.Pos).Normalize()
	if away == (space.Vec2{}) {
		away = space.V(fm.One, 0) // standing inside the threat: any way out
	}
	perpL := space.V(-away.Y, away.X)
	perpR := space.V(away.Y, -away.X)
	candidates := [5]space.Vec2{
		away,
		away.Add(perpL).Normalize(),
		away.Add(perpR).Normalize(),
		perpL,
		perpR,
	}
	for _, dir := range candidates {
		pt := a.Pos.Add(dir.Scale(retreatDist))
		if w.Grid == nil || w.Grid.CanMove(a.Pos, pt) {
			return pt, true
		}
	}
	return space.Vec2{}, false
}

func nearestEnemy(w *core.World, a *core.Actor, radius fm.Fixed) *core.Actor {
	var best *core.Actor
	var bestDist fm.Fixed
	for _, o := range w.Actors {
		if o.Dead || o.ID == a.ID || o.Team == a.Team || o.Team == core.TeamNone {
			continue
		}
		d := space.Dist(a.Pos, o.Pos)
		if d > radius {
			continue
		}
		// Strict < breaks distance ties toward the earlier-spawned actor.
		if best == nil || d < bestDist {
			best, bestDist = o, d
		}
	}
	return best
}
