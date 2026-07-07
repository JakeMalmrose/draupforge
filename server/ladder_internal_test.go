package server

// Ladder + death-recap unit tests: bests record per character with the
// build attached, the board sorts deepest-first, and the dying client gets
// a recap of what hit them.

import (
	"encoding/json"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

func TestRecordBestAndLadder(t *testing.T) {
	st, _ := NewIdentityStore("")
	db := content.DB()

	tokA, _ := st.Claim("Deep One")
	st.AddChar(tokA, "Shallow Alt")
	tokB, _ := st.Claim("Rival Guy")

	ch := &core.Character{Def: "player", Level: 12,
		Gems: []core.CharGem{{Skill: "fireball", Level: 7, Supports: []string{"immolate", ""}}}}

	// Deep One reaches floor 9 playing fireball.
	if _, _, ok, _ := st.Connect(tokA, "Deep One"); !ok {
		t.Fatal("connect A")
	}
	st.RecordBest(tokA, 5, buildSnapOf(db, ch))
	st.RecordBest(tokA, 9, buildSnapOf(db, ch))
	st.RecordBest(tokA, 7, buildSnapOf(db, ch)) // shallower: no-op
	st.Disconnect(tokA, nil)

	// The alt reaches floor 3.
	if _, _, ok, _ := st.Connect(tokA, "Shallow Alt"); !ok {
		t.Fatal("connect alt")
	}
	st.RecordBest(tokA, 3, buildSnapOf(db, ch))
	st.Disconnect(tokA, nil)

	// The rival reaches floor 9 too, at a lower level.
	if _, _, ok, _ := st.Connect(tokB, ""); !ok {
		t.Fatal("connect B")
	}
	st.RecordBest(tokB, 9, nil)
	st.Disconnect(tokB, nil)

	rows := st.Ladder()
	if len(rows) != 3 {
		t.Fatalf("ladder rows = %d, want 3", len(rows))
	}
	if rows[0].Name != "Deep One" || rows[0].Best != 9 {
		t.Errorf("row 0 = %+v, want Deep One at floor 9 (level breaks the tie)", rows[0])
	}
	if rows[1].Name != "Rival Guy" || rows[2].Name != "Shallow Alt" {
		t.Errorf("rows 1,2 = %s, %s — want Rival Guy then Shallow Alt", rows[1].Name, rows[2].Name)
	}
	b := rows[0].Build
	if b == nil || len(b.Gems) != 1 || b.Gems[0].Level != 7 {
		t.Fatalf("best build = %+v, want the fireball-7 snapshot", b)
	}
	if len(b.Gems[0].Supports) != 1 {
		t.Errorf("supports = %v, want the one socketed support (empty sockets skipped)", b.Gems[0].Supports)
	}

	// Offline RecordBest is a no-op — bests are banked by live sessions.
	st.RecordBest(tokB, 50, nil)
	if rows := st.Ladder(); rows[0].Best == 50 {
		t.Error("offline RecordBest landed")
	}
}

// TestDeathRecapFrame: a client death sends a recap carrying the recent
// hits the tick recorded, the floor, and its mods.
func TestDeathRecapFrame(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	in.descend()
	in.descend()
	in.descend() // floor 4: modded depth
	a := in.sim.W.ActorByID(c.actor)

	// Two hits land on the client, then the death event.
	hits := []protocol.EventSnap{
		{Kind: "hit", Actor: 999, Other: uint64(c.actor), Amount: 12000, Note: "zombie_slam"},
		{Kind: "hit", Actor: 999, Other: uint64(c.actor), Amount: 15500, Note: "zombie_slam"},
	}
	in.runTick(hits, nil, nil, nil, nil)
	if len(c.recentHits) != 2 {
		t.Fatalf("recorded %d hits, want 2", len(c.recentHits))
	}

	a.Dead = true
	tr.mu.Lock()
	tr.frames = nil
	tr.mu.Unlock()
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: uint64(c.actor), Note: "player"}},
		nil, nil, nil, nil)

	var recap *protocol.RecapSnap
	tr.mu.Lock()
	for _, f := range tr.frames {
		var msg protocol.ServerMsg
		if json.Unmarshal(f, &msg) == nil && msg.Type == "recap" {
			recap = msg.Recap
		}
	}
	tr.mu.Unlock()
	if recap == nil {
		t.Fatal("no recap frame arrived")
	}
	if recap.Floor != 4 {
		t.Errorf("recap floor = %d, want 4", recap.Floor)
	}
	if len(recap.Hits) != 2 || recap.Hits[1].Amount != 15500 || recap.Hits[1].From == "" {
		t.Errorf("recap hits = %+v, want the two recorded slams", recap.Hits)
	}
	if len(recap.Mods) != modCountAt(4, in.route, in.chamber) {
		t.Errorf("recap mods = %v, want the floor's rolled mods", recap.Mods)
	}
	// The eject cleared the evidence for the next world.
	if len(c.recentHits) != 0 {
		t.Errorf("recentHits after eject = %d, want cleared", len(c.recentHits))
	}
}
