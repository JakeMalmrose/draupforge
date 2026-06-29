package space_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/space"
)

// TestExitDerivedFarAndWalkable: the stairs-down sit on reachable ground,
// away from the spawn, so a descent always crosses the floor. (testRand and
// testSpec live in mapgen_test.go.)
func TestExitDerivedFarAndWalkable(t *testing.T) {
	g := space.GenerateRooms(testSpec, &testRand{s: 7})
	if g.Exit == g.Spawn {
		t.Fatal("exit coincides with spawn on a multi-room map")
	}
	if !g.Fits(g.Exit) {
		t.Errorf("exit %v is not standable ground", g.Exit)
	}
	// The whole point is distance: the farthest room should be a real walk
	// from spawn, not an adjacent tile.
	if d := g.Exit.Sub(g.Spawn).Len(); d <= g.Tile {
		t.Errorf("exit is only %v from spawn — not a meaningful descent", d)
	}
}

// TestExitSurvivesSave: like Spawn, the exit is saved (not re-derived) so a
// restored floor descends from the same stairs.
func TestExitSurvivesSave(t *testing.T) {
	g := space.GenerateRooms(testSpec, &testRand{s: 11})
	g2, err := space.DecodeGrid(g.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if g2.Exit != g.Exit {
		t.Errorf("exit not preserved across save: %v -> %v", g.Exit, g2.Exit)
	}
	if g2.Spawn != g.Spawn {
		t.Errorf("spawn not preserved across save: %v -> %v", g.Spawn, g2.Spawn)
	}
}
