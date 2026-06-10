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
