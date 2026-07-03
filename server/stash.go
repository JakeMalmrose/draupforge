// The stash — the hideout bank (ROADMAP phase 4). Items live on the
// identity in durable character form (core.CharItem), outside every world
// and every character bag, so a good drop stops living or dying with one
// bag. Host-layer like the descent: the sim never knows stashes exist —
// verbs buffer on the client and the tick goroutine moves items between
// the actor's bag and the identity store between steps, the same way
// injection does. Hideout-only (floor 0 of a descent), named players only:
// a guest has no identity to bank into.
package server

import (
	"encoding/json"
	"log"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// stashOp is one buffered stash verb, in the order the client sent them.
type stashOp struct {
	take bool
	item core.EntityID // put: the bag item's entity ID
	idx  int           // take: the stash index
}

// stashWant is one client's harvested stash verbs for a tick.
type stashWant struct {
	c   *client
	ops []stashOp
}

// processStash runs this tick's stash verbs. Anywhere but the hideout the
// verbs drop silently — the client only offers the stash there.
func (in *Instance) processStash(wants []stashWant) {
	if len(wants) == 0 || in.run == 0 || in.floor != 0 {
		return
	}
	in.surgery = true // replay: bag mutations happen outside Step
	for _, w := range wants {
		c := w.c
		if c.token == "" {
			continue // guests have no identity to bank into
		}
		a := in.sim.W.ActorByID(c.actor)
		if a == nil || a.Dead {
			continue
		}
		for _, op := range w.ops {
			if op.take {
				if len(a.Inventory) >= a.Def.InventorySize {
					continue // bag full; the item stays banked
				}
				ci, ok := in.ids.StashTake(c.token, op.idx)
				if !ok {
					continue
				}
				item, err := core.ItemFromChar(in.sim.W.Content, ci)
				if err != nil {
					// Content edits orphaned it; re-bank rather than delete.
					in.ids.StashAdd(c.token, ci)
					log.Printf("server: stash take: %v", err)
					continue
				}
				item.ID = in.sim.W.AllocID()
				a.Inventory = append(a.Inventory, item)
			} else {
				idx := -1
				for i := range a.Inventory {
					if a.Inventory[i].ID == op.item {
						idx = i
						break
					}
				}
				if idx < 0 {
					continue // not a bag item (or already gone)
				}
				if !in.ids.StashAdd(c.token, core.CharItemOf(a.Inventory[idx])) {
					continue // stash full; the item stays in the bag
				}
				a.Inventory = append(a.Inventory[:idx], a.Inventory[idx+1:]...)
			}
		}
		// One refreshed snapshot per client per tick, win or lose — a
		// dropped op still reconciles the UI with reality.
		if !c.send(in.stashFrame(c), false) {
			c.tr.Close()
		}
	}
}

// stashSnap is the wire form of one identity's stash. Item IDs are stash
// indices — stash items have no entity, and "take" addresses by index.
func (in *Instance) stashSnap(c *client) *protocol.StashSnap {
	if c.token == "" {
		return nil
	}
	snap := &protocol.StashSnap{Cap: StashCap, Items: []protocol.ItemSnap{}}
	for i, ci := range in.ids.StashList(c.token) {
		snap.Items = append(snap.Items, charItemSnap(in.db, uint64(i), ci))
	}
	return snap
}

func (in *Instance) stashFrame(c *client) []byte {
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "stash", Stash: in.stashSnap(c)})
	return frame
}

// charItemSnap renders a durable item for the wire without minting a live
// one — same shape the bag uses, so the client renders both identically.
func charItemSnap(db *core.ContentDB, id uint64, ci core.CharItem) protocol.ItemSnap {
	if ci.Gem != nil {
		return protocol.ItemSnap{ID: id, Rarity: "normal", Gem: &protocol.GemItemSnap{
			Support: ci.Gem.Support, Level: ci.Gem.Level, Choices: ci.Gem.Choices,
		}}
	}
	out := protocol.ItemSnap{ID: id, Base: ci.Base, Rarity: core.Rarity(ci.Rarity).String()}
	if base := db.BaseItems[ci.Base]; base != nil && base.Implicit != nil && ci.Implicit != 0 {
		out.Implicit = &protocol.AffixSnap{ID: base.Implicit.ID, Value: ci.Implicit.Milli()}
	}
	for _, af := range ci.Affixes {
		out.Affixes = append(out.Affixes, protocol.AffixSnap{ID: af.ID, Value: af.Value.Milli()})
	}
	if u := db.Unique(ci.Unique); u != nil {
		out.Unique = &protocol.UniqueItemSnap{Name: u.Name, Desc: u.Desc, Mods: u.ModLines}
	}
	return out
}
