// Character extract/inject — the durable, world-independent projection of a
// player (DESIGN.md §14). A Character carries identity and possessions only:
// def, level, XP, pools, equipment, bag. Zone-local state (position, action,
// buffs, DoTs) deliberately does not transfer; the stat sheet rebuilds from
// def + level + equipment at injection, and item IDs are re-minted from the
// destination world's counter (entity IDs are world-local).
//
// The shapes mirror save.go's — JSON-marshalable by construction, so the
// future character store can persist these verbatim.
package core

import (
	"fmt"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// CharAffix is one rolled affix in character form: content by string ID.
type CharAffix struct {
	ID    string   `json:"id"`
	Value fm.Fixed `json:"value"`
}

// CharItem is an item in character form — no entity ID; IDs are minted at
// injection because they double as sheet mod sources and would collide in a
// fresh world.
type CharItem struct {
	Base     string      `json:"base"`
	Rarity   uint8       `json:"rarity"`
	Implicit fm.Fixed    `json:"implicit,omitempty"`
	Affixes  []CharAffix `json:"affixes,omitempty"`
}

// Character is the portable player state that survives zone transfers.
// Pools carry over across floors (clamped to the rebuilt maxima); a
// non-positive Life means "arrive refilled" — the death-respawn case, since
// a character is never dead, only its zone projection was.
type Character struct {
	Def       string                    `json:"def"`
	Level     int                       `json:"level"`
	XP        int64                     `json:"xp"`
	Life      fm.Fixed                  `json:"life"`
	Mana      fm.Fixed                  `json:"mana"`
	ES        fm.Fixed                  `json:"es"`
	Equipment [EquipSlotCount]*CharItem `json:"equipment"`
	Inventory []CharItem                `json:"inventory,omitempty"`
	// Passives are milestone choices, by ID in pick order — durable build
	// identity, unlike zone-local statuses.
	Passives []string `json:"passives,omitempty"`
}

// ExtractCharacter reduces an actor to its character state — the reverse of
// InjectCharacter. Pure data copy, valid at any point in a tick.
func ExtractCharacter(a *Actor) Character {
	ch := Character{
		Def:   a.Def.ID,
		Level: a.Level,
		XP:    a.XP,
		Life:  a.Life,
		Mana:  a.Mana,
		ES:    a.ES,
	}
	for slot, item := range a.Equipment {
		if item != nil {
			ci := charItem(*item)
			ch.Equipment[slot] = &ci
		}
	}
	for i := range a.Inventory {
		ch.Inventory = append(ch.Inventory, charItem(a.Inventory[i]))
	}
	for _, p := range a.Passives {
		ch.Passives = append(ch.Passives, p.ID)
	}
	return ch
}

func charItem(item Item) CharItem {
	ci := CharItem{Base: item.Base.ID, Rarity: uint8(item.Rarity), Implicit: item.Implicit}
	for _, af := range item.Affixes {
		ci.Affixes = append(ci.Affixes, CharAffix{ID: af.Def.ID, Value: af.Value})
	}
	return ci
}

// InjectCharacter mints a character's actor projection into a world: spawn
// at pos (clamped to walkable ground on grid worlds), level and XP restored,
// items re-minted with fresh entity IDs, sheet rebuilt from def + level +
// equipment. Content is resolved by string ID against the world's registry;
// any unknown reference fails before the world is touched.
func InjectCharacter(w *World, ch Character, pos space.Vec2) (*Actor, error) {
	def := w.Content.Actors[ch.Def]
	if def == nil {
		return nil, fmt.Errorf("core: character references unknown actor def %q", ch.Def)
	}
	affixes := make(map[string]*AffixDef, len(w.Content.Affixes))
	for _, af := range w.Content.Affixes {
		affixes[af.ID] = af
	}
	var equipment [EquipSlotCount]*Item
	for slot, ci := range ch.Equipment {
		if ci == nil {
			continue
		}
		item, err := decodeCharItem(w.Content, affixes, *ci)
		if err != nil {
			return nil, err
		}
		equipment[slot] = &item
	}
	inventory := make([]Item, 0, len(ch.Inventory))
	for _, ci := range ch.Inventory {
		item, err := decodeCharItem(w.Content, affixes, ci)
		if err != nil {
			return nil, err
		}
		inventory = append(inventory, item)
	}

	if w.Grid != nil {
		p, ok := w.Grid.NearestWalkable(pos)
		if !ok {
			return nil, fmt.Errorf("core: no walkable tile to inject character %q", ch.Def)
		}
		pos = p
	}
	a := w.SpawnActor(def, pos)
	a.SetLevel(ch.Level)
	a.XP = ch.XP
	for _, id := range ch.Passives {
		pd := w.Content.Passive(id)
		if pd == nil {
			return nil, fmt.Errorf("core: character references unknown passive %q", id)
		}
		a.TakePassive(pd)
	}
	// Mint IDs in slot-then-bag order so injection is deterministic.
	for slot := range equipment {
		if item := equipment[slot]; item != nil {
			item.ID = w.AllocID()
			a.Equipment[slot] = item
			a.AddItemMods(item)
		}
	}
	for i := range inventory {
		inventory[i].ID = w.AllocID()
	}
	a.Inventory = inventory

	// Pools: carry, clamped to the rebuilt maxima. Life <= 0 means a
	// death-respawn — arrive refilled rather than dead.
	if ch.Life <= 0 {
		a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
	} else {
		a.Life = fm.Min(ch.Life, a.MaxLife())
		a.Mana = fm.Min(ch.Mana, a.MaxMana())
		a.ES = fm.Min(ch.ES, a.MaxES())
	}
	return a, nil
}

func decodeCharItem(db *ContentDB, affixes map[string]*AffixDef, ci CharItem) (Item, error) {
	base := db.BaseItems[ci.Base]
	if base == nil {
		return Item{}, fmt.Errorf("core: character references unknown base item %q", ci.Base)
	}
	item := Item{Base: base, Rarity: Rarity(ci.Rarity), Implicit: ci.Implicit}
	for _, af := range ci.Affixes {
		def := affixes[af.ID]
		if def == nil {
			return Item{}, fmt.Errorf("core: character references unknown affix %q", af.ID)
		}
		item.Affixes = append(item.Affixes, RolledAffix{Def: def, Value: af.Value})
	}
	return item, nil
}
