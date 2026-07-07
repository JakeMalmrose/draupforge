package sim_test

// Milestone passives: level-gated, one pick per milestone, permanent, and
// durable across zone transfers and saves.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func choose(s *sim.Sim, id core.EntityID, passive string) {
	s.Step([]core.Command{{Actor: id, Kind: core.CmdChoosePassive, Passive: passive}})
}

func TestChoosePassiveGatesAndApplies(t *testing.T) {
	s := sim.New(content.DB(), 51)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)

	choose(s, id, "iron_constitution") // level 1: milestone 5 locked
	if len(a.Passives) != 0 {
		t.Fatal("under-leveled pick was accepted")
	}

	a.SetLevel(5)
	base := a.MaxLife()              // after level growth, before the passive
	choose(s, id, "no_such_passive") // unknown IDs are dropped
	choose(s, id, "iron_constitution")
	if len(a.Passives) != 1 || a.Passives[0].ID != "iron_constitution" {
		t.Fatalf("passives = %v, want iron_constitution", a.Passives)
	}
	if want := base + fm.FromInt(40); a.MaxLife() != want {
		t.Errorf("MaxLife = %v, want %v (+40 from the passive)", a.MaxLife(), want)
	}

	choose(s, id, "keen_eye") // same milestone: fork already taken
	if len(a.Passives) != 1 {
		t.Fatal("second pick on a taken milestone was accepted")
	}

	a.SetLevel(10)
	choose(s, id, "executioner") // next milestone unlocks a new pick
	if len(a.Passives) != 2 {
		t.Fatal("milestone 10 pick rejected at level 10")
	}
}

func TestPassivesSurviveTransferAndSave(t *testing.T) {
	s := sim.New(content.DB(), 52)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)
	a.SetLevel(5)
	choose(s, id, "clear_mind")
	wantMana := a.MaxMana()

	// Zone transfer: extract, inject into a fresh world.
	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 53)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Passives) != 1 || b.Passives[0].ID != "clear_mind" {
		t.Fatalf("transferred passives = %v, want clear_mind", b.Passives)
	}
	if b.MaxMana() != wantMana {
		t.Errorf("transferred MaxMana = %v, want %v", b.MaxMana(), wantMana)
	}

	// Save round trip: same hash, passive resolved.
	data, err := s2.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	w3, err := core.LoadWorld(s2.W.Content, data)
	if err != nil {
		t.Fatal(err)
	}
	if s2.W.Hash() != w3.Hash() {
		t.Fatal("hash changed across save/load of a passives world")
	}
	if got := w3.Actors[0].Passives; len(got) != 1 || got[0].ID != "clear_mind" {
		t.Fatalf("loaded passives = %v, want clear_mind", got)
	}
}

// TestMilestoneLadderReachesCap walks a max-level character up the whole
// ladder: one fork per fifth level, 5 through 50, each choice landing its
// mods on the sheet.
func TestMilestoneLadderReachesCap(t *testing.T) {
	s := sim.New(content.DB(), 81)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	a.SetLevel(50)

	for m := 5; m <= 50; m += 5 {
		var pick *core.PassiveDef
		for _, p := range s.W.Content.Passives {
			if p.Milestone == m {
				pick = p
				break
			}
		}
		if pick == nil {
			t.Fatalf("no passive at milestone %d", m)
		}
		s.Step([]core.Command{{Actor: player, Kind: core.CmdChoosePassive, Passive: pick.ID}})
		if !a.HasMilestone(m) {
			t.Fatalf("milestone %d pick %s was refused", m, pick.ID)
		}
	}
	if len(a.Passives) != 10 {
		t.Fatalf("passives taken = %d, want 10", len(a.Passives))
	}
	// A second pick at a spent milestone is refused.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdChoosePassive, Passive: "heavy_hands"}})
	if len(a.Passives) != 10 {
		t.Error("a spent milestone accepted a second pick")
	}
}
