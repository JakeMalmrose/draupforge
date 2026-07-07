package server

// Delve-chart generation unit tests: deterministic lattices, guaranteed
// connectivity, the entry node, and the reveal fog on the wire snap.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
)

func TestDelveRowsDeterministicAndBounded(t *testing.T) {
	for seed := uint64(1); seed <= 30; seed++ {
		for row := 1; row <= 30; row++ {
			a, b := delveRow(seed, row), delveRow(seed, row)
			if len(a) < 3 || len(a) > 5 {
				t.Fatalf("seed %d row %d holds %d nodes, want 3–5", seed, row, len(a))
			}
			for i := range a {
				if a[i] != b[i] {
					t.Fatalf("seed %d row %d not deterministic: %v vs %v", seed, row, a, b)
				}
				if a[i] < 0 || a[i] >= delveCols {
					t.Fatalf("seed %d row %d column %d out of the lattice", seed, row, a[i])
				}
				if i > 0 && a[i] <= a[i-1] {
					t.Fatalf("seed %d row %d not ascending: %v", seed, row, a)
				}
			}
		}
	}
}

func TestDelveRowOneHoldsTheEntry(t *testing.T) {
	for seed := uint64(1); seed <= 50; seed++ {
		if !nodeExists(seed, nodeAddr{1, delveEntryCol}) {
			t.Fatalf("seed %d row 1 lacks the entry column: %v", seed, delveRow(seed, 1))
		}
	}
}

// TestDelveEdgesConnectEveryRow: every node has at least one edge into the
// row above (so the whole chart hangs off row 1) and at least one into the
// row below (so no node is a hard dead end going down).
func TestDelveEdgesConnectEveryRow(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		for row := 1; row <= 20; row++ {
			edges := downEdges(seed, row)
			up, down := delveRow(seed, row), delveRow(seed, row+1)
			for _, u := range up {
				found := false
				for _, e := range edges {
					if e[0] == u {
						found = true
					}
				}
				if !found {
					t.Fatalf("seed %d node %d:%d has no way down", seed, row, u)
				}
			}
			for _, d := range down {
				found := false
				for _, e := range edges {
					if e[1] == d {
						found = true
					}
				}
				if !found {
					t.Fatalf("seed %d node %d:%d unreachable from above", seed, row+1, d)
				}
			}
		}
	}
}

// TestDelveNeighborsAreSymmetric: n lists m iff m lists n — the map draws
// and validates travel off the same undirected graph.
func TestDelveNeighborsAreSymmetric(t *testing.T) {
	seed := uint64(9)
	lists := func(from, to nodeAddr) bool {
		for _, nb := range delveNeighbors(seed, from) {
			if nb == to {
				return true
			}
		}
		return false
	}
	for row := 1; row <= 12; row++ {
		for _, col := range delveRow(seed, row) {
			n := nodeAddr{row, col}
			for _, nb := range delveNeighbors(seed, n) {
				if !nodeExists(seed, nb) {
					t.Fatalf("node %v lists a neighbor that doesn't exist: %v", n, nb)
				}
				if !lists(nb, n) {
					t.Fatalf("edge %v→%v not symmetric", n, nb)
				}
			}
		}
	}
}

// TestDelveSnapFog: a fresh run reveals the entry node's neighborhood in
// full and one ring further as veiled silhouettes — nothing beyond, mods on
// full nodes only.
func TestDelveSnapFog(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	ds := in.delveSnap("")
	if ds.Row != in.node.Row || ds.Col != in.node.Col {
		t.Errorf("snap current = %d:%d, want %v", ds.Row, ds.Col, in.node)
	}
	byAddr := map[nodeAddr]int{} // 1 full, 2 veiled
	for _, n := range ds.Nodes {
		state := 1
		if n.Veiled {
			state = 2
		}
		byAddr[nodeAddr{n.Row, n.Col}] = state
		if n.Veiled && (len(n.Mods) > 0 || n.CanGo) {
			t.Errorf("veiled node %d:%d leaks mods or travel", n.Row, n.Col)
		}
		if n.Biome == "" {
			t.Errorf("node %d:%d has no biome silhouette", n.Row, n.Col)
		}
	}
	if byAddr[in.node] != 1 {
		t.Fatal("current node missing from its own snap")
	}
	for _, nb := range delveNeighbors(in.runSeed, in.node) {
		if byAddr[nb] != 1 {
			t.Errorf("neighbor %v of the visited node not fully revealed", nb)
		}
	}
	// Nothing beyond maxRow+2 leaks.
	for a := range byAddr {
		if a.Row > in.maxRow+2 {
			t.Errorf("node %v revealed beyond the fog bound", a)
		}
	}
}

// TestDelveSnapGrowsWithClears: clearing the current node turns its
// unvisited neighbors travelable.
func TestDelveSnapGrowsWithClears(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	in.fin = nodeFloors
	in.floor = globalFloor(in.node.Row, in.fin)
	canGo := func(ds *protocol.DelveSnap, to nodeAddr) bool {
		for _, n := range ds.Nodes {
			if (nodeAddr{n.Row, n.Col}) == to {
				return n.CanGo
			}
		}
		return false
	}
	var next nodeAddr
	for _, nb := range delveNeighbors(in.runSeed, in.node) {
		if !in.visited[nb] {
			next = nb
			break
		}
	}
	if next.Row == 0 {
		t.Fatal("entry node has no unvisited neighbor")
	}
	if canGo(in.delveSnap(""), next) {
		t.Fatal("frontier open before the clear")
	}
	in.cleared[in.node] = true
	if !canGo(in.delveSnap(""), next) {
		t.Fatal("frontier still barred after the clear")
	}
}
