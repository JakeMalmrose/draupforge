package server

// Identity unit tests: the bank/restore plumbing between clients and the
// store, driven directly on tick-goroutine data like the descent unit
// tests. The full welcome/cookie flow over a real socket lives in
// identity_test.go.

import (
	"path/filepath"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// namedClient claims a name on in's store and returns a connected client
// for it, mirroring what HandleWS does.
func namedClient(t *testing.T, in *Instance, name string) *client {
	t.Helper()
	tok, err := in.ids.Claim(name, false, false)
	if err != nil {
		t.Fatal(err)
	}
	gotName, char, ok, dup := in.ids.Connect(tok, "")
	if !ok || dup || gotName != name {
		t.Fatalf("Connect = (%q, ok=%v, dup=%v), want fresh %q", gotName, ok, dup, name)
	}
	c := &client{tr: &fakeTransport{}, mode: modeBinary, name: name, token: tok}
	if char != nil {
		c.lastChar, c.hasChar = *char, true
	}
	return c
}

func TestNamedCharacterRoundTrip(t *testing.T) {
	in, err := New(content.DB(), Config{Seed: 1, IdentityPath: filepath.Join(t.TempDir(), "ids.json")})
	if err != nil {
		t.Fatal(err)
	}
	c := namedClient(t, in, "Hero")
	tok := c.token
	if !in.spawnClient(c) {
		t.Fatal("spawnClient refused the named client")
	}
	in.sim.W.ActorByID(c.actor).XP = 1234

	in.removeClient(c)
	if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
		t.Error("leaver's actor still alive")
	}

	// Reconnect: the banked character comes back and re-injects.
	_, char, ok, dup := in.ids.Connect(tok, "")
	if !ok || dup {
		t.Fatalf("reconnect refused (ok=%v dup=%v)", ok, dup)
	}
	if char == nil || char.XP != 1234 {
		t.Fatalf("banked char = %+v, want XP 1234", char)
	}
	c2 := &client{tr: &fakeTransport{}, mode: modeBinary, name: "Hero", token: tok, lastChar: *char, hasChar: true}
	if !in.spawnClient(c2) {
		t.Fatal("respawn refused")
	}
	if got := in.sim.W.ActorByID(c2.actor).XP; got != 1234 {
		t.Errorf("restored actor XP = %d, want 1234", got)
	}

	// The store file survives a process restart.
	in2, err := New(content.DB(), Config{Seed: 1, IdentityPath: in.cfg.IdentityPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, char, ok, _ := in2.ids.Connect(tok, ""); !ok || char == nil || char.XP != 1234 {
		t.Fatalf("after reload: ok=%v char=%+v, want XP 1234", ok, char)
	}
}

func TestDuplicateSessionAndRelease(t *testing.T) {
	in, err := New(content.DB(), Config{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := namedClient(t, in, "Hero")
	if _, _, ok, dup := in.ids.Connect(c.token, ""); ok || !dup {
		t.Fatalf("second Connect = ok=%v dup=%v, want a dup refusal", ok, dup)
	}
	if !in.spawnClient(c) {
		t.Fatal("spawn refused")
	}
	tok := c.token
	in.removeClient(c)
	// A processed leave frees the slot — and a double-filed leave (readLoop
	// and a failed send can both file one) must not blow up.
	in.removeClient(c)
	if _, _, ok, dup := in.ids.Connect(tok, ""); !ok || dup {
		t.Fatalf("after leave: ok=%v dup=%v, want reconnectable", ok, dup)
	}
}

func TestGuestLeavesNoTrace(t *testing.T) {
	in, err := New(content.DB(), Config{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := &client{tr: &fakeTransport{}, mode: modeBinary}
	if !in.spawnClient(c) {
		t.Fatal("guest spawn refused")
	}
	in.removeClient(c)
	if n := len(in.ids.byToken); n != 0 {
		t.Errorf("guest left %d identities behind", n)
	}
	if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
		t.Error("guest actor survived the leave")
	}
}

func TestWelcomeCarriesNameAndRoster(t *testing.T) {
	in, err := New(content.DB(), Config{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := namedClient(t, in, "Hero")
	if !in.spawnClient(c) {
		t.Fatal("spawn refused")
	}
	guest := &client{tr: &fakeTransport{}, mode: modeBinary}
	if !in.spawnClient(guest) {
		t.Fatal("guest spawn refused")
	}
	guest.send(in.welcomeFrame(guest), false)
	msg := guest.tr.(*fakeTransport).lastWelcome(t)
	if msg.Name != "" {
		t.Errorf("guest welcome name = %q, want empty", msg.Name)
	}
	if got := msg.Roster[uint64(c.actor)]; got != "Hero" {
		t.Errorf("roster[%d] = %q, want Hero (roster %v)", c.actor, got, msg.Roster)
	}
	if _, there := msg.Roster[uint64(guest.actor)]; there {
		t.Error("guest actor should not be in the roster")
	}
}

func TestClaimValidation(t *testing.T) {
	st, _ := NewIdentityStore("")
	for _, bad := range []string{"", "x", "no  double", "seventeen-chars-x", "läärve", "semi;colon", "-lead"} {
		if _, err := st.Claim(bad, false, false); err == nil {
			t.Errorf("Claim(%q) succeeded, want rejection", bad)
		}
	}
	if _, err := st.Claim("Jake M-2", false, false); err != nil {
		t.Errorf("Claim(Jake M-2): %v", err)
	}
	if _, err := st.Claim("jake m-2", false, false); err != errNameTaken {
		t.Errorf("case-insensitive dupe: err = %v, want errNameTaken", err)
	}
}

// TestDeleteResistsResurrection: after DeleteChar, the late writes a dying
// session fires (periodic Bank, the leave's Disconnect) must not re-create
// the character — the name stays free. Deleting the last character takes
// the whole account with it.
func TestDeleteResistsResurrection(t *testing.T) {
	st, err := NewIdentityStore("")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.Claim("Ghost", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, dup := st.Connect(tok, ""); !ok || dup {
		t.Fatalf("Connect: ok=%v dup=%v", ok, dup)
	}
	ch := core.Character{Def: "player", Level: 3}
	st.Bank(tok, &ch)

	deleted, wasActive, gone := st.DeleteChar(tok, "Ghost")
	if deleted != "Ghost" || !wasActive || !gone {
		t.Fatalf("DeleteChar = (%q, active=%v, gone=%v), want live last-char delete", deleted, wasActive, gone)
	}
	if st.Name(tok) != "" {
		t.Fatal("token still resolves after delete")
	}

	// The dying session's flushes land on the gone token: all no-ops.
	st.Bank(tok, &ch)
	st.Disconnect(tok, &ch)
	if st.Name(tok) != "" {
		t.Fatal("a late bank resurrected the deleted identity")
	}

	tok2, err := st.Claim("Ghost", false, false)
	if err != nil {
		t.Fatalf("re-claim of a deleted name failed: %v", err)
	}
	if tok2 == tok {
		t.Fatal("re-claim reissued the deleted token")
	}
	if _, char, ok, _ := st.Connect(tok2, ""); !ok || char != nil {
		t.Fatalf("re-claimed identity should be fresh: ok=%v char=%v", ok, char)
	}
}
