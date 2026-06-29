package server

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
)

func zombieLevels(s *sim.Sim) []int {
	var out []int
	for _, a := range s.W.Actors {
		if a.Def.ID == "zombie" {
			out = append(out, a.Level)
		}
	}
	return out
}

// TestBuildFloorScalesPacks: floor 1 spawns base-level packs (matching the
// pre-descent behavior), and each floor down raises the pack level — the
// escalation the descent is for.
func TestBuildFloorScalesPacks(t *testing.T) {
	in, err := New(content.DB(), Config{
		Seed:    5,
		Map:     &protocol.MapSpec{Width: 32, Height: 32, Rooms: 6},
		Scatter: []protocol.Scatter{{Def: "zombie", Count: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}

	f1 := zombieLevels(in.sim)
	if len(f1) != 4 {
		t.Fatalf("floor 1 spawned %d zombies, want 4", len(f1))
	}
	for _, lv := range f1 {
		if lv != 1 {
			t.Errorf("floor 1 zombie level = %d, want 1 (base)", lv)
		}
	}

	f2, err := in.buildFloor(2)
	if err != nil {
		t.Fatal(err)
	}
	want := 1 + levelBonus(2)
	levels := zombieLevels(f2)
	if len(levels) != 4 {
		t.Fatalf("floor 2 spawned %d zombies, want 4", len(levels))
	}
	for _, lv := range levels {
		if lv != want {
			t.Errorf("floor 2 zombie level = %d, want %d", lv, want)
		}
	}
}
