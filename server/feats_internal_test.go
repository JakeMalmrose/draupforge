package server

// Feat unit tests: the store, depth triggers, kill triggers, and the
// untouched check.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
)

func TestFeatStore(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, _ := st.Claim("Achiever", false, false)
	if !st.AddFeat(tok, "king") {
		t.Fatal("first earn reported not-new")
	}
	if st.AddFeat(tok, "king") {
		t.Fatal("re-earn reported new")
	}
	st.AddFeat(tok, "apex")
	if got := st.Feats(tok); len(got) != 2 || got[0] != "apex" || got[1] != "king" {
		t.Fatalf("feats = %v, want sorted [apex king]", got)
	}
	if st.AddFeat("nobody", "king") {
		t.Error("unknown token earned a feat")
	}
}

// TestFeatTriggers: depth entry and set-piece kills pay out, and the
// untouched king requires a clean recap ring.
func TestFeatTriggers(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	tok, _ := in.ids.Claim("Trophy Hunter", true, false)
	c.token = tok
	c.hardcore = true
	if _, _, ok, _ := in.ids.Connect(tok, ""); !ok {
		t.Fatal("connect")
	}

	// Depth: entering floor 10 proves three feats (10 + the hardcore 10).
	in.floor = 9
	in.descend()
	feats := in.ids.Feats(tok)
	want := map[string]bool{"depth_10": true, "hc_10": true}
	for _, f := range feats {
		delete(want, f)
	}
	if len(want) != 0 {
		t.Fatalf("after floor 10: feats = %v, missing %v", feats, want)
	}

	// A king kill with hits on the ring: Kingslayer but not Untouchable.
	c.recentHits = []protocol.RecapHit{{From: "zombie", Amount: 1000}}
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: 999, Note: bossDef}},
		nil, nil, nil, nil)
	feats = in.ids.Feats(tok)
	has := func(id string) bool {
		for _, f := range feats {
			if f == id {
				return true
			}
		}
		return false
	}
	if !has("king") || has("king_untouched") {
		t.Fatalf("hit king kill: feats = %v, want king without untouched", feats)
	}

	// A clean-ring king kill earns Untouchable.
	c.recentHits = nil
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: 999, Note: bossDef}},
		nil, nil, nil, nil)
	if feats = in.ids.Feats(tok); !has("king_untouched") {
		// refresh the closure's view
		found := false
		for _, f := range feats {
			if f == "king_untouched" {
				found = true
			}
		}
		if !found {
			t.Fatalf("untouched king kill: feats = %v, want king_untouched", feats)
		}
	}
}
