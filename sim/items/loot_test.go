package items_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
	"github.com/JakeMalmrose/draupforge/sim/items"
)

func TestRollItemConstraints(t *testing.T) {
	db := content.DB()
	table := db.LootTables["zombie_drops"]

	// Roll a pile of items across seeds and check every invariant.
	for seed := uint64(0); seed < 200; seed++ {
		w := core.NewWorld(db, seed)
		item := items.RollItem(w, table)

		if item.Base == nil {
			t.Fatal("item rolled with nil base")
		}

		var prefixes, suffixes int
		groups := map[string]bool{}
		for _, af := range item.Affixes {
			if af.Value < af.Def.Min || af.Value > af.Def.Max {
				t.Errorf("seed %d: affix %s rolled %d outside [%d, %d]",
					seed, af.Def.ID, af.Value, af.Def.Min, af.Def.Max)
			}
			if groups[af.Def.Group] {
				t.Errorf("seed %d: duplicate affix group %q", seed, af.Def.Group)
			}
			groups[af.Def.Group] = true
			if af.Def.Kind == core.Prefix {
				prefixes++
			} else {
				suffixes++
			}
		}

		switch item.Rarity {
		case core.RarityNormal:
			if len(item.Affixes) != 0 {
				t.Errorf("seed %d: normal item with %d affixes", seed, len(item.Affixes))
			}
		case core.RarityMagic:
			if len(item.Affixes) < 1 || prefixes > 1 || suffixes > 1 {
				t.Errorf("seed %d: magic item broke caps: %d affixes, %dp/%ds",
					seed, len(item.Affixes), prefixes, suffixes)
			}
		case core.RarityRare:
			if len(item.Affixes) < 4 || prefixes > 3 || suffixes > 3 {
				t.Errorf("seed %d: rare item broke caps: %d affixes, %dp/%ds",
					seed, len(item.Affixes), prefixes, suffixes)
			}
		}
	}
}

func TestRollItemDeterministic(t *testing.T) {
	db := content.DB()
	table := db.LootTables["zombie_drops"]

	a := items.RollItem(core.NewWorld(db, 7), table)
	b := items.RollItem(core.NewWorld(db, 7), table)
	if a.Base.ID != b.Base.ID || a.Rarity != b.Rarity || len(a.Affixes) != len(b.Affixes) {
		t.Fatal("same seed rolled different items")
	}
	for i := range a.Affixes {
		if a.Affixes[i].Def.ID != b.Affixes[i].Def.ID || a.Affixes[i].Value != b.Affixes[i].Value {
			t.Fatal("same seed rolled different affixes")
		}
	}
}

func TestRollItemImplicit(t *testing.T) {
	db := content.DB()
	table := db.LootTables["dummy_drops"] // full base list, every base has an implicit

	for seed := uint64(0); seed < 100; seed++ {
		w := core.NewWorld(db, seed)
		item := items.RollItem(w, table)
		imp := item.Base.Implicit
		if imp == nil {
			t.Fatalf("seed %d: base %s has no implicit — every v1 base should", seed, item.Base.ID)
		}
		if item.Implicit < imp.Min || item.Implicit > imp.Max {
			t.Errorf("seed %d: %s implicit rolled %d outside [%d, %d]",
				seed, item.Base.ID, item.Implicit, imp.Min, imp.Max)
		}
	}
}

func TestRollRarityWeights(t *testing.T) {
	db := content.DB()
	always := func(weights [3]uint32) *core.LootTableDef {
		return &core.LootTableDef{ID: "t", Bases: []string{"iron_ring"}, RarityWeights: weights}
	}
	for seed := uint64(0); seed < 50; seed++ {
		if r := items.RollItem(core.NewWorld(db, seed), always([3]uint32{1, 0, 0})).Rarity; r != core.RarityNormal {
			t.Fatalf("seed %d: all-normal weights rolled %v", seed, r)
		}
		if r := items.RollItem(core.NewWorld(db, seed), always([3]uint32{0, 0, 1})).Rarity; r != core.RarityRare {
			t.Fatalf("seed %d: all-rare weights rolled %v", seed, r)
		}
		// All-zero weights (content forgot) degrade to normal instead of panicking.
		if r := items.RollItem(core.NewWorld(db, seed), always([3]uint32{})).Rarity; r != core.RarityNormal {
			t.Fatalf("seed %d: zero weights rolled %v", seed, r)
		}
	}
}

func TestRollItemStarvedPoolEmitsEvent(t *testing.T) {
	// One legal affix but rares want 4–6: the roll must come up short and
	// say so via EvLootStarved.
	full := content.DB()
	db := &core.ContentDB{
		BaseItems: full.BaseItems,
		Affixes:   full.Affixes[:1],
		LootTables: map[string]*core.LootTableDef{
			"starved": {ID: "starved", Bases: []string{"iron_ring"}, RarityWeights: [3]uint32{0, 0, 1}},
		},
	}
	w := core.NewWorld(db, 3)
	item := items.RollItem(w, db.LootTables["starved"])
	if len(item.Affixes) != 1 {
		t.Fatalf("starved rare rolled %d affixes, want 1", len(item.Affixes))
	}
	for _, ev := range w.Events() {
		if ev.Kind == core.EvLootStarved && ev.Other == item.ID && ev.Note == item.Base.ID {
			return
		}
	}
	t.Fatal("no EvLootStarved event for a roll that came up short")
}
