// Package items rolls loot: rarity, base selection, and affix generation
// against weighted pools. All randomness comes from the world's loot stream,
// so combat changes never reshuffle drops in a replay.
package items

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// Affix count rules per rarity: magic items roll 1–2 affixes capped at one
// prefix and one suffix; rares roll 4–6 capped at three and three.
const (
	magicAffixCap = 1
	rareAffixCap  = 3
)

// RollLoot scans this tick's death events and rolls drops for any dier with
// a loot table. Runs after combat/DoT phases so every death is visible;
// the corpse is still addressable because compaction happens at tick end.
func RollLoot(w *core.World) {
	deaths := w.Events()
	for _, ev := range deaths {
		if ev.Kind != core.EvDeath {
			continue
		}
		a := w.ActorByID(ev.Actor)
		if a == nil || a.Def.LootTable == "" {
			continue
		}
		table := w.Content.LootTables[a.Def.LootTable]
		if table == nil {
			continue
		}
		// Rarity pays in drop attempts — magic 2, rare 3 — each gated by
		// the table's chance independently. Consumption is keyed off the
		// dier's rarity (world state, hashed), so replays stay aligned;
		// normal monsters consume exactly what they always did.
		rolls := 1
		switch a.Rarity {
		case core.RarityMagic:
			rolls = 2
		case core.RarityRare:
			rolls = 3
		}
		for i := 0; i < rolls; i++ {
			if !w.RNGLoot.Chance(table.DropChance) {
				continue
			}
			item := RollItem(w, table)
			d := w.SpawnDrop(a.Pos, item)
			w.Emit(core.Event{Kind: core.EvDrop, Actor: a.ID, Other: d.ID, Note: item.Base.ID})
		}
	}
}

// RollItem generates one item from a loot table: base, rarity (weighted per
// table), the base's implicit, then affixes drawn from the weighted pool
// with no two from the same group. RNG draw order (base, rarity, implicit,
// affixes) is replay-relevant — don't reorder.
func RollItem(w *core.World, table *core.LootTableDef) core.Item {
	baseID := table.Bases[w.RNGLoot.Uint64n(uint64(len(table.Bases)))]
	item := core.Item{ID: w.AllocID(), Base: w.Content.BaseItems[baseID]}
	item.Rarity = rollRarity(w, table)
	if imp := item.Base.Implicit; imp != nil {
		item.Implicit = w.RNGLoot.Range(imp.Min, imp.Max)
	}

	var want int
	kindCap := 0
	switch item.Rarity {
	case core.RarityMagic:
		want = 1 + int(w.RNGLoot.Uint64n(2)) // 1–2
		kindCap = magicAffixCap
	case core.RarityRare:
		want = 4 + int(w.RNGLoot.Uint64n(3)) // 4–6
		kindCap = rareAffixCap
	default:
		return item
	}

	usedGroups := make(map[string]bool)
	kindCounts := [2]int{}
	for len(item.Affixes) < want {
		af := pickAffix(w, usedGroups, kindCounts, kindCap)
		if af == nil {
			// Pool exhausted under constraints: a starved table is a content
			// bug, so make it visible instead of silently rolling short.
			w.Emit(core.Event{
				Kind:   core.EvLootStarved,
				Other:  item.ID,
				Amount: fm.FromInt(int64(want - len(item.Affixes))),
				Note:   item.Base.ID,
			})
			break
		}
		usedGroups[af.Group] = true
		kindCounts[af.Kind]++
		item.Affixes = append(item.Affixes, core.RolledAffix{
			Def:   af,
			Value: w.RNGLoot.Range(af.Min, af.Max),
		})
	}
	return item
}

// rollRarity draws normal/magic/rare from the table's weights. An all-zero
// table (content forgot to set it) degrades to normal-only.
func rollRarity(w *core.World, table *core.LootTableDef) core.Rarity {
	var total uint64
	for _, wt := range table.RarityWeights {
		total += uint64(wt)
	}
	if total == 0 {
		return core.RarityNormal
	}
	roll := w.RNGLoot.Uint64n(total)
	for r, wt := range table.RarityWeights {
		if roll < uint64(wt) {
			return core.Rarity(r)
		}
		roll -= uint64(wt)
	}
	return core.RarityNormal // unreachable
}

// pickAffix does a weighted draw over the affixes still legal for this item.
// Iterates the content slice in order — deterministic by construction.
func pickAffix(w *core.World, usedGroups map[string]bool, kindCounts [2]int, kindCap int) *core.AffixDef {
	var total uint64
	for _, af := range w.Content.Affixes {
		if legal(af, usedGroups, kindCounts, kindCap) {
			total += uint64(af.Weight)
		}
	}
	if total == 0 {
		return nil
	}
	roll := w.RNGLoot.Uint64n(total)
	for _, af := range w.Content.Affixes {
		if !legal(af, usedGroups, kindCounts, kindCap) {
			continue
		}
		if roll < uint64(af.Weight) {
			return af
		}
		roll -= uint64(af.Weight)
	}
	return nil // unreachable
}

func legal(af *core.AffixDef, usedGroups map[string]bool, kindCounts [2]int, kindCap int) bool {
	return !usedGroups[af.Group] && kindCounts[af.Kind] < kindCap
}
