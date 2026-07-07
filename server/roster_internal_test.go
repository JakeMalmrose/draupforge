package server

// Roster unit tests: multiple characters under one account token (ROADMAP
// v2 Track 2 item 1) — add/select/delete, default-character pick, the
// account-wide stash, and the v1 → v2 identity-file migration. The full
// cookie → ?char= flow over a real socket lives in identity_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JakeMalmrose/draupforge/sim/core"
)

func TestRosterAddSelectBank(t *testing.T) {
	st, err := NewIdentityStore("")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.Claim("First", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddChar(tok, "Second", false, false); err != nil {
		t.Fatalf("AddChar: %v", err)
	}

	// Names are unique across the account and across accounts, any case.
	if err := st.AddChar(tok, "first", false, false); err != errNameTaken {
		t.Errorf("own-roster dupe: err = %v, want errNameTaken", err)
	}
	other, err := st.Claim("Rival", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddChar(other, "SECOND", false, false); err != errNameTaken {
		t.Errorf("cross-account dupe: err = %v, want errNameTaken", err)
	}
	if _, err := st.Claim("second", false, false); err != errNameTaken {
		t.Errorf("claim of a roster name: err = %v, want errNameTaken", err)
	}
	if err := st.AddChar("no-such-token", "Nobody", false, false); err != errNoIdentity {
		t.Errorf("AddChar on unknown token: err = %v, want errNoIdentity", err)
	}

	// Play Second, bank a leveled character, leave: Second is now both the
	// leveled slot and the most-recent default.
	name, char, ok, dup := st.Connect(tok, "Second")
	if !ok || dup || name != "Second" || char != nil {
		t.Fatalf("Connect(Second) = (%q, %v, ok=%v, dup=%v), want fresh Second", name, char, ok, dup)
	}
	st.Bank(tok, &core.Character{Def: "player", Level: 5})
	st.Disconnect(tok, &core.Character{Def: "player", Level: 6})

	chars, last := st.Roster(tok)
	if len(chars) != 2 || last != "Second" {
		t.Fatalf("Roster = (%v, last=%q), want 2 chars defaulting to Second", chars, last)
	}
	if chars[0].Name != "First" || chars[0].Level != 1 {
		t.Errorf("chars[0] = %+v, want fresh First at level 1", chars[0])
	}
	if chars[1].Name != "Second" || chars[1].Level != 6 {
		t.Errorf("chars[1] = %+v, want banked Second at level 6", chars[1])
	}

	// The empty pick lands on the most recently played; a stale name (a
	// deleted character remembered by the browser) falls back the same way.
	if name, char, ok, _ := st.Connect(tok, ""); !ok || name != "Second" || char == nil || char.Level != 6 {
		t.Fatalf("default Connect = (%q, %+v, ok=%v), want banked Second", name, char, ok)
	}
	st.Disconnect(tok, nil)
	if name, _, ok, _ := st.Connect(tok, "GoneName"); !ok || name != "Second" {
		t.Fatalf("stale-name Connect = (%q, ok=%v), want fallback to Second", name, ok)
	}
	st.Disconnect(tok, nil)

	// Selecting the other character banks into ITS slot, not the default's.
	if name, char, ok, _ := st.Connect(tok, "First"); !ok || name != "First" || char != nil {
		t.Fatalf("Connect(First) = (%q, %+v, ok=%v), want fresh First", name, char, ok)
	}
	st.Disconnect(tok, &core.Character{Def: "player", Level: 3})
	chars, last = st.Roster(tok)
	if last != "First" || chars[0].Level != 3 || chars[1].Level != 6 {
		t.Fatalf("after playing First: chars=%v last=%q, want First@3 default, Second@6", chars, last)
	}
}

func TestRosterCap(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, err := st.Claim("Cap 0", false, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < RosterCap; i++ {
		if err := st.AddChar(tok, "Cap "+string(rune(0x41+i)), false, false); err != nil {
			t.Fatalf("AddChar #%d: %v", i, err)
		}
	}
	if err := st.AddChar(tok, "One Too Many", false, false); err != errRosterFull {
		t.Fatalf("over-cap AddChar: err = %v, want errRosterFull", err)
	}
}

// TestDeleteCharKeepsAccount: deleting one character frees its name but
// leaves the account — other characters and the shared stash intact. Only
// the last deletion takes the account (and stash) with it.
func TestDeleteCharKeepsAccount(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, err := st.Claim("Keeper", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddChar(tok, "Doomed", false, false); err != nil {
		t.Fatal(err)
	}
	if !st.StashAdd(tok, core.CharItem{Base: "short_sword"}) {
		t.Fatal("StashAdd refused")
	}

	deleted, wasActive, gone := st.DeleteChar(tok, "doomed")
	if deleted != "Doomed" || wasActive || gone {
		t.Fatalf("DeleteChar = (%q, active=%v, gone=%v), want offline non-last delete", deleted, wasActive, gone)
	}
	if chars, _ := st.Roster(tok); len(chars) != 1 || chars[0].Name != "Keeper" {
		t.Fatalf("roster after delete = %v, want just Keeper", chars)
	}
	if items := st.StashList(tok); len(items) != 1 {
		t.Fatalf("stash after delete = %d items, want 1 — the stash is the account's", len(items))
	}
	// The freed name is claimable by anyone.
	if _, err := st.Claim("Doomed", false, false); err != nil {
		t.Fatalf("re-claim of freed name: %v", err)
	}

	// Deleting the last character deletes the account, stash included.
	deleted, _, gone = st.DeleteChar(tok, "Keeper")
	if deleted != "Keeper" || !gone {
		t.Fatalf("last DeleteChar = (%q, gone=%v), want account gone", deleted, gone)
	}
	if st.Name(tok) != "" || st.StashList(tok) != nil {
		t.Fatal("account survived its last character")
	}
}

// TestDeleteActiveCharBanksNowhere: deleting the character a live session
// is playing must not let that session's late Bank/Disconnect land in a
// different slot — the session 71 resurrection bug, roster edition.
func TestDeleteActiveCharBanksNowhere(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, err := st.Claim("Alive", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddChar(tok, "Victim", false, false); err != nil {
		t.Fatal(err)
	}
	if name, _, ok, _ := st.Connect(tok, "Victim"); !ok || name != "Victim" {
		t.Fatalf("Connect(Victim) failed (name=%q ok=%v)", name, ok)
	}

	deleted, wasActive, gone := st.DeleteChar(tok, "Victim")
	if deleted != "Victim" || !wasActive || gone {
		t.Fatalf("DeleteChar = (%q, active=%v, gone=%v), want live non-last delete", deleted, wasActive, gone)
	}

	// The dying session flushes: nothing may land on Alive's slot.
	st.Bank(tok, &core.Character{Def: "player", Level: 99})
	st.Disconnect(tok, &core.Character{Def: "player", Level: 99})
	chars, _ := st.Roster(tok)
	if len(chars) != 1 || chars[0].Name != "Alive" || chars[0].Level != 1 {
		t.Fatalf("roster after late flush = %v, want untouched level-1 Alive", chars)
	}
	// And the account is connectable again (online flag freed).
	if _, _, ok, dup := st.Connect(tok, ""); !ok || dup {
		t.Fatalf("reconnect after active delete: ok=%v dup=%v", ok, dup)
	}
}

// TestIdentityFileMigrationV1: a pre-roster store file loads as one-slot
// rosters — character and stash intact — and rewrites as v2.
func TestIdentityFileMigrationV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ids.json")
	v1 := map[string]any{
		"version": 1,
		"identities": map[string]any{
			"token-a": identityV1{
				Name:    "Veteran",
				Char:    &core.Character{Def: "player", Level: 9},
				Stash:   []core.CharItem{{Base: "short_sword"}},
				Created: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := NewIdentityStore(path)
	if err != nil {
		t.Fatal(err)
	}
	chars, last := st.Roster("token-a")
	if len(chars) != 1 || chars[0].Name != "Veteran" || chars[0].Level != 9 || last != "Veteran" {
		t.Fatalf("migrated roster = (%v, last=%q), want one Veteran at level 9", chars, last)
	}
	if items := st.StashList("token-a"); len(items) != 1 || items[0].Base != "short_sword" {
		t.Fatalf("migrated stash = %v, want the short_sword", items)
	}
	if st.TokenByName("veteran") != "token-a" {
		t.Error("migrated name does not resolve to its token")
	}

	// The migration marked the store dirty; a flush rewrites it as v2 and
	// the rewrite round-trips.
	st.mu.Lock()
	if !st.dirty {
		t.Error("migration left the store clean; it would never persist as v2")
	}
	st.saveLocked()
	st.mu.Unlock()
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Version != identityFileVersion {
		t.Fatalf("rewritten file version = %d, want %d", probe.Version, identityFileVersion)
	}
	st2, err := NewIdentityStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if chars, _ := st2.Roster("token-a"); len(chars) != 1 || chars[0].Level != 9 {
		t.Fatalf("v2 reload roster = %v, want the migrated Veteran", chars)
	}
}
