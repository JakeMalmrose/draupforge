// Entity separation — the soft de-overlap pass that keeps packs (and
// skeleton armies) reading as individuals instead of collapsing into one
// blob. Monsters and player-owned minions participate; actual players are
// never pushed (and never push): body-blocking is a deliberate non-feature
// for now.
package skills

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// separateStep caps how far one pair pushes apart per tick — soft enough
// that converging monsters ease apart over a few ticks instead of popping.
var separateStep = fm.FromMilli(60)

// overlapFraction: pairs closer than this fraction of their summed radii
// get pushed. Under 1.0 so ranks can still brush shoulders.
var overlapFraction = fm.FromMilli(900)

// separable reports whether the de-overlap pass may move an actor: monsters
// and player-owned minions, never an actual player. A pair pushes only when
// both ends are separable, so nothing ever shoves (or hides inside) a player.
func separable(a *core.Actor) bool {
	return !a.Dead && (a.Team == core.TeamMonsters || a.Owner != 0)
}

// Separate nudges overlapping pairs apart, half the push each, clamped by
// walkability. Deterministic by construction: pairs iterate in actor slice
// order (i<j), pushes are pure position math, no RNG. Runs as its own phase
// right after movement.
func Separate(w *core.World) {
	actors := w.Actors
	for i := 0; i < len(actors); i++ {
		a := actors[i]
		if !separable(a) {
			continue
		}
		for j := i + 1; j < len(actors); j++ {
			b := actors[j]
			if !separable(b) {
				continue
			}
			want := fm.Mul(a.Def.Radius+b.Def.Radius, overlapFraction)
			d := space.Dist(a.Pos, b.Pos)
			if d >= want {
				continue
			}
			var dir space.Vec2
			if d > 0 {
				dir = b.Pos.Sub(a.Pos).Normalize()
			} else {
				// Perfectly stacked: separate along x, the earlier-spawned
				// actor yielding left — arbitrary but deterministic.
				dir = space.V(fm.One, 0)
			}
			push := fm.Min(fm.Div(want-d, fm.FromInt(2)), separateStep)
			nudge(w, a, dir.Scale(-push))
			nudge(w, b, dir.Scale(push))
		}
	}
}

// nudge moves an actor if the grid allows it; open-plane worlds always do.
func nudge(w *core.World, a *core.Actor, delta space.Vec2) {
	to := a.Pos.Add(delta)
	if w.Grid != nil && !w.Grid.CanMove(a.Pos, to) {
		return
	}
	a.Pos = to
}
