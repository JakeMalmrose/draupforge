// Package ai decides monster behavior. Deciders emit the same Commands
// players send — AI gets no private channel into the sim, so everything it
// does is validated by the same rules.
package ai

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// Decide produces this tick's AI commands in actor slice order.
func Decide(w *core.World) []core.Command {
	var cmds []core.Command
	for _, a := range w.Actors {
		if a.Dead || a.Def.AI == "" {
			continue
		}
		switch a.Def.AI {
		case "melee_chaser":
			if c, ok := meleeChaser(w, a); ok {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

// meleeChaser: aggro the nearest enemy in radius, walk into reach, swing.
// Re-decides every tick it isn't mid-swing, so it tracks moving targets.
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
