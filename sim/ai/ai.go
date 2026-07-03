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
	"boss_brute":   bossBrute,
	"boss_king":    bossKing,
	"minion_melee": minionMelee,
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

// meleeChaser: aggro the nearest enemy in territory, walk into reach, swing.
// Re-decides every tick it isn't mid-swing, so it tracks moving targets;
// walls are the pathfinder's problem (CmdMove routes around them).
func meleeChaser(w *core.World, a *core.Actor) (core.Command, bool) {
	if a.Action.Kind == core.ActionSkill {
		return core.Command{}, false // committed to the swing
	}
	tgt := acquireTarget(w, a)
	if tgt == nil {
		return returnHome(a)
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
	tgt := acquireTarget(w, a)
	if tgt == nil {
		return returnHome(a)
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

// bossBrute: a two-skill heavyweight. Skill selection is stateless and
// reads as intent: inside slamRange it winds up Skills[0] (the big
// telegraphed AoE — your cue to move), otherwise with line of sight it
// throws Skills[1], and without either it lumbers closer.
func bossBrute(w *core.World, a *core.Actor) (core.Command, bool) {
	if a.Action.Kind == core.ActionSkill {
		return core.Command{}, false
	}
	tgt := acquireTarget(w, a)
	if tgt == nil {
		return returnHome(a)
	}
	slam := w.Content.Skills[a.Def.Skills[0]]
	slamReach := slam.AoERadius + tgt.Def.Radius
	if space.Dist(a.Pos, tgt.Pos) <= slamReach {
		return core.Command{Actor: a.ID, Kind: core.CmdUseSkill, Skill: slam.ID}, true
	}
	los := true
	if w.Grid != nil {
		_, blocked := w.Grid.SegmentHit(a.Pos, tgt.Pos)
		los = !blocked
	}
	if los && len(a.Def.Skills) > 1 {
		return core.Command{
			Actor: a.ID, Kind: core.CmdUseSkill,
			Skill: a.Def.Skills[1], Point: tgt.Pos,
		}, true
	}
	return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: tgt.Pos}, true
}

// minionFollowGap is how far a minion drifts from its owner before it
// breaks off to catch up; minionLeash bounds how far from the owner it
// will engage — the pack fights around its summoner, not across the map.
var (
	minionFollowGap = fm.FromInt(4)
	minionLeash     = fm.FromInt(10)
)

// minionMelee: fight what threatens the owner, otherwise heel. The leash
// anchors to the owner's CURRENT position (mobile territory); an orphaned
// minion (owner gone or dead) fights on unleashed like a plain chaser.
func minionMelee(w *core.World, a *core.Actor) (core.Command, bool) {
	if a.Action.Kind == core.ActionSkill {
		return core.Command{}, false
	}
	owner := w.ActorByID(a.Owner)
	if owner != nil && owner.Dead {
		owner = nil
	}
	var tgt *core.Actor
	var bestDist fm.Fixed
	for _, o := range w.Actors {
		if o.Dead || o.ID == a.ID || o.Team == a.Team || o.Team == core.TeamNone {
			continue
		}
		d := space.Dist(a.Pos, o.Pos)
		if d > a.Def.AggroRadius {
			continue
		}
		if owner != nil && space.Dist(owner.Pos, o.Pos) > minionLeash {
			continue // too far from the summoner to be our problem
		}
		if tgt == nil || d < bestDist {
			tgt, bestDist = o, d
		}
	}
	if tgt != nil {
		skID := a.Def.Skills[0]
		sk := w.Content.Skills[skID]
		if space.Dist(a.Pos, tgt.Pos) <= sk.Range+a.Def.Radius+tgt.Def.Radius {
			return core.Command{Actor: a.ID, Kind: core.CmdUseSkill, Skill: skID, TargetID: tgt.ID}, true
		}
		return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: tgt.Pos}, true
	}
	if owner != nil && space.Dist(a.Pos, owner.Pos) > minionFollowGap {
		return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: owner.Pos}, true
	}
	return core.Command{}, false
}

