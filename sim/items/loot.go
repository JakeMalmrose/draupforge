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
		// Currency first: one orb draw per kill with a live enemy killer.
		// Minion kills bank to the summoner (CreditFor), same as XP.
		if killer := w.ActorByID(ev.Other); killer != nil && !killer.Dead && killer.Team != a.Team {
			rollOrb(w, w.CreditFor(killer), a.Rarity)
		}
		// Uncut gems next: independent skill and support draws (a rare can
		// pay both), permilles scaled by the dier's rarity like orbs.
		// Zero-chance tables consume nothing.
		mult := rarityMult(a.Rarity)
		for _, g := range [2]struct {
			permille uint32
			support  bool
		}{{table.SkillGemPermille, false}, {table.SupportGemPermille, true}} {
			if g.permille == 0 {
				continue
			}
			if w.RNGLoot.Uint64n(1000) < uint64(g.permille)*mult {
				item := RollUncutGem(w, g.support, a.Level)
				d := w.SpawnDrop(a.Pos, item)
				w.Emit(core.Event{Kind: core.EvDrop, Actor: a.ID, Other: d.ID, Note: item.Name()})
			}
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
			// Item level is the dier's level (floor-scaled), gating which
			// affix tiers can appear — deeper kills drop better gear.
			item := RollItem(w, table, a.Level)
			d := w.SpawnDrop(a.Pos, item)
			w.Emit(core.Event{Kind: core.EvDrop, Actor: a.ID, Other: d.ID, Note: item.Base.ID})
		}
	}
}

// RollItem generates one item from a loot table. A unique check runs first
// (one draw when the table's UniquePermille is set, plus a uniform pick on
// success — the unique dictates its own base and mods, only the implicit
// still rolls); otherwise base, rarity (weighted per table), the base's
// implicit, then affixes drawn from the weighted pool with no two from the
// same group. RNG draw order (unique?, pick?, base, rarity, implicit,
// affixes) is replay-relevant — don't reorder.
func RollItem(w *core.World, table *core.LootTableDef, itemLevel int) core.Item {
	if itemLevel < 1 {
		itemLevel = 1
	}
	if table.UniquePermille > 0 && len(w.Content.Uniques) > 0 &&
		w.RNGLoot.Uint64n(1000) < uint64(table.UniquePermille) {
		u := w.Content.Uniques[w.RNGLoot.Uint64n(uint64(len(w.Content.Uniques)))]
		item := core.Item{
			ID: w.AllocID(), Base: w.Content.BaseItems[u.Base],
			Rarity: core.RarityUnique, Unique: u, ItemLevel: itemLevel,
		}
		if imp := item.Base.Implicit; imp != nil {
			item.Implicit = w.RNGLoot.Range(imp.Min, imp.Max)
		}
		return item
	}
	baseID := table.Bases[w.RNGLoot.Uint64n(uint64(len(table.Bases)))]
	item := core.Item{ID: w.AllocID(), Base: w.Content.BaseItems[baseID], ItemLevel: itemLevel}
	item.Rarity = rollRarity(w, table)
	if imp := item.Base.Implicit; imp != nil {
		item.Implicit = w.RNGLoot.Range(imp.Min, imp.Max)
	}

	fillAffixes(w, &item)
	return item
}

// fillAffixes rolls an item's affix block for its rarity — count draw,
// then weighted picks (loot stream). Any existing affixes are replaced:
// the chaos-orb reroll and the drop path share this.
func fillAffixes(w *core.World, item *core.Item) {
	item.Affixes = nil
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
		return
	}

	usedGroups := make(map[string]bool)
	kindCounts := [2]int{}
	for len(item.Affixes) < want {
		af := pickAffix(w, item.Base.Slot, item.ItemLevel, usedGroups, kindCounts, kindCap)
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
}

