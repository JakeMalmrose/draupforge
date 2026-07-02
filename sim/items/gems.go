package items

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// RollUncutGem creates an uncut gem item with its draft of three distinct
// choices pre-rolled from the loot stream — cutting later is deterministic,
// the drop was the RNG. Skill gems carry the level they were found at.
func RollUncutGem(w *core.World, support bool, level int) core.Item {
	item := core.Item{ID: w.AllocID(), Gem: &core.UncutGem{Support: support}}
	if support {
		for _, i := range rollDraft(w, len(w.Content.Supports)) {
			item.Gem.Choices = append(item.Gem.Choices, w.Content.Supports[i].ID)
		}
		return item
	}
	if level < 1 {
		level = 1
	}
	if level > core.MaxGemLevel {
		level = core.MaxGemLevel
	}
	item.Gem.Level = level
	for _, i := range rollDraft(w, len(w.Content.Cuttable)) {
		item.Gem.Choices = append(item.Gem.Choices, w.Content.Cuttable[i].ID)
	}
	return item
}

// rollDraft draws GemDraftSize distinct indices in [0, n). Content asserts
// n >= GemDraftSize. Fixed consumption: exactly three loot-stream draws.
func rollDraft(w *core.World, n int) [core.GemDraftSize]int {
	a := int(w.RNGLoot.Uint64n(uint64(n)))
	b := int(w.RNGLoot.Uint64n(uint64(n - 1)))
	if b >= a {
		b++
	}
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	c := int(w.RNGLoot.Uint64n(uint64(n - 2)))
	if c >= lo {
		c++
	}
	if c >= hi {
		c++
	}
	return [core.GemDraftSize]int{a, b, c}
}

// CutSkill consumes an uncut skill gem from the bag, cutting draft choice
// `choice` as a new gem at the drop's level. At the skill-gem cap the
// command must name a gem to replace — the old gem, its sockets, and its
// supports are destroyed. Duplicate skills are rejected.
func CutSkill(w *core.World, a *core.Actor, itemID core.EntityID, choice int, replace bool, gemIdx int) bool {
	idx := inventoryIndex(a, itemID)
	if idx < 0 {
		return false
	}
	item := a.Inventory[idx]
	if item.Gem == nil || item.Gem.Support || choice < 0 || choice >= len(item.Gem.Choices) {
		return false
	}
	sk := w.Content.Skills[item.Gem.Choices[choice]]
	if sk == nil || a.GemForSkill(sk.ID) != nil {
		return false
	}
	if replace {
		if gemIdx < 0 || gemIdx >= len(a.Gems) {
			return false
		}
	} else if len(a.Gems) >= core.MaxSkillGems {
		return false
	}
	level := item.Gem.Level
	a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
	if replace {
		a.Gems[gemIdx] = core.Gem{
			Skill:    sk,
			Level:    level,
			Sockets:  core.GemStartSockets,
			Supports: make([]*core.SupportDef, core.GemStartSockets),
		}
	} else {
		a.GrantGem(sk, level)
	}
	w.Emit(core.Event{Kind: core.EvGem, Actor: a.ID, Note: "cut:" + sk.ID, Amount: fm.FromInt(int64(level))})
	return true
}

// LevelGem consumes an uncut skill gem to raise an existing gem to the
// drop's level — only ever upward; a lower-level drop is for cutting.
func LevelGem(w *core.World, a *core.Actor, itemID core.EntityID, gemIdx int) bool {
	idx := inventoryIndex(a, itemID)
	if idx < 0 {
		return false
	}
	item := a.Inventory[idx]
	if item.Gem == nil || item.Gem.Support || gemIdx < 0 || gemIdx >= len(a.Gems) {
		return false
	}
	g := &a.Gems[gemIdx]
	if item.Gem.Level <= g.Level {
		return false
	}
	g.Level = item.Gem.Level
	a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
	w.Emit(core.Event{Kind: core.EvGem, Actor: a.ID, Note: "level:" + g.Skill.ID, Amount: fm.FromInt(int64(g.Level))})
	return true
}

// CutSupport consumes an uncut support gem, socketing draft choice `choice`
// into the named gem and socket. The skill's tags must satisfy the
// support's requirements; a support already in another of the gem's sockets
// is rejected; socketing over an occupied socket destroys the old support.
func CutSupport(w *core.World, a *core.Actor, itemID core.EntityID, choice, gemIdx, socket int) bool {
	idx := inventoryIndex(a, itemID)
	if idx < 0 {
		return false
	}
	item := a.Inventory[idx]
	if item.Gem == nil || !item.Gem.Support || choice < 0 || choice >= len(item.Gem.Choices) {
		return false
	}
	def := w.Content.Support(item.Gem.Choices[choice])
	if def == nil || gemIdx < 0 || gemIdx >= len(a.Gems) {
		return false
	}
	g := &a.Gems[gemIdx]
	if socket < 0 || socket >= g.Sockets {
		return false
	}
	if !g.Skill.Tags.ContainsAll(def.Requires) || g.HasSupport(def.ID) {
		return false
	}
	a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
	g.Supports[socket] = def
	w.Emit(core.Event{Kind: core.EvGem, Actor: a.ID, Note: "support:" + def.ID + ":" + g.Skill.ID})
	return true
}

// AddSocket spends one jeweller orb to grow a gem's socket count, capped at
// MaxGemSockets.
func AddSocket(w *core.World, a *core.Actor, gemIdx int) bool {
	if a.Orbs[core.OrbJeweller] <= 0 || gemIdx < 0 || gemIdx >= len(a.Gems) {
		return false
	}
	g := &a.Gems[gemIdx]
	if g.Sockets >= core.MaxGemSockets {
		return false
	}
	a.Orbs[core.OrbJeweller]--
	g.Sockets++
	g.Supports = append(g.Supports, nil)
	w.Emit(core.Event{Kind: core.EvGem, Actor: a.ID, Note: "socket:" + g.Skill.ID, Amount: fm.FromInt(int64(g.Sockets))})
	return true
}
