package sim_test

// The Forge and the deeper crafting ladder: regal/exalt/annulment/scouring
// orbs, and melt-to-shards / shards-to-orbs — the deterministic half of the
// economy. Command-driven loot-RNG consumption only.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// giveItem hands the actor a hand-built bag item of the given rarity with
// n rolled affixes drawn through the real pool.
func giveItem(t *testing.T, s *sim.Sim, a *core.Actor, rarity core.Rarity) core.EntityID {
	t.Helper()
	base := s.W.Content.BaseItems["rusty_sword"]
	if base == nil {
		t.Fatal("no rusty_sword base in content")
	}
	item := core.Item{ID: s.W.AllocID(), Base: base, Rarity: rarity, ItemLevel: 10}
	a.Inventory = append(a.Inventory, item)
	return item.ID
}

func forgeBagItem(a *core.Actor, id core.EntityID) *core.Item {
	for i := range a.Inventory {
		if a.Inventory[i].ID == id {
			return &a.Inventory[i]
		}
	}
	return nil
}

func TestRegalKeepsAffixesAndAddsOne(t *testing.T) {
	s := sim.New(content.DB(), 71)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	id := giveItem(t, s, a, core.RarityNormal)
	a.Orbs[core.OrbTransmutation] = 1
	a.Orbs[core.OrbRegal] = 1

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbTransmutation, TargetID: id}})
	it := forgeBagItem(a, id)
	before := make([]string, 0, len(it.Affixes))
	for _, ra := range it.Affixes {
		before = append(before, ra.Def.ID)
	}

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbRegal, TargetID: id}})
	it = forgeBagItem(a, id)
	if it.Rarity != core.RarityRare {
		t.Fatalf("regal left rarity %v, want rare", it.Rarity)
	}
	if len(it.Affixes) != len(before)+1 {
		t.Fatalf("regal affix count = %d, want %d", len(it.Affixes), len(before)+1)
	}
	for i, id := range before {
		if it.Affixes[i].Def.ID != id {
			t.Errorf("regal disturbed existing affix %d: %s -> %s", i, id, it.Affixes[i].Def.ID)
		}
	}
	if a.Orbs[core.OrbRegal] != 0 {
		t.Error("regal not spent")
	}
}

func TestExaltAnnulScour(t *testing.T) {
	s := sim.New(content.DB(), 72)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	id := giveItem(t, s, a, core.RarityNormal)
	a.Orbs[core.OrbAlchemy] = 1
	a.Orbs[core.OrbExalt] = 1
	a.Orbs[core.OrbAnnulment] = 1
	a.Orbs[core.OrbScouring] = 1

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbAlchemy, TargetID: id}})
	n := len(forgeBagItem(a, id).Affixes)

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbExalt, TargetID: id}})
	if got := len(forgeBagItem(a, id).Affixes); got != n+1 && n < 6 {
		t.Fatalf("exalt affixes = %d, want %d", got, n+1)
	}

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbAnnulment, TargetID: id}})
	if got := len(forgeBagItem(a, id).Affixes); got != n {
		t.Fatalf("annulment affixes = %d, want %d", got, n)
	}

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbScouring, TargetID: id}})
	it := forgeBagItem(a, id)
	if it.Rarity != core.RarityNormal || len(it.Affixes) != 0 {
		t.Fatalf("scouring left %v with %d affixes", it.Rarity, len(it.Affixes))
	}
}

func TestForgeMeltAndBuy(t *testing.T) {
	s := sim.New(content.DB(), 73)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	id := giveItem(t, s, a, core.RarityRare)
	bag := len(a.Inventory)

	s.Step([]core.Command{{Actor: player, Kind: core.CmdForgeMelt, TargetID: id}})
	if len(a.Inventory) != bag-1 {
		t.Fatal("melt did not consume the item")
	}
	if got := a.Shards; got != core.MeltShards(core.RarityRare) {
		t.Fatalf("rare melt paid %d shards, want %d", got, core.MeltShards(core.RarityRare))
	}

	// Buy: refused while short, honored at price.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdForgeBuy, Orb: core.OrbExalt}})
	if a.Orbs[core.OrbExalt] != 0 {
		t.Fatal("bought an exalt without the shards")
	}
	a.Shards = core.OrbShardPrice[core.OrbTransmutation] + 1
	s.Step([]core.Command{{Actor: player, Kind: core.CmdForgeBuy, Orb: core.OrbTransmutation}})
	if a.Orbs[core.OrbTransmutation] != 1 || a.Shards != 1 {
		t.Fatalf("buy: orbs %d shards %d, want 1 and 1", a.Orbs[core.OrbTransmutation], a.Shards)
	}
}

func TestNewOrbsRefuseUniquesButForgeMelts(t *testing.T) {
	s := sim.New(content.DB(), 74)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	u := s.W.Content.Uniques[0]
	item := core.Item{ID: s.W.AllocID(), Base: s.W.Content.BaseItems[u.Base], Rarity: core.RarityUnique, Unique: u, ItemLevel: 10}
	a.Inventory = append(a.Inventory, item)
	a.Orbs[core.OrbScouring] = 1
	a.Orbs[core.OrbAnnulment] = 1

	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbScouring, TargetID: item.ID}})
	s.Step([]core.Command{{Actor: player, Kind: core.CmdApplyOrb, Orb: core.OrbAnnulment, TargetID: item.ID}})
	if it := forgeBagItem(a, item.ID); it == nil || it.Rarity != core.RarityUnique {
		t.Fatal("an orb touched a unique")
	}
	if a.Orbs[core.OrbScouring] != 1 || a.Orbs[core.OrbAnnulment] != 1 {
		t.Error("orbs were spent on a refused unique")
	}

	// But the Forge WILL melt one — for the top shard price.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdForgeMelt, TargetID: item.ID}})
	if got := a.Shards; got != core.MeltShards(core.RarityUnique) {
		t.Fatalf("unique melt paid %d, want %d", got, core.MeltShards(core.RarityUnique))
	}
}

func TestShardsTransferAndSave(t *testing.T) {
	s := sim.New(content.DB(), 75)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	a.Shards = 42
	s.Step(nil)

	blob, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	w2, err := core.LoadWorld(content.DB(), blob)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w2.Hash(), s.W.Hash(); got != want {
		t.Fatalf("hash mismatch after save/load: %x vs %x", got, want)
	}
	if w2.ActorByID(player).Shards != 42 {
		t.Fatal("shards did not survive save/load")
	}

	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 76)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if b.Shards != 42 {
		t.Fatal("shards did not transfer with the character")
	}
}
