package server

// Floor-modifier unit tests under the delve chart: deterministic per-node
// rolls, depth-scaled counts, mods actually landing on monsters, and the
// stairs → map → travel flow.

import (
	"encoding/json"
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

func TestModCountBands(t *testing.T) {
	cases := []struct{ row, want int }{{1, 0}, {2, 1}, {3, 1}, {4, 2}, {6, 2}, {7, 3}, {17, 3}}
	for _, c := range cases {
		if got := modCountForRow(c.row); got != c.want {
			t.Errorf("modCountForRow(%d) = %d, want %d", c.row, got, c.want)
		}
	}
	// Row 1 stays clean even for a would-be juiced node.
	for col := 0; col < delveCols; col++ {
		if mods := delveNodeMods(99, nodeAddr{1, col}); len(mods) != 0 {
			t.Errorf("row-1 node rolled mods: %v", mods)
		}
	}
}

func TestNodeModsDeterministicAndDistinct(t *testing.T) {
	n := nodeAddr{Row: 7, Col: 3}
	a := delveNodeMods(99, n)
	b := delveNodeMods(99, n)
	if len(a) < 3 || len(a) != len(b) {
		t.Fatalf("rolled %d/%d mods, want the row-7 band (3+)", len(a), len(b))
	}
	seen := map[string]bool{}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("same node rolled different mods: %v vs %v", a, b)
		}
		if seen[a[i].ID] {
			t.Fatalf("duplicate mod %q in one roll", a[i].ID)
		}
		seen[a[i].ID] = true
	}
	// Sibling nodes diverge somewhere along the row — the column salts.
	diverged := false
	for col := 0; col < delveCols && !diverged; col++ {
		c := delveNodeMods(99, nodeAddr{Row: 7, Col: col})
		if len(c) != len(a) {
			diverged = true
			break
		}
		for i := range c {
			if c[i].ID != a[i].ID {
				diverged = true
				break
			}
		}
	}
	if !diverged {
		t.Error("every row-7 node rolled identical mods — suspicious salt")
	}
}

// TestFloorModsLandOnMonsters: build a modded node whose roll includes a
// monster-facing package and assert every monster carries it; the same
// address rebuilds to the same world hash.
func TestFloorModsLandOnMonsters(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	// Scan the chart for a node whose mods include a MonMod package —
	// deterministic for the fixed seed.
	var node nodeAddr
	var wantMod string
scan:
	for row := 4; row <= 8; row++ {
		for _, col := range delveRow(in.runSeed, row) {
			for _, m := range delveNodeMods(in.runSeed, nodeAddr{row, col}) {
				if m.MonMod != "" {
					node, wantMod = nodeAddr{row, col}, m.MonMod
					break scan
				}
			}
		}
	}
	if wantMod == "" {
		t.Fatal("no node in rows 4–8 rolled a monster-facing mod — table drifted?")
	}
	s, err := in.buildFloor(node, 2)
	if err != nil {
		t.Fatal(err)
	}
	monsters, carrying := 0, 0
	for _, a := range s.W.Actors {
		if a.Team != core.TeamMonsters {
			continue
		}
		monsters++
		for _, md := range a.Mods {
			if md.ID == wantMod {
				carrying++
				break
			}
		}
	}
	if monsters == 0 || carrying != monsters {
		t.Fatalf("%d/%d monsters carry floor mod %q, want all", carrying, monsters, wantMod)
	}

	// Replayable: same address → same hash.
	s2, err := in.buildFloor(node, 2)
	if err != nil {
		t.Fatal(err)
	}
	if s.W.Hash() != s2.W.Hash() {
		t.Error("same node address built different worlds")
	}
}

