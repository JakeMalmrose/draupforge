// Package items rolls loot: rarity, base selection, and affix generation
// against weighted pools. All randomness comes from the world's loot stream,
// so combat changes never reshuffle drops in a replay.
package items

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
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
		if table == nil || !w.RNGLoot.Chance(table.DropChance) {
			continue
		}
		item := RollItem(w, table)
		d := w.SpawnDrop(a.Pos, item)
		w.Emit(core.Event{Kind: core.EvDrop, Actor: a.ID, Other: d.ID, Note: item.Base.ID})
	}
}

// RollItem generates one item from a loot table: base, rarity, then affixes
// drawn from the weighted pool with no two from the same group.
func RollItem(w *core.World, table *core.LootTableDef) core.Item {
	baseID := table.Bases[w.RNGLoot.Uint64n(uint64(len(table.Bases)))]
	item := core.Item{ID: w.AllocID(), Base: w.Content.BaseItems[baseID]}

	switch roll := w.RNGLoot.Uint64n(100); {
	case roll < 50:
		item.Rarity = core.RarityNormal
	case roll < 85:
		item.Rarity = core.RarityMagic
	default:
		item.Rarity = core.RarityRare
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
			break // pool exhausted under constraints
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
