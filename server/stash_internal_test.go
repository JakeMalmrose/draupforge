package server

// The stash contract: put moves a bag item into the identity's bank and
// take moves it back (re-minted), both hideout-only and named-only, with
// caps respected on both sides and the bank surviving a store reload.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// stashFixture: a hideout instance with one named client holding a rusty
// sword in its bag.
func stashFixture(t *testing.T) (*Instance, *client, *fakeTransport, core.EntityID) {
	t.Helper()
	in, c, tr := descentInstanceAt(t, 3, 0)
	tok, err := in.ids.Claim("Banker")
	if err != nil {
		t.Fatal(err)
	}
	c.token, c.name = tok, "Banker"
	a := in.sim.W.ActorByID(c.actor)
	item := core.Item{ID: in.sim.W.AllocID(), Base: in.db.BaseItems["rusty_sword"]}
	a.Inventory = append(a.Inventory, item)
	return in, c, tr, item.ID
}

// lastStashFrame decodes the newest "stash" frame the transport saw.
func lastStashFrame(t *testing.T, tr *fakeTransport) *protocol.StashSnap {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for i := len(tr.frames) - 1; i >= 0; i-- {
		var msg protocol.ServerMsg
		if json.Unmarshal(tr.frames[i], &msg) == nil && msg.Type == "stash" {
			return msg.Stash
		}
	}
	t.Fatal("no stash frame recorded")
	return nil
}

func TestStashPutAndTake(t *testing.T) {
	in, c, tr, itemID := stashFixture(t)
	a := in.sim.W.ActorByID(c.actor)
	bagBefore := len(a.Inventory)

	in.processStash([]stashWant{{c: c, ops: []stashOp{{item: itemID}}}})
	if len(a.Inventory) != bagBefore-1 {
		t.Fatalf("bag has %d items after put, want %d", len(a.Inventory), bagBefore-1)
	}
	banked := in.ids.StashList(c.token)
	if len(banked) != 1 || banked[0].Base != "rusty_sword" {
		t.Fatalf("stash = %+v, want one rusty_sword", banked)
	}
	snap := lastStashFrame(t, tr)
	if len(snap.Items) != 1 || snap.Items[0].Base != "rusty_sword" || snap.Cap != StashCap {
		t.Fatalf("stash frame = %+v, want the banked sword and the cap", snap)
	}

	in.processStash([]stashWant{{c: c, ops: []stashOp{{take: true, idx: 0}}}})
	if len(in.ids.StashList(c.token)) != 0 {
		t.Fatal("take left the item banked")
	}
	if len(a.Inventory) != bagBefore {
		t.Fatalf("bag has %d items after take, want %d", len(a.Inventory), bagBefore)
	}
	got := a.Inventory[len(a.Inventory)-1]
	if got.Base.ID != "rusty_sword" || got.ID == 0 || got.ID == itemID {
		t.Fatalf("withdrawn item = %+v, want a rusty_sword under a fresh entity ID", got)
	}
}

func TestStashGuardrails(t *testing.T) {
	t.Run("guests have no stash", func(t *testing.T) {
		in, c, _, itemID := stashFixture(t)
		c.token = ""
		a := in.sim.W.ActorByID(c.actor)
		before := len(a.Inventory)
		in.processStash([]stashWant{{c: c, ops: []stashOp{{item: itemID}}}})
		if len(a.Inventory) != before {
			t.Fatal("a guest banked an item")
		}
	})
	t.Run("hideout only", func(t *testing.T) {
		in, c, _, itemID := stashFixture(t)
		in.floor = 2
		a := in.sim.W.ActorByID(c.actor)
		before := len(a.Inventory)
		in.processStash([]stashWant{{c: c, ops: []stashOp{{item: itemID}}}})
		if len(a.Inventory) != before || len(in.ids.StashList(c.token)) != 0 {
			t.Fatal("stashed outside the hideout")
		}
	})
	t.Run("stash cap holds", func(t *testing.T) {
		in, c, _, itemID := stashFixture(t)
		for i := 0; i < StashCap; i++ {
			if !in.ids.StashAdd(c.token, core.CharItem{Base: "iron_ring"}) {
				t.Fatal("filling the stash failed early")
			}
		}
		a := in.sim.W.ActorByID(c.actor)
		before := len(a.Inventory)
		in.processStash([]stashWant{{c: c, ops: []stashOp{{item: itemID}}}})
		if len(a.Inventory) != before {
			t.Fatal("put into a full stash consumed the item")
		}
	})
	t.Run("bag cap holds", func(t *testing.T) {
		in, c, _, _ := stashFixture(t)
		in.ids.StashAdd(c.token, core.CharItem{Base: "iron_ring"})
		a := in.sim.W.ActorByID(c.actor)
		for len(a.Inventory) < a.Def.InventorySize {
			a.Inventory = append(a.Inventory, core.Item{
				ID: in.sim.W.AllocID(), Base: in.db.BaseItems["iron_ring"],
			})
		}
		in.processStash([]stashWant{{c: c, ops: []stashOp{{take: true, idx: 0}}}})
		if len(in.ids.StashList(c.token)) != 1 {
			t.Fatal("take into a full bag lost the banked item")
		}
	})
}

func TestStashSurvivesStoreReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ids.json")
	st, err := NewIdentityStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.Claim("Keeper")
	if err != nil {
		t.Fatal(err)
	}
	if !st.StashAdd(tok, core.CharItem{Base: "bone_amulet", Rarity: 2}) {
		t.Fatal("stash add failed")
	}
	st.Disconnect(tok, nil) // forces a save

	st2, err := NewIdentityStore(path)
	if err != nil {
		t.Fatal(err)
	}
	banked := st2.StashList(tok)
	if len(banked) != 1 || banked[0].Base != "bone_amulet" || banked[0].Rarity != 2 {
		t.Fatalf("reloaded stash = %+v, want the banked rare amulet", banked)
	}
}
