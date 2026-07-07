package server

// Hardcore + SSF unit tests: the permanent flags, the hardcore fall (slot
// gone, memorial kept, account alive), and the SSF gates.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

func TestModeFlagsAndActiveFlags(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, err := st.Claim("Iron Will", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if hc, ssf := st.ActiveFlags(tok); hc || ssf {
		t.Error("offline account reported active flags")
	}
	if _, _, ok, _ := st.Connect(tok, ""); !ok {
		t.Fatal("connect")
	}
	if hc, ssf := st.ActiveFlags(tok); !hc || !ssf {
		t.Errorf("ActiveFlags = (%v, %v), want (true, true)", hc, ssf)
	}
	chars, _ := st.Roster(tok)
	if !chars[0].Hardcore || !chars[0].SSF {
		t.Errorf("roster flags = %+v, want both set", chars[0])
	}
}

// TestHardcoreFall: the character ends — memorial appended, name freed —
// but the account (stash, checkpoints, memorials) survives an emptied
// roster, and the dying session's flushes land nowhere.
func TestHardcoreFall(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, _ := st.Claim("Doomed Hero", true, false)
	st.StashAdd(tok, core.CharItem{Base: "short_sword"})
	if _, _, ok, _ := st.Connect(tok, ""); !ok {
		t.Fatal("connect")
	}
	st.AddCheckpoint(tok, 3)

	fallen, ok := st.FellInBattle(tok, 14, 9)
	if !ok || fallen != "Doomed Hero" {
		t.Fatalf("FellInBattle = (%q, %v), want the hero", fallen, ok)
	}
	// Late flushes from the dying session land nowhere.
	st.Bank(tok, &core.Character{Def: "player", Level: 99})
	st.Disconnect(tok, &core.Character{Def: "player", Level: 99})

	if !st.Exists(tok) {
		t.Fatal("the account fell with the character; memorials need it alive")
	}
	chars, _ := st.Roster(tok)
	if len(chars) != 0 {
		t.Fatalf("roster = %v, want empty", chars)
	}
	mem := st.Memorials(tok)
	if len(mem) != 1 || mem[0].Name != "Doomed Hero" || mem[0].Level != 14 || mem[0].Floor != 9 {
		t.Fatalf("memorials = %+v, want the level-14 floor-9 fall", mem)
	}
	if len(st.StashList(tok)) != 1 {
		t.Error("the stash fell with the character")
	}
	if got := st.Checkpoints(tok); len(got) != 1 {
		t.Error("checkpoints fell with the character")
	}
	// The name is free, and the account can roll the next one.
	if _, err := st.Claim("Doomed Hero", false, false); err != nil {
		t.Errorf("fallen name not claimable: %v", err)
	}
	if err := st.AddChar(tok, "Next Of Kin", false, false); err != nil {
		t.Errorf("memorial-only account refused a new character: %v", err)
	}
}

// TestHardcoreDeathEndsTheCharacter: the descent-side flow — a hardcore
// client's death deletes the slot, writes the memorial, and lights the
// doom fuse instead of ejecting them with the living.
func TestHardcoreDeathEndsTheCharacter(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	tok, _ := in.ids.Claim("Brave One", true, false)
	c.token = tok
	c.hardcore = true
	if _, _, ok, _ := in.ids.Connect(tok, ""); !ok {
		t.Fatal("connect")
	}
	in.descend() // floor 2
	a := in.sim.W.ActorByID(c.actor)
	a.Dead = true
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: uint64(c.actor), Note: "player"}},
		nil, nil, nil, nil)

	if c.doom == 0 {
		t.Error("no doom fuse lit on the hardcore dier")
	}
	chars, _ := in.ids.Roster(tok)
	if len(chars) != 0 {
		t.Fatalf("roster = %v, want the character gone", chars)
	}
	mem := in.ids.Memorials(tok)
	if len(mem) != 1 || mem[0].Floor != 2 {
		t.Fatalf("memorials = %+v, want one at floor 2", mem)
	}
}

// TestSSFGates: stash verbs drop and invites bounce for SSF characters.
func TestSSFGates(t *testing.T) {
	in, c, _ := descentInstanceAt(t, 3, 0)
	tok, _ := in.ids.Claim("Lone Wolf", false, true)
	c.token = tok
	c.ssf = true
	if _, _, ok, _ := in.ids.Connect(tok, ""); !ok {
		t.Fatal("connect")
	}
	// A stash put in the hideout: dropped, nothing banked.
	a := in.sim.W.ActorByID(c.actor)
	item := a.Inventory[0] // the starting uncut gem
	in.processStash([]stashWant{{c: c, ops: []stashOp{{item: item.ID}}}})
	if got := in.ids.StashList(tok); got != nil {
		t.Fatalf("SSF stash = %v, want untouched", got)
	}
	if len(in.sim.W.ActorByID(c.actor).Inventory) == 0 {
		t.Fatal("the item left the bag despite the SSF gate")
	}
}
