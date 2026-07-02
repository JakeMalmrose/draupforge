package sim_test

// Territorial aggro: monsters notice enemies by line of sight (or hearing,
// up close, through walls), only engage inside their leash territory, and
// walk back home when nothing qualifies. These pin the behavior that keeps
// packs from converging on the spawn room — the death-spiral fix.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// openHall is a wide unobstructed room: line of sight everywhere, so only
// distance and leash decide engagement.
func openHall() []string {
	rows := make([]string, 12)
	rows[0] = "########################################"
	rows[11] = rows[0]
	for y := 1; y < 11; y++ {
		rows[y] = "#......................................#"
	}
	return rows
}

// TestLeashDisengageAndReturnHome: a zombie chases a player through its
// territory, but once the player stands beyond the leash the zombie gives
// up and walks all the way back to its spawn point.
func TestLeashDisengageAndReturnHome(t *testing.T) {
	s := simWithGrid(openHall(), 21)
	player := mustSpawn(t, s, "player", 12500, 5500)
	zombie := mustSpawn(t, s, "zombie", 5500, 5500)

	s.Step(nil)
	z := s.W.ActorByID(zombie)
	if z.Action.Kind == core.ActionIdle {
		t.Fatal("zombie ignored a visible player inside its territory")
	}

	// Drag complete: the player now stands outside the territory
	// (leash 20 from home), still within the zombie's aggro radius.
	s.W.ActorByID(player).Pos = space.V(fm.FromMilli(28500), fm.FromMilli(5500))
	z.Pos = space.V(fm.FromMilli(15500), fm.FromMilli(5500))

	for i := 0; i < 300; i++ {
		s.Step(nil)
	}
	if d := space.Dist(z.Pos, z.Home); d > fm.FromMilli(1500) {
		t.Errorf("disengaged zombie ended %v from home, want it back", d)
	}
	if z.Action.Kind != core.ActionIdle {
		t.Errorf("zombie at home should idle, action = %v", z.Action.Kind)
	}
}

// TestAggroLineOfSightAndHearing: behind a wall a monster only notices an
// enemy inside hearing range (half aggro) — beyond that it stays oblivious
// until it has line of sight.
func TestAggroLineOfSightAndHearing(t *testing.T) {
	grid := []string{
		"##############",
		"#.....#......#",
		"#.....#......#",
		"#.....#......#",
		"#............#",
		"#............#",
		"#............#",
		"##############",
	}

	// Distance 10: inside aggro (15), beyond hearing (7.5), wall between.
	s := simWithGrid(grid, 22)
	mustSpawn(t, s, "player", 12500, 2500)
	zombie := mustSpawn(t, s, "zombie", 2500, 2500)
	s.Step(nil)
	z := s.W.ActorByID(zombie)
	if z.Action.Kind != core.ActionIdle {
		t.Fatalf("zombie noticed an unseen player beyond hearing range, action = %v", z.Action.Kind)
	}

	// Distance 6: still no line of sight, but close enough to hear.
	s2 := simWithGrid(grid, 23)
	mustSpawn(t, s2, "player", 8500, 2500)
	zombie2 := mustSpawn(t, s2, "zombie", 2500, 2500)
	s2.Step(nil)
	if s2.W.ActorByID(zombie2).Action.Kind != core.ActionMove {
		t.Fatalf("zombie didn't react to a heard player, action = %v", s2.W.ActorByID(zombie2).Action.Kind)
	}
}

// TestLeashIgnoresOutOfTerritoryEnemy: an enemy in plain sight and aggro
// range, but outside the territory, is not engaged — the monster heads
// home instead of starting a chase it would immediately abandon.
func TestLeashIgnoresOutOfTerritoryEnemy(t *testing.T) {
	s := simWithGrid(openHall(), 24)
	mustSpawn(t, s, "player", 30500, 5500)
	zombie := mustSpawn(t, s, "zombie", 20500, 5500)

	// Simulate a zombie mid-chase, far from its home tile.
	z := s.W.ActorByID(zombie)
	z.Home = space.V(fm.FromMilli(5500), fm.FromMilli(5500))

	s.Step(nil)
	if z.Action.Kind != core.ActionMove {
		t.Fatalf("dragged zombie should walk home, action = %v", z.Action.Kind)
	}
	if z.Action.MoveTarget != z.Home {
		t.Errorf("zombie walking to %v, want home %v", z.Action.MoveTarget, z.Home)
	}
}
