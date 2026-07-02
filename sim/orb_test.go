package sim_test

// Currency crafting-lite: orbs bank to the killer on kills and transform
// bag items — transmute (normal→magic), alchemy (normal→rare), chaos
// (reroll rare). All affix rolls ride the loot stream.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// bagItem plants a fresh normal item in the actor's bag and returns its ID.
func bagItem(s *sim.Sim, a *core.Actor, base string) core.EntityID {
	item := core.Item{ID: s.W.AllocID(), Base: s.W.Content.BaseItems[base], Rarity: core.RarityNormal}
	a.Inventory = append(a.Inventory, item)
	return item.ID
}

func applyOrb(s *sim.Sim, id core.EntityID, orb core.OrbKind, item core.EntityID) {
	s.Step([]core.Command{{Actor: id, Kind: core.CmdApplyOrb, Orb: orb, TargetID: item}})
}

func TestOrbsTransformBagItems(t *testing.T) {
	s := sim.New(content.DB(), 81)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)
	a.Orbs = [core.OrbCount]int32{2, 1, 1}

	sword := bagItem(s, a, "rusty_sword")
	cap_ := bagItem(s, a, "leather_cap")

	// Chaos on a normal item: rejected, nothing spent.
	applyOrb(s, id, core.OrbChaos, sword)
	if a.Orbs[core.OrbChaos] != 1 || a.Inventory[0].Rarity != core.RarityNormal {
		t.Fatal("chaos orb applied to a normal item")
	}

	applyOrb(s, id, core.OrbTransmutation, sword)
	if got := a.Inventory[0]; got.Rarity != core.RarityMagic || len(got.Affixes) < 1 || len(got.Affixes) > 2 {
		t.Fatalf("transmuted sword: rarity %v, %d affixes", got.Rarity, len(got.Affixes))
	}
	if a.Orbs[core.OrbTransmutation] != 1 {
		t.Fatalf("transmutation wallet = %d, want 1", a.Orbs[core.OrbTransmutation])
	}

	applyOrb(s, id, core.OrbAlchemy, cap_)
	if got := a.Inventory[1]; got.Rarity != core.RarityRare || len(got.Affixes) < 4 {
		t.Fatalf("alched cap: rarity %v, %d affixes", got.Rarity, len(got.Affixes))
	}

	applyOrb(s, id, core.OrbChaos, cap_)
	if got := a.Inventory[1]; got.Rarity != core.RarityRare || len(got.Affixes) < 4 || a.Orbs[core.OrbChaos] != 0 {
		t.Fatalf("chaosed cap: rarity %v, %d affixes, wallet %d", got.Rarity, len(got.Affixes), a.Orbs[core.OrbChaos])
	}

	// Wallet empty: rejected.
	applyOrb(s, id, core.OrbChaos, cap_)
	if a.Orbs[core.OrbChaos] != 0 {
		t.Fatal("spent an orb that wasn't there")
	}
}

func TestOrbDropsBankToKiller(t *testing.T) {
	s := sim.New(content.DB(), 82)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)

	sawEvent := false
	for i := 0; i < 200; i++ {
		did := mustSpawn(t, s, "training_dummy", 5000, 0)
		d := s.W.ActorByID(did)
		d.Dead = true
		s.W.Emit(core.Event{Kind: core.EvDeath, Actor: did, Other: id})
		items.RollLoot(s.W)
		for _, ev := range s.W.Events() {
			if ev.Kind == core.EvOrb {
				sawEvent = true
			}
		}
		s.W.EndTick()
		var total int32
		for _, n := range a.Orbs {
			total += n
		}
		if total > 0 {
			if !sawEvent {
				t.Fatal("wallet grew without an orb event")
			}
			return
		}
	}
	t.Fatal("200 kills never dropped an orb (rates are ~13.5%/kill)")
}

func TestOrbWalletDurable(t *testing.T) {
	s := sim.New(content.DB(), 83)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)
	a.Orbs = [core.OrbCount]int32{3, 0, 7}

	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 84)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if b.Orbs != a.Orbs {
		t.Fatalf("transferred wallet = %v, want %v", b.Orbs, a.Orbs)
	}

	data, err := s2.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	w3, err := core.LoadWorld(s2.W.Content, data)
	if err != nil {
		t.Fatal(err)
	}
	if s2.W.Hash() != w3.Hash() {
		t.Fatal("hash changed across save/load of a wallet world")
	}
	if w3.Actors[0].Orbs != a.Orbs {
		t.Fatalf("loaded wallet = %v", w3.Actors[0].Orbs)
	}
}