// bossKing: the staged-skill boss. Skills are [slam, volley, storm] — all
// telegraphed sequences the player dodges by reading. Selection is a pure
// function of distance and current life (no AI memory): inside slam range
// the tracked triple slam; at range with line of sight, ring volleys —
// swapped for the gap-bisecting double storm below half life; otherwise it
// stalks closer. Pacing comes from each skill's built-in recovery stage.
func bossKing(w *core.World, a *core.Actor) (core.Command, bool) {
	if a.Action.Kind == core.ActionSkill {
		return core.Command{}, false // committed to the sequence
	}
	tgt := acquireTarget(w, a)
	if tgt == nil {
		return returnHome(a)
	}
	slam := w.Content.Skills[a.Def.Skills[0]]
	if space.Dist(a.Pos, tgt.Pos) <= slam.Range+tgt.Def.Radius {
		return core.Command{
			Actor: a.ID, Kind: core.CmdUseSkill,
			Skill: slam.ID, TargetID: tgt.ID, Point: tgt.Pos,
		}, true
	}
	los := true
	if w.Grid != nil {
		_, blocked := w.Grid.SegmentHit(a.Pos, tgt.Pos)
		los = !blocked
	}
	if los && len(a.Def.Skills) > 2 {
		ring := a.Def.Skills[1]
		if a.Life*2 < a.MaxLife() {
			ring = a.Def.Skills[2] // enraged: the storm
		}
		return core.Command{
			Actor: a.ID, Kind: core.CmdUseSkill,
			Skill: ring, TargetID: tgt.ID, Point: tgt.Pos,
		}, true
	}
	return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: tgt.Pos}, true
}

// hearingDiv: through walls, an enemy is noticed only within
// AggroRadius/hearingDiv — "heard you up close". With line of sight the
// full radius applies.
var hearingDiv = fm.FromInt(2)

// homeSlack is how close to Home counts as being home — a tile, so a
// returned monster idles instead of shuffling onto the exact spot.
var homeSlack = fm.One

// acquireTarget picks the enemy this actor engages: the nearest one it can
// notice (line of sight, or hearing range through walls) that stands inside
// its territory (within LeashRadius of Home, when leashed). Everything here
// is a pure function of positions, so a monster at a boundary never
// oscillates between engaging and disengaging — the verdict only changes
// when someone moves.
func acquireTarget(w *core.World, a *core.Actor) *core.Actor {
	hearing := fm.Div(a.Def.AggroRadius, hearingDiv)
	var best *core.Actor
	var bestDist fm.Fixed
	for _, o := range w.Actors {
		if o.Dead || o.ID == a.ID || o.Team == a.Team || o.Team == core.TeamNone {
			continue
		}
		d := space.Dist(a.Pos, o.Pos)
		if d > a.Def.AggroRadius {
			continue
		}
		if a.Def.LeashRadius > 0 && space.Dist(a.Home, o.Pos) > a.Def.LeashRadius {
			continue // outside my territory — not my problem
		}
		// The raycast is the expensive check, so it runs last and only
		// beyond hearing range.
		if w.Grid != nil && d > hearing {
			if _, blocked := w.Grid.SegmentHit(a.Pos, o.Pos); blocked {
				continue
			}
		}
		// Strict < breaks distance ties toward the earlier-spawned actor.
		if best == nil || d < bestDist {
			best, bestDist = o, d
		}
	}
	return best
}

// returnHome sends a disengaged leashed monster back to its spawn point,
// so packs drift home instead of piling up wherever a chase ended.
func returnHome(a *core.Actor) (core.Command, bool) {
	if a.Def.LeashRadius == 0 || space.Dist(a.Pos, a.Home) <= homeSlack {
		return core.Command{}, false
	}
	return core.Command{Actor: a.ID, Kind: core.CmdMove, Point: a.Home}, true
}
