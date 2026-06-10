package items

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// PickupRange is how far an actor can reach to grab a ground drop, measured
// center to center.
var PickupRange = fm.FromInt(2)

// Equip validates and executes a pickup-and-equip: the drop leaves the
// ground, its affixes land on the actor's sheet, and any displaced item
// falls at the actor's feet. Returns false if the command was invalid
// (out of range, missing drop) — the sim drops it silently like any other
// rejected command.
func Equip(w *core.World, a *core.Actor, dropID core.EntityID) bool {
	drop := w.DropByID(dropID)
	if drop == nil {
		return false
	}
	if space.Dist(a.Pos, drop.Pos) > PickupRange {
		return false
	}

	item := drop.Item
	slot := chooseSlot(a, item.Base.Slot)

	if old := a.Equipment[slot]; old != nil {
		a.Sheet.RemoveSource(uint64(old.ID))
		w.SpawnDrop(a.Pos, *old)
	}

	equipped := item // actor owns its own copy
	a.Equipment[slot] = &equipped
	for _, af := range item.Affixes {
		a.Sheet.Add(stats.Modifier{
			Stat:   af.Def.Stat,
			Layer:  af.Def.Layer,
			Value:  af.Value,
			Tags:   af.Def.Tags,
			Source: uint64(item.ID),
		})
	}
	clampPools(a)

	drop.Taken = true
	w.Emit(core.Event{Kind: core.EvEquip, Actor: a.ID, Other: item.ID, Note: item.Base.ID})
	return true
}

// chooseSlot resolves a slot family to a concrete slot: first empty slot in
// preference order, else the first slot (replace).
func chooseSlot(a *core.Actor, f core.SlotFamily) core.EquipSlot {
	candidates := core.SlotsFor(f)
	for _, s := range candidates {
		if a.Equipment[s] == nil {
			return s
		}
	}
	return candidates[0]
}

// clampPools pulls current resources down to their (possibly shrunken)
// maxima after an equipment change. Growing a maximum never grants the
// difference as current — you equip a +life ring at the life you had.
func clampPools(a *core.Actor) {
	a.Life = fm.Min(a.Life, a.MaxLife())
	a.Mana = fm.Min(a.Mana, a.MaxMana())
	a.ES = fm.Min(a.ES, a.MaxES())
}