// TestTravelFlowOverRunTick: at a node's last-floor stairs, "descend"
// answers with a travel-mode delve frame (no swap); new ground stays barred
// until the set-piece falls; a travel pick then enters the chosen neighbor.
func TestTravelFlowOverRunTick(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	in.descend()
	in.descend() // fin 3: the entry node's last floor
	if in.fin != nodeFloors || in.floor != 3 {
		t.Fatalf("node/fin/floor = %v/%d/%d, want the entry node's last floor", in.node, in.fin, in.floor)
	}

	// Stand at the stairs and ask to descend: a map, not a swap.
	a := in.sim.W.ActorByID(c.actor)
	a.Pos = in.stairs
	tr.mu.Lock()
	tr.frames = nil
	tr.mu.Unlock()
	in.runTick(nil, runWants{descends: []*client{c}})
	if in.floor != 3 {
		t.Fatalf("descend swapped immediately to floor %d; want the map first", in.floor)
	}
	var delve *protocol.DelveSnap
	tr.mu.Lock()
	for _, f := range tr.frames {
		var msg protocol.ServerMsg
		if json.Unmarshal(f, &msg) == nil && msg.Type == "delve" {
			delve = msg.Delve
		}
	}
	tr.mu.Unlock()
	if delve == nil || delve.Kind != "travel" {
		t.Fatalf("delve frame = %+v, want travel mode", delve)
	}
	if delve.Row != in.node.Row || delve.Col != in.node.Col {
		t.Errorf("delve current = %d:%d, want %v", delve.Row, delve.Col, in.node)
	}
	// Uncleared: no unvisited node is travelable yet.
	for _, n := range delve.Nodes {
		if n.CanGo && !n.Visited {
			t.Errorf("uncleared node offers new ground %d:%d", n.Row, n.Col)
		}
	}

	// Pick a downward neighbor anyway: refused (the set-piece stands).
	var next nodeAddr
	for _, nb := range delveNeighbors(in.runSeed, in.node) {
		if nb.Row == in.node.Row+1 {
			next = nb
			break
		}
	}
	if next.Row == 0 {
		t.Fatal("entry node has no downward edge — generation broke connectivity")
	}
	a.Pos = in.stairs
	in.runTick(nil, runWants{travels: []travelWant{{c: c, to: next}}})
	if in.floor != 3 {
		t.Fatalf("travel through a live set-piece swapped to floor %d", in.floor)
	}

	// The set-piece falls: the node clears, and the same pick now travels.
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: 999, Note: setPieceFor(in.node.Row)}}, runWants{})
	if !in.cleared[in.node] {
		t.Fatal("set-piece death did not clear the node")
	}
	a = in.sim.W.ActorByID(c.actor)
	a.Pos = in.stairs
	in.runTick(nil, runWants{travels: []travelWant{{c: c, to: next}}})
	if in.node != next || in.fin != 1 || in.floor != globalFloor(next.Row, 1) {
		t.Fatalf("after travel: node=%v fin=%d floor=%d, want %v/1/%d",
			in.node, in.fin, in.floor, next, globalFloor(next.Row, 1))
	}
	if !in.visited[next] {
		t.Error("travelled node not marked visited")
	}

	// The run snap reads the new node's mods and address.
	rs := in.runSnap()
	if rs.Row != next.Row || rs.Col != next.Col || rs.Fin != 1 {
		t.Errorf("run snap address = %d:%d fin %d, want %v fin 1", rs.Row, rs.Col, rs.Fin, next)
	}
	if len(rs.Mods) != len(delveNodeMods(in.runSeed, next)) {
		t.Errorf("run snap mods = %v, want the node's %d", rs.Mods, len(delveNodeMods(in.runSeed, next)))
	}
}

// TestTravelBackUp: cleared ground is a highway — from a cleared node's
// stairs you can re-enter any visited node, holding or raising your depth.
func TestTravelBackUp(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	first := in.node
	in.descend()
	in.descend()
	in.cleared[in.node] = true // the entry node's set-piece falls
	down := trunkNodeAt(in.runSeed, 2)
	in.travelTo(down)
	in.descend()
	in.descend() // row 2's last floor
	if in.floor != 6 {
		t.Fatalf("floor = %d, want 6", in.floor)
	}

	// Travel back up to the visited entry node — no clear needed here,
	// visited is enough.
	a := in.sim.W.ActorByID(c.actor)
	a.Pos = in.stairs
	in.runTick(nil, runWants{travels: []travelWant{{c: c, to: first}}})
	if in.node != first || in.floor != 1 {
		t.Fatalf("after retreat: node=%v floor=%d, want %v/1", in.node, in.floor, first)
	}
}
