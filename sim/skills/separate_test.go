package skills_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/skills"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

func sepDef(id string, team core.Team) *core.ActorDef {
	var base [stats.StatCount]fm.Fixed
	base[stats.Life] = fm.FromInt(100)
	return &core.ActorDef{ID: id, Team: team, Radius: fm.FromMilli(500), BaseStats: base}
}

// Two stacked monsters ease apart — the original behavior, still pinned.
func TestSeparateMonsters(t *testing.T) {
	w := core.NewWorld(&core.ContentDB{}, 1)
	def := sepDef("m", core.TeamMonsters)
	a := w.SpawnActor(def, space.V(fm.FromInt(5), fm.FromInt(5)))
	b := w.SpawnActor(def, space.V(fm.FromInt(5), fm.FromInt(5)))

	for i := 0; i < 60; i++ {
		skills.Separate(w)
	}
	if d := space.Dist(a.Pos, b.Pos); d < fm.FromMilli(850) {
		t.Fatalf("monsters still stacked after separation: dist %v", d)
	}
}

// Player-owned minions de-overlap too — a skeleton army must read as an
// army, not a single-file clump (the session-64 playtest complaint).
func TestSeparateMinions(t *testing.T) {
	w := core.NewWorld(&core.ContentDB{}, 1)
	owner := w.SpawnActor(sepDef("p", core.TeamPlayers), space.V(fm.FromInt(2), fm.FromInt(2)))
	def := sepDef("skel", core.TeamPlayers)
	a := w.SpawnActor(def, space.V(fm.FromInt(5), fm.FromInt(5)))
	b := w.SpawnActor(def, space.V(fm.FromInt(5), fm.FromInt(5)))
	a.Owner = owner.ID
	b.Owner = owner.ID

	for i := 0; i < 60; i++ {
		skills.Separate(w)
	}
	if d := space.Dist(a.Pos, b.Pos); d < fm.FromMilli(850) {
		t.Fatalf("minions still stacked after separation: dist %v", d)
	}
}

// Actual players are never pushed, even overlapping a monster or a minion —
// body-blocking stays a deliberate non-feature.
func TestSeparateNeverMovesPlayers(t *testing.T) {
	w := core.NewWorld(&core.ContentDB{}, 1)
	player := w.SpawnActor(sepDef("p", core.TeamPlayers), space.V(fm.FromInt(5), fm.FromInt(5)))
	monster := w.SpawnActor(sepDef("m", core.TeamMonsters), space.V(fm.FromInt(5), fm.FromInt(5)))
	minion := w.SpawnActor(sepDef("skel", core.TeamPlayers), space.V(fm.FromInt(5), fm.FromInt(5)))
	minion.Owner = player.ID
	start := player.Pos

	for i := 0; i < 60; i++ {
		skills.Separate(w)
	}
	if player.Pos != start {
		t.Fatalf("player was pushed from %v to %v", start, player.Pos)
	}
	if monster.Pos == minion.Pos {
		t.Fatalf("monster and minion still perfectly stacked")
	}
}