// Orb drop rates per kill, per mille, scaled by the dier's rarity (x2
// magic, x3 rare — matching the drop-attempt ladder). One combined draw
// decides which orb, if any. Open for tuning.
var orbPermille = [core.OrbCount]uint64{90, 30, 15, 40} // transmutation, alchemy, chaos, jeweller

// rarityMult is the shared drop-luck ladder: magic pays double, rare triple.
func rarityMult(r core.Rarity) uint64 {
	switch r {
	case core.RarityMagic:
		return 2
	case core.RarityRare:
		return 3
	}
	return 1
}

// rollOrb banks a currency drop straight to the killer — no ground
// entity; picking up shards of currency is friction, not fun. Consumes
// exactly one loot draw per eligible kill.
func rollOrb(w *core.World, killer *core.Actor, rarity core.Rarity) {
	mult := rarityMult(rarity)
	roll := w.RNGLoot.Uint64n(1000)
	for o := core.OrbKind(0); o < core.OrbCount; o++ {
		band := orbPermille[o] * mult
		if roll < band {
			killer.Orbs[o]++
			w.Emit(core.Event{
				Kind: core.EvOrb, Actor: killer.ID,
				Amount: fm.FromInt(int64(killer.Orbs[o])), Note: o.String(),
			})
			return
		}
		roll -= band
	}
}

// ApplyOrb spends one orb from the actor's wallet on an inventory item:
// transmutation upgrades normal to magic, alchemy normal to rare, chaos
// rerolls a rare. Equipped items can't be crafted (their mods live on the
// sheet); reports whether anything happened.
func ApplyOrb(w *core.World, a *core.Actor, orb core.OrbKind, itemID core.EntityID) bool {
	if orb >= core.OrbCount || a.Orbs[orb] <= 0 {
		return false
	}
	// Jewellers work on cut gems (CmdAddSocket), never bag items; uncut
	// gems have no affixes to craft.
	if orb == core.OrbJeweller {
		return false
	}
	var item *core.Item
	for i := range a.Inventory {
		if a.Inventory[i].ID == itemID {
			item = &a.Inventory[i]
		}
	}
	if item == nil || item.Gem != nil {
		return false
	}
	switch orb {
	case core.OrbTransmutation:
		if item.Rarity != core.RarityNormal {
			return false
		}
		item.Rarity = core.RarityMagic
	case core.OrbAlchemy:
		if item.Rarity != core.RarityNormal {
			return false
		}
		item.Rarity = core.RarityRare
	case core.OrbChaos:
		if item.Rarity != core.RarityRare {
			return false
		}
	}
	a.Orbs[orb]--
	fillAffixes(w, item)
	w.Emit(core.Event{
		Kind: core.EvOrb, Actor: a.ID, Other: item.ID,
		Note: orb.String() + ":" + item.Base.ID,
	})
	return true
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

// pickAffix does a weighted draw over the affixes still legal for this item
// at its level. Iterates the content slice in order — deterministic by
// construction.
func pickAffix(w *core.World, slot core.SlotFamily, itemLevel int, usedGroups map[string]bool, kindCounts [2]int, kindCap int) *core.AffixDef {
	var total uint64
	for _, af := range w.Content.Affixes {
		if legal(af, slot, itemLevel, usedGroups, kindCounts, kindCap) {
			total += uint64(af.Weight)
		}
	}
	if total == 0 {
		return nil
	}
	roll := w.RNGLoot.Uint64n(total)
	for _, af := range w.Content.Affixes {
		if !legal(af, slot, itemLevel, usedGroups, kindCounts, kindCap) {
			continue
		}
		if roll < uint64(af.Weight) {
			return af
		}
		roll -= uint64(af.Weight)
	}
	return nil // unreachable
}

func legal(af *core.AffixDef, slot core.SlotFamily, itemLevel int, usedGroups map[string]bool, kindCounts [2]int, kindCap int) bool {
	return af.AllowedOn(slot) && af.ILvl <= itemLevel && !usedGroups[af.Group] && kindCounts[af.Kind] < kindCap
}
