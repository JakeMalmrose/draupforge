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

// Equip wears the item named by id in the given slot (EquipAuto = first
// empty slot in family preference order), sourcing the item from the
// inventory if it's there, else from a ground drop in pickup range. The
// slot must accept the item's family — a ring goes in either ring slot and
// nowhere else. A displaced item goes to the inventory, or the ground if
// the bag is full.
func Equip(w *core.World, a *core.Actor, id core.EntityID, slot core.EquipSlot) bool {
	// Resolve and validate before moving anything, so a rejected equip
	// leaves the world untouched.
	var item core.Item
	idx := inventoryIndex(a, id)
	var drop *core.Drop
	if idx >= 0 {
		item = a.Inventory[idx]
	} else if drop = w.DropByID(id); drop != nil && inPickupRange(a, drop) {
		item = drop.Item
	} else {
		return false
	}
	if slot == core.EquipAuto {
		slot = chooseSlot(a, item.Base.Slot)
	} else if !slotAccepts(slot, item.Base.Slot) {
		return false
	}

	if idx >= 0 {
		a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
	} else {
		drop.Taken = true
	}

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
	GrantItemMods(a, &equipped)
	clampPools(a)
	w.Emit(core.Event{Kind: core.EvEquip, Actor: a.ID, Other: item.ID, Note: item.Base.ID})
	return true
}

// GrantItemMods adds an item's implicit and affix modifiers to an actor's
// sheet, sourced by the item's own ID (so RemoveSource(uint64(item.ID))
// cleanly strips them again on unequip). Equip calls this for a normal
// equip/swap; it's also exported for callers that place an item into
// a.Equipment directly — e.g. the server re-minting a character's gear at
// zone injection, where the item never passes through Equip's inventory/drop
// resolution.
func GrantItemMods(a *core.Actor, item *core.Item) {
	if imp := item.Base.Implicit; imp != nil {
		a.Sheet.Add(stats.Modifier{
			Stat:   imp.Stat,
			Layer:  imp.Layer,
			Value:  item.Implicit,
			Tags:   imp.Tags,
			Source: uint64(item.ID),
		})
	}
	for _, af := range item.Affixes {
		a.Sheet.Add(stats.Modifier{
			Stat:   af.Def.Stat,
			Layer:  af.Def.Layer,
			Value:  af.Value,
			Tags:   af.Def.Tags,
			Source: uint64(item.ID),
		})
	}
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

// slotAccepts reports whether a concrete slot can hold items of a family.
func slotAccepts(slot core.EquipSlot, f core.SlotFamily) bool {
	for _, s := range core.SlotsFor(f) {
		if s == slot {
			return true
		}
	}
	return false
}

// clampPools pulls current resources down to their (possibly shrunken)
// maxima after an equipment change. Growing a maximum never grants the
// difference as current — you equip a +life ring at the life you had.
func clampPools(a *core.Actor) {
	a.Life = fm.Min(a.Life, a.MaxLife())
	a.Mana = fm.Min(a.Mana, a.MaxMana())
	a.ES = fm.Min(a.ES, a.MaxES())
}
