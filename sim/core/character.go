package core

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// Character is the durable, world-independent projection of a player: the
// subset of an Actor that crosses zone boundaries (DESIGN §14). It carries
// identity, progression, and items — but no zone-local state (position,
// action, buffs, DoTs, pools). The stat sheet rebuilds from def + level +
// equipment at injection. It is host-layer transient data during a transfer,
// never world state, so it ships no save/hash coverage of its own.
type Character struct {
	Def       string
	Level     int
	XP        int64
	Equipment [EquipSlotCount]*Item
	Inventory []Item
}

// ExtractCharacter reduces an Actor to its durable character state. Items are
// copied by value (affixes deep-copied) so the result shares nothing mutable
// with the world the actor lived in.
func ExtractCharacter(a *Actor) Character {
	ch := Character{Def: a.Def.ID, Level: a.Level, XP: a.XP}
	for slot, item := range a.Equipment {
		if item != nil {
			c := copyItem(*item)
			ch.Equipment[slot] = &c
		}
	}
	for _, item := range a.Inventory {
		ch.Inventory = append(ch.Inventory, copyItem(item))
	}
	return ch
}

// InjectCharacter mints a fresh Actor for ch in this world: a new entity ID,
// re-minted item IDs (the old IDs were world-local and would collide here),
// and a stat sheet rebuilt from the def, the character's level, and its
// equipment. Pools enter full — a zone is a fresh start. Position is clamped
// to walkable ground on grid worlds.
func (w *World) InjectCharacter(ch Character, pos space.Vec2) (*Actor, error) {
	def := w.Content.Actors[ch.Def]
	if def == nil {
		return nil, fmt.Errorf("core: inject references unknown actor def %q", ch.Def)
	}
	if w.Grid != nil {
		if p, ok := w.Grid.NearestWalkable(pos); ok {
			pos = p
		}
	}
	a := w.SpawnActor(def, pos) // mints ID, applies def.Level growth, fills pools
	level := ch.Level
	if level < 1 {
		level = 1
	}
	a.SetLevel(level)
	a.XP = ch.XP

	for slot, item := range ch.Equipment {
		if item == nil {
			continue
		}
		eq := copyItem(*item)
		eq.ID = w.AllocID() // re-mint: item IDs double as sheet mod sources
		a.Equipment[slot] = &eq
		a.applyItemMods(&eq)
	}
	for _, item := range ch.Inventory {
		inv := copyItem(item)
		inv.ID = w.AllocID()
		a.Inventory = append(a.Inventory, inv)
	}

	// Rebuild done; enter the floor at full pools against the final maxima.
	a.Life = a.MaxLife()
	a.Mana = a.MaxMana()
	a.ES = a.MaxES()
	return a, nil
}

// applyItemMods grants an equipped item's implicit and affix modifiers to the
// actor's sheet, sourced by the item's ID. Mirrors the modifier half of
// items.Equip — kept here so character injection stays inside core (which
// can't import the items package).
func (a *Actor) applyItemMods(item *Item) {
	if imp := item.Base.Implicit; imp != nil {
		a.Sheet.Add(stats.Modifier{
			Stat: imp.Stat, Layer: imp.Layer, Value: item.Implicit,
			Tags: imp.Tags, Source: uint64(item.ID),
		})
	}
	for _, af := range item.Affixes {
		a.Sheet.Add(stats.Modifier{
			Stat: af.Def.Stat, Layer: af.Def.Layer, Value: af.Value,
			Tags: af.Def.Tags, Source: uint64(item.ID),
		})
	}
}

func copyItem(item Item) Item {
	out := item
	if len(item.Affixes) > 0 {
		out.Affixes = append([]RolledAffix(nil), item.Affixes...)
	}
	return out
}

// FloorSeed derives the world seed for a floor of a run from the run's seed.
// Floor 1 is the run seed itself (so a single-floor instance is unchanged);
// deeper floors mix the index in, so a whole descent is replayable floor by
// floor from one run seed.
func FloorSeed(run uint64, floor int) uint64 {
	if floor <= 1 {
		return run
	}
	s := run + uint64(floor)
	return SplitMix64(&s)
}
