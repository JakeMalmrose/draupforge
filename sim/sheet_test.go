package sim_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
)

// TestBuildSheetReadOnly: the character sheet is derived data — building
// one must not consume RNG, emit events, or touch any hashed state, or a
// C-panel peek would fork replays.
func TestBuildSheetReadOnly(t *testing.T) {
	s := sim.New(content.DB(), 7)
	id := mustSpawn(t, s, "player", 0, 0)
	if err := s.GrantGem(id, "fireball", 5); err != nil {
		t.Fatal(err)
	}
	before := s.W.Hash()

	sheet := sim.BuildSheet(s.W, id)
	if sheet == nil {
		t.Fatal("no sheet for a live player")
	}
	if s.W.Hash() != before {
		t.Fatal("BuildSheet mutated the world")
	}

	names := map[string]int64{}
	for _, st := range sheet.Stats {
		names[st.Name] = st.Val
	}
	if names["maximum life"] <= 0 {
		t.Fatalf("sheet maximum life = %d, want > 0", names["maximum life"])
	}
	if _, ok := names["fire resistance"]; !ok {
		t.Fatal("sheet missing fire resistance line")
	}

	var fb *int
	for i := range sheet.Gems {
		if sheet.Gems[i].Skill == "fireball" {
			fb = &i
			break
		}
	}
	if fb == nil {
		t.Fatal("sheet missing the fireball gem")
	}
	g := sheet.Gems[*fb]
	if g.Level != 5 || g.AvgHit <= 0 || g.CastMs <= 0 || g.ManaCost <= 0 {
		t.Fatalf("fireball gem numbers off: %+v", g)
	}
	if g.Fans != 1 {
		t.Fatalf("bare fireball fans = %d, want 1", g.Fans)
	}

	// Deterministic: same world, same numbers.
	again := sim.BuildSheet(s.W, id)
	if again.Gems[*fb].AvgHit != g.AvgHit {
		t.Fatal("NominalHit is not deterministic")
	}
}
