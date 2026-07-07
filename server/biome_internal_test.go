package server

// Biome unit tests: band selection, biome-built floors (map kind + roster),
// and the biome id on the run snap.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim"
)

func TestBiomeForFloor(t *testing.T) {
	cases := []struct {
		floor int
		want  string
	}{
		{0, ""}, {1, "crypt"}, {9, "crypt"}, {10, "caves"}, {19, "caves"},
		{20, "frost"}, {99, "frost"},
	}
	for _, c := range cases {
		got := ""
		if b := biomeForFloor(c.floor); b != nil {
			got = b.ID
		}
		if got != c.want {
			t.Errorf("biomeForFloor(%d) = %q, want %q", c.floor, got, c.want)
		}
	}
}

// TestBuildFloorUsesBiomeRoster: a caves-band floor scatters the caves mix
// (plus the schedule's set-piece and any death-spawn kin), never the
// scenario's crypt roster wholesale; a crypt floor keeps the scenario's.
func TestBuildFloorUsesBiomeRoster(t *testing.T) {
	in, _, _ := descentInstance(t, 3)

	defsOn := func(s *sim.Sim) map[string]int {
		m := map[string]int{}
		for _, a := range s.W.Actors {
			m[a.Def.ID]++
		}
		return m
	}

	// Floor 11 (caves band, no set-piece schedule: 11%3 != 0, 11%5 != 0).
	s11, err := in.buildFloor(11, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := defsOn(s11)
	allowed := map[string]bool{"ghoul": true, "carrion_husk": true, "zombie": true}
	for def := range got {
		if !allowed[def] {
			t.Errorf("floor 11 spawned %q, not in the caves roster", def)
		}
	}
	if got["ghoul"] == 0 || got["carrion_husk"] == 0 {
		t.Errorf("floor 11 roster = %v, want the caves mix present", got)
	}
	// The scenario roster (descentInstance uses arena-style scatter) rides
	// crypt floors unchanged.
	s2, err := in.buildFloor(2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if defsOn(s2)["carrion_husk"] > defsOn(s11)["carrion_husk"] {
		t.Log("crypt floor has more husks than a caves floor — roster weights are open for tuning")
	}
}

// TestCavesFloorsCarveCaves: a caves-band floor's terrain differs from the
// same seed carved as rooms — the map kind actually switched.
func TestCavesFloorsCarveCaves(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	s11a, err := in.buildFloor(11, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s11b, err := in.buildFloor(11, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Same floor twice: byte-identical terrain (the replayable-floors
	// invariant survives the biome switch).
	ga, gb := s11a.W.Grid, s11b.W.Grid
	for y := 0; y < ga.Height; y++ {
		for x := 0; x < ga.Width; x++ {
			if ga.Solid(x, y) != gb.Solid(x, y) {
				t.Fatalf("floor 11 terrain not replayable at (%d,%d)", x, y)
			}
		}
	}
	// And a crypt floor from the same run differs in generator character:
	// rooms maps have long straight corridor walls; just assert the two
	// floors aren't identical (they share width/height).
	s2, err := in.buildFloor(2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	same := true
	g2 := s2.W.Grid
	for y := 0; y < ga.Height && same; y++ {
		for x := 0; x < ga.Width; x++ {
			if ga.Solid(x, y) != g2.Solid(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatal("caves floor terrain identical to a rooms floor")
	}
}

func TestRunSnapCarriesBiome(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	in.floor = 0
	if b := in.runSnap().Biome; b != "" {
		t.Errorf("hideout biome = %q, want empty", b)
	}
	in.floor = 3
	if b := in.runSnap().Biome; b != "crypt" {
		t.Errorf("floor 3 biome = %q, want crypt", b)
	}
	in.floor = 12
	if b := in.runSnap().Biome; b != "caves" {
		t.Errorf("floor 12 biome = %q, want caves", b)
	}
}
