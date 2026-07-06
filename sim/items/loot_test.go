package items_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
)

func TestRollItemConstraints(t *testing.T) {
	db := content.DB()
	table := db.LootTables["zombie_drops"]

	// Roll a pile of items across seeds and check every invariant.
	for seed := uint64(0); seed < 200; seed++ {
		w := core.NewWorld(db, seed)
		item := items.RollItem(w, table, 20)

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

	a := items.RollItem(core.NewWorld(db, 7), table, 20)
	b := items.RollItem(core.NewWorld(db, 7), table, 20)
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
		item := items.RollItem(w, table, 20)
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
		if r := items.RollItem(core.NewWorld(db, seed), always([3]uint32{1, 0, 0}), 20).Rarity; r != core.RarityNormal {
			t.Fatalf("seed %d: all-normal weights rolled %v", seed, r)
		}
		if r := items.RollItem(core.NewWorld(db, seed), always([3]uint32{0, 0, 1}), 20).Rarity; r != core.RarityRare {
			t.Fatalf("seed %d: all-rare weights rolled %v", seed, r)
		}
		// All-zero weights (content forgot) degrade to normal instead of panicking.
		if r := items.RollItem(core.NewWorld(db, seed), always([3]uint32{}), 20).Rarity; r != core.RarityNormal {
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
	item := items.RollItem(w, db.LootTables["starved"], 20)
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

// TestAffixesRespectSlotFamilies: every affix rolled on any base is legal
// for that base's family — and the marquee case: boots roll move speed,
// nothing else does.
func TestAffixesRespectSlotFamilies(t *testing.T) {
	db := content.DB()
	rareOnly := func(base string) *core.LootTableDef {
		return &core.LootTableDef{
			ID: "test_" + base, DropChance: fm.One,
			RarityWeights: [3]uint32{0, 0, 1},
			Bases:         []string{base},
		}
	}
	sawMoveSpeedOnBoots := false
	for base, def := range db.BaseItems {
		w := core.NewWorld(db, 99)
		table := rareOnly(base)
		for i := 0; i < 150; i++ {
			item := items.RollItem(w, table, 20)
			for _, af := range item.Affixes {
				if !af.Def.AllowedOn(def.Slot) {
					t.Fatalf("%s rolled %s, not allowed on family %d", base, af.Def.ID, def.Slot)
				}
				if af.Def.ID == "increased_move_speed" {
					if base != "leather_boots" {
						t.Fatalf("move speed rolled on %s", base)
					}
					sawMoveSpeedOnBoots = true
				}
			}
		}
	}
	if !sawMoveSpeedOnBoots {
		t.Error("150 rare boots never rolled move speed — pool wiring suspect")
	}
}

// TestItemLevelGatesTiers: low-level drops never carry an affix tier gated
// above their item level, and deep drops can — so depth means better gear.
func TestItemLevelGatesTiers(t *testing.T) {
	db := content.DB()
	// A body-armour-only table maximizes the tiered life/armour affixes.
	table := &core.LootTableDef{
		ID: "t", DropChance: fm.One, Bases: []string{"leather_vest"},
		RarityWeights: [3]uint32{0, 0, 1}, // always rare — six affixes
	}
	gated := map[string]int{
		"flat_life_greater": 5, "flat_life_grand": 12,
		"flat_armour_greater": 5, "flat_armour_grand": 12,
	}

	sawLowGated, sawHighTier := false, false
	for seed := uint64(0); seed < 400; seed++ {
		low := items.RollItem(core.NewWorld(db, seed), table, 1)
		if low.ItemLevel != 1 {
			t.Fatalf("item level = %d, want 1", low.ItemLevel)
		}
		for _, af := range low.Affixes {
			if min, ok := gated[af.Def.ID]; ok && min > 1 {
				t.Fatalf("ilvl-1 item rolled %s (gated at ilvl %d)", af.Def.ID, min)
			}
			if af.Def.ID == "flat_life" {
				sawLowGated = true // the base tier is available low
			}
		}
		high := items.RollItem(core.NewWorld(db, seed), table, 20)
		for _, af := range high.Affixes {
			if af.Def.ID == "flat_life_grand" || af.Def.ID == "flat_armour_grand" {
				sawHighTier = true
			}
		}
	}
	if !sawLowGated {
		t.Fatal("never rolled the base life tier at ilvl 1 — pool starved?")
	}
	if !sawHighTier {
		t.Fatal("400 ilvl-20 rares never rolled a grand tier — gate never opens?")
	}
}

func TestRollValuesLandOnStep(t *testing.T) {
	db := content.DB()
	table := db.LootTables["dummy_drops"]

	// Every content affix and implicit declares a Step; rolled values must
	// land exactly on the Min + k·Step lattice so tooltips never have to
	// round. Rolled across many seeds to cover the whole pool.
	for seed := uint64(0); seed < 300; seed++ {
		w := core.NewWorld(db, seed)
		item := items.RollItem(w, table, 20)

		if imp := item.Base.Implicit; imp != nil && item.Implicit != 0 {
			if imp.Step <= 0 {
				t.Fatalf("implicit %s has no Step", imp.ID)
			}
			if (item.Implicit-imp.Min)%imp.Step != 0 {
				t.Errorf("seed %d: implicit %s rolled %d off the %d-step lattice from %d",
					seed, imp.ID, item.Implicit, imp.Step, imp.Min)
			}
		}
		for _, af := range item.Affixes {
			if af.Def.Step <= 0 {
				t.Fatalf("affix %s has no Step", af.Def.ID)
			}
			if (af.Value-af.Def.Min)%af.Def.Step != 0 {
				t.Errorf("seed %d: affix %s rolled %d off the %d-step lattice from %d",
					seed, af.Def.ID, af.Value, af.Def.Step, af.Def.Min)
			}
		}
	}
}
