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

// Pickup moves a ground drop into the actor's inventory. Rejected when out
// of range or the bag is full.
func Pickup(w *core.World, a *core.Actor, dropID core.EntityID) bool {
	drop := w.DropByID(dropID)
	if drop == nil || !inPickupRange(a, drop) || !hasRoom(a) {
		return false
	}
	drop.Taken = true
	a.Inventory = append(a.Inventory, drop.Item)
	w.Emit(core.Event{Kind: core.EvPickup, Actor: a.ID, Other: drop.Item.ID, Note: drop.Item.Base.ID})
	return true
}

// Equip wears the item named by id, sourcing it from the inventory if it's
// there, else from a ground drop in pickup range. A displaced item goes to
// the inventory, or the ground if the bag is full.
func Equip(w *core.World, a *core.Actor, id core.EntityID) bool {
	var item core.Item
	if idx := inventoryIndex(a, id); idx >= 0 {
		item = a.Inventory[idx]
		a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
	} else if drop := w.DropByID(id); drop != nil && inPickupRange(a, drop) {
		item = drop.Item
		drop.Taken = true
	} else {
		return false
	}

	slot := chooseSlot(a, item.Base.Slot)
	if old := a.Equipment[slot]; old != nil {
		a.Sheet.RemoveSource(uint64(old.ID))
		if hasRoom(a) {
			a.Inventory = append(a.Inventory, *old)
		} else {
			w.SpawnDrop(a.Pos, *old)
		}
	}

	equipped := item
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
	w.Emit(core.Event{Kind: core.EvEquip, Actor: a.ID, Other: item.ID, Note: item.Base.ID})
	return true
}

// Unequip moves an equipped item into the inventory. Rejected if the bag is
// full — dropping gear on the ground should be an explicit choice, not a
// side effect.
func Unequip(w *core.World, a *core.Actor, itemID core.EntityID) bool {
	for slot := core.EquipSlot(0); slot < core.EquipSlotCount; slot++ {
		item := a.Equipment[slot]
		if item == nil || item.ID != itemID {
			continue
		}
		if !hasRoom(a) {
			return false
		}
		a.Sheet.RemoveSource(uint64(item.ID))
		a.Inventory = append(a.Inventory, *item)
		a.Equipment[slot] = nil
		clampPools(a)
		w.Emit(core.Event{Kind: core.EvUnequip, Actor: a.ID, Other: item.ID, Note: item.Base.ID})
		return true
	}
	return false
}

// DropItem moves an inventory item to the ground at the actor's feet.
func DropItem(w *core.World, a *core.Actor, itemID core.EntityID) bool {
	idx := inventoryIndex(a, itemID)
	if idx < 0 {
		return false
	}
	item := a.Inventory[idx]
	a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
	d := w.SpawnDrop(a.Pos, item)
	w.Emit(core.Event{Kind: core.EvDrop, Actor: a.ID, Other: d.ID, Note: item.Base.ID})
	return true
}

func inPickupRange(a *core.Actor, d *core.Drop) bool {
	return space.Dist(a.Pos, d.Pos) <= PickupRange
}

func hasRoom(a *core.Actor) bool {
	return len(a.Inventory) < a.Def.InventorySize
}

func inventoryIndex(a *core.Actor, id core.EntityID) int {
	for i := range a.Inventory {
		if a.Inventory[i].ID == id {
			return i
		}
	}
	return -1
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
