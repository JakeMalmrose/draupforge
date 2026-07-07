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
// fresh world. Uncut gems carry Gem instead of Base.
type CharItem struct {
	Base      string      `json:"base,omitempty"`
	Rarity    uint8       `json:"rarity"`
	Implicit  fm.Fixed    `json:"implicit,omitempty"`
	ItemLevel int         `json:"ilvl,omitempty"`
	Affixes   []CharAffix `json:"affixes,omitempty"`
	Gem       *CharUncut  `json:"gem,omitempty"`
	Unique    string      `json:"unique,omitempty"` // UniqueDef ID
}

// CharUncut is an uncut gem item's payload in character form.
type CharUncut struct {
	Support bool     `json:"support,omitempty"`
	Level   int      `json:"level,omitempty"`
	Choices []string `json:"choices"`
}

// CharGem is one cut skill gem in character form; Supports is
// socket-addressed, "" = empty socket.
type CharGem struct {
	Skill    string   `json:"skill"`
	Level    int      `json:"level"`
	Sockets  int      `json:"sockets"`
	Supports []string `json:"supports"`
	AuraOn   bool     `json:"aura_on,omitempty"`
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
	// Gems are the cut skill gems, in cut order — the other half of build
	// identity. A character without any (legacy saves) is re-granted its
	// def's starting gems at injection.
	Gems []CharGem `json:"gems,omitempty"`
	// FlaskCharges carries the flask bank across zones, like pools.
	FlaskCharges []int32 `json:"flask_charges,omitempty"`
	// Orbs carries the crafting wallet, OrbKind order.
	Orbs []int32 `json:"orbs,omitempty"`
	// Shards carries the Forge balance, durable like the wallet.
	Shards int32 `json:"shards,omitempty"`
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
	for i := range a.Gems {
		g := &a.Gems[i]
		cg := CharGem{Skill: g.Skill.ID, Level: g.Level, Sockets: g.Sockets, AuraOn: g.AuraOn}
		for _, sup := range g.Supports {
			if sup == nil {
				cg.Supports = append(cg.Supports, "")
			} else {
				cg.Supports = append(cg.Supports, sup.ID)
			}
		}
		ch.Gems = append(ch.Gems, cg)
	}
	ch.FlaskCharges = a.FlaskCharges
	for _, n := range a.Orbs {
		if n != 0 {
			ch.Orbs = a.Orbs[:]
			break
		}
	}
	ch.Shards = a.Shards
	return ch
}

// CharItemOf converts one live item to its durable character form — the
// same shape ExtractCharacter uses; host-layer storage (the stash) stores
// items this way.
func CharItemOf(item Item) CharItem { return charItem(item) }

// ItemFromChar resolves a durable item back to a live one against a content
// registry. The entity ID is left zero — the destination world mints it,
// same rule as injection.
func ItemFromChar(db *ContentDB, ci CharItem) (Item, error) {
	affixes := make(map[string]*AffixDef, len(db.Affixes))
	for _, af := range db.Affixes {
		affixes[af.ID] = af
	}
	return decodeCharItem(db, affixes, ci)
}

func charItem(item Item) CharItem {
	if item.Gem != nil {
		return CharItem{Gem: &CharUncut{
			Support: item.Gem.Support, Level: item.Gem.Level, Choices: item.Gem.Choices,
		}}
	}
	ci := CharItem{Base: item.Base.ID, Rarity: uint8(item.Rarity), Implicit: item.Implicit, ItemLevel: item.ItemLevel}
	for _, af := range item.Affixes {
		ci.Affixes = append(ci.Affixes, CharAffix{ID: af.Def.ID, Value: af.Value})
	}
	if item.Unique != nil {
		ci.Unique = item.Unique.ID
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
	// Cut gems replace the def's starting grant; a character without any
	// (legacy saves predating gems) keeps the starters SpawnActor gave it.
	if len(ch.Gems) > 0 {
		a.Gems = nil
		for _, cg := range ch.Gems {
			sk := w.Content.Skills[cg.Skill]
			if sk == nil {
				return nil, fmt.Errorf("core: character references unknown skill %q", cg.Skill)
			}
			g := Gem{Skill: sk, Level: cg.Level, Sockets: cg.Sockets, AuraOn: cg.AuraOn}
			for _, id := range cg.Supports {
				if id == "" {
					g.Supports = append(g.Supports, nil)
					continue
				}
				sup := w.Content.Support(id)
				if sup == nil {
					return nil, fmt.Errorf("core: character references unknown support %q", id)
				}
				g.Supports = append(g.Supports, sup)
			}
			a.Gems = append(a.Gems, g)
		}
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

	// Flask charges carry like pools; length re-clamps to the def's flasks
	// (content edits between sessions shrink or grow the bank sanely).
	for i := range a.FlaskCharges {
		if i < len(ch.FlaskCharges) {
			a.FlaskCharges[i] = min(max(ch.FlaskCharges[i], 0), FlaskMaxCharges)
		}
	}

	if len(ch.Orbs) <= int(OrbCount) {
		copy(a.Orbs[:], ch.Orbs)
	}
	a.Shards = ch.Shards

	// Running auras re-apply as part of the sheet rebuild (their AuraOn
	// flag is the durable record) — before the pool clamp, so reserved
	// mana counts against the arriving maximum.
	ActivateCharacterAuras(w, a)

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
	if ci.Gem != nil {
		for _, c := range ci.Gem.Choices {
			if ci.Gem.Support {
				if db.Support(c) == nil {
					return Item{}, fmt.Errorf("core: character references unknown support %q", c)
				}
			} else if db.Skills[c] == nil {
				return Item{}, fmt.Errorf("core: character references unknown skill %q", c)
			}
		}
		return Item{Gem: &UncutGem{
			Support: ci.Gem.Support, Level: ci.Gem.Level, Choices: ci.Gem.Choices,
		}}, nil
	}
	base := db.BaseItems[ci.Base]
	if base == nil {
		return Item{}, fmt.Errorf("core: character references unknown base item %q", ci.Base)
	}
	item := Item{Base: base, Rarity: Rarity(ci.Rarity), Implicit: ci.Implicit, ItemLevel: ci.ItemLevel}
	for _, af := range ci.Affixes {
		def := affixes[af.ID]
		if def == nil {
			return Item{}, fmt.Errorf("core: character references unknown affix %q", af.ID)
		}
		item.Affixes = append(item.Affixes, RolledAffix{Def: def, Value: af.Value})
	}
	if ci.Unique != "" {
		item.Unique = db.Unique(ci.Unique)
		if item.Unique == nil {
			return Item{}, fmt.Errorf("core: character references unknown unique %q", ci.Unique)
		}
	}
	return item, nil
}
