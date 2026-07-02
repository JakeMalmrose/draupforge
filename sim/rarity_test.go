package sim_test

// Monster rarity: ScatterSpawnPack rolls magic/rare monsters with modifier
// packages, worth more XP and more drop attempts. These pin the roll
// mechanics, the stream-neutrality of zero chances (goldens depend on it),
// and the save/hash coverage of rarity state.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func packSim(t *testing.T, seed uint64, magicPm, rarePm uint64) *sim.Sim {
	t.Helper()
	s := sim.New(content.DB(), seed)
	s.GenerateMap(space.MapSpec{Width: 40, Height: 40, Rooms: 7})
	if err := s.ScatterSpawnPack("zombie", 6, 3, magicPm, rarePm); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScatterSpawnPackRollsRarity(t *testing.T) {
	s := packSim(t, 31, 0, 1000) // rare guaranteed
	for _, a := range s.W.Actors {
		if a.Rarity != core.RarityRare {
			t.Fatalf("rarity = %v, want rare", a.Rarity)
		}
		if len(a.Mods) != 2 || a.Mods[0].ID == a.Mods[1].ID {
			t.Fatalf("rare mods = %v, want 2 distinct", a.Mods)
		}
		if a.Life != a.MaxLife() {
			t.Errorf("pools not refilled after mods: life %v != max %v", a.Life, a.MaxLife())
		}
	}

	s = packSim(t, 31, 1000, 0) // magic guaranteed
	for _, a := range s.W.Actors {
		if a.Rarity != core.RarityMagic || len(a.Mods) != 1 {
			t.Fatalf("magic monster got rarity %v, %d mods", a.Rarity, len(a.Mods))
		}
	}
}

// TestScatterSpawnPackZeroChancesIsLeveledSpawn: with no rarity pressure
// the pack path consumes exactly the ScatterSpawnLeveled stream and builds
// the identical world — this is what keeps the golden dungeon valid.
func TestScatterSpawnPackZeroChancesIsLeveledSpawn(t *testing.T) {
	a := sim.New(content.DB(), 33)
	a.GenerateMap(space.MapSpec{Width: 40, Height: 40, Rooms: 7})
	if err := a.ScatterSpawnLeveled("zombie", 5, 2); err != nil {
		t.Fatal(err)
	}
	b := sim.New(content.DB(), 33)
	b.GenerateMap(space.MapSpec{Width: 40, Height: 40, Rooms: 7})
	if err := b.ScatterSpawnPack("zombie", 5, 2, 0, 0); err != nil {
		t.Fatal(err)
	}
	if a.W.Hash() != b.W.Hash() {
		t.Fatal("zero-chance pack spawn diverged from leveled spawn")
	}
}

// TestRareLootRolls: dummy_drops always drops (DropChance 1.0), so drop
// counts are exact — a normal dummy pays 1 attempt, a rare one 3.
func TestRareLootRolls(t *testing.T) {
	drops := func(rarity core.Rarity) int {
		s := sim.New(content.DB(), 40)
		id := mustSpawn(t, s, "training_dummy", 0, 0)
		a := s.W.ActorByID(id)
		if rarity != core.RarityNormal {
			a.ApplyMonsterMods(rarity, s.W.Content.MonsterMods[:2])
		}
		killer := mustSpawn(t, s, "player", 5000, 0)
		a.Dead = true
		s.W.Emit(core.Event{Kind: core.EvDeath, Actor: a.ID, Other: killer})
		items.RollLoot(s.W)
		return len(s.W.Drops)
	}
	if n := drops(core.RarityNormal); n != 1 {
		t.Fatalf("normal dummy dropped %d items, want exactly 1", n)
	}
	if n := drops(core.RarityRare); n != 3 {
		t.Fatalf("rare dummy dropped %d items, want exactly 3", n)
	}
}

// TestRaritySaveRoundTrip: rarity and mods survive save/load — same hash,
// same resolved defs.
func TestRaritySaveRoundTrip(t *testing.T) {
	s := packSim(t, 35, 300, 300)
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	w2, err := core.LoadWorld(s.W.Content, data)
	if err != nil {
		t.Fatal(err)
	}
	if s.W.Hash() != w2.Hash() {
		t.Fatal("hash changed across save/load of a rarity world")
	}
	for i, a := range s.W.Actors {
		b := w2.Actors[i]
		if a.Rarity != b.Rarity || len(a.Mods) != len(b.Mods) {
			t.Fatalf("actor %d rarity/mods lost in round trip", a.ID)
		}
		for j := range a.Mods {
			if a.Mods[j] != b.Mods[j] {
				t.Fatalf("actor %d mod %d resolved to a different def", a.ID, j)
			}
		}
	}
}
