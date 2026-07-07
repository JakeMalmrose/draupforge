// The delve chart — the descent is a map now (PoE1-Delve style). A run owns
// a lattice of nodes: rows are depth, columns are lateral position, edges
// connect neighbors down, up, and sideways. Each node is a 3-floor dungeon
// of one biome ending in a set-piece; clearing it (killing the set-piece)
// unlocks travel to its neighbors, so you steer the descent — deeper,
// sideways to hold a comfortable depth, or back up through cleared ground.
//
// Everything here is a pure function of (run seed, address), like the floor
// mods: no world RNG, no state — a death-eject rebuild, a chart preview,
// and the floor build all agree by construction. Biomes are spatial clumps:
// coarse cells seed jittered "blob" centers with depth-weighted biomes, and
// a node takes its nearest center's biome, so the map reads as regions
// rather than bands.
package server

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// delveCols is the lattice width; delveEntryCol is where the run enters
// (row 1's forced node, under the hideout portal).
const (
	delveCols     = 7
	delveEntryCol = 3
)

// nodeAddr names one node on the delve chart. Rows start at 1 (the row
// under the hideout); cols run 0..delveCols-1.
type nodeAddr struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

func (n nodeAddr) String() string { return fmt.Sprintf("%d:%d", n.Row, n.Col) }

// globalFloor is the difficulty axis: node (row, fin) is this deep in the
// old linear-floor terms. Monster levels, rarity pressure, item levels, the
// ladder, and checkpoints all keep counting in global floors.
func globalFloor(row, fin int) int { return (row-1)*3 + fin }

// nodeRowOf inverts globalFloor: which node row holds a global floor.
func nodeRowOf(floor int) int { return (floor + 2) / 3 }

// finOf: which floor within its node (1..3) a global floor is.
func finOf(floor int) int { return (floor-1)%3 + 1 }

// Derivation salts, one namespace per question the map answers. Distinct
// from floorModSalt's 0xF100D... space.
const (
	delveRowSalt  uint64 = 0xDE15E_0001_00000
	delveDownSalt uint64 = 0xDE15E_0002_00000
	delveLatSalt  uint64 = 0xDE15E_0003_00000
	delveCellSalt uint64 = 0xDE15E_0004_00000
	delveNodeSalt uint64 = 0xDE15E_0005_00000
)

func delveSalt(base uint64, a, b int) uint64 {
	return base ^ uint64(uint32(a))<<7 ^ uint64(uint32(b))
}

// delveRow lists the occupied columns of a row, ascending. Rows hold 3–5
// nodes; row 1 always includes the entry column.
func delveRow(runSeed uint64, row int) []int {
	if row < 1 {
		return nil
	}
	st := deriveSeed(runSeed, delveSalt(delveRowSalt, row, 0))
	count := 3 + int(core.SplitMix64(&st)%3)
	cols := make([]int, delveCols)
	for i := range cols {
		cols[i] = i
	}
	// Partial Fisher-Yates: the first count entries are the picks.
	for k := 0; k < count; k++ {
		j := k + int(core.SplitMix64(&st)%uint64(delveCols-k))
		cols[k], cols[j] = cols[j], cols[k]
	}
	picks := cols[:count]
	if row == 1 {
		has := false
		for _, c := range picks {
			if c == delveEntryCol {
				has = true
			}
		}
		if !has {
			picks[0] = delveEntryCol
		}
	}
	// Insertion sort — picks are tiny and the order must be canonical.
	for i := 1; i < len(picks); i++ {
		for j := i; j > 0 && picks[j] < picks[j-1]; j-- {
			picks[j], picks[j-1] = picks[j-1], picks[j]
		}
	}
	return picks
}

// nearestCol picks the entry of cols closest to c (ties toward the smaller
// column). cols must be non-empty and ascending.
func nearestCol(cols []int, c int) int {
	best, bd := cols[0], abs(cols[0]-c)
	for _, cc := range cols[1:] {
		if d := abs(cc - c); d < bd {
			best, bd = cc, d
		}
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// downEdges derives the edges between row and row+1 as [up-col, down-col]
// pairs. Every node above gets its nearest node below; candidates within
// one column roll for extras (drawn unconditionally in a fixed order, so
// the stream never shifts); every node below is guaranteed an edge up.
func downEdges(runSeed uint64, row int) [][2]int {
	up, down := delveRow(runSeed, row), delveRow(runSeed, row+1)
	st := deriveSeed(runSeed, delveSalt(delveDownSalt, row, 0))
	have := make(map[[2]int]bool) // lookup only, never iterated
	var edges [][2]int
	add := func(u, d int) {
		k := [2]int{u, d}
		if !have[k] {
			have[k] = true
			edges = append(edges, k)
		}
	}
	for _, u := range up {
		add(u, nearestCol(down, u))
	}
	for _, u := range up {
		for _, d := range down {
			if abs(u-d) <= 1 {
				if core.SplitMix64(&st)%100 < 45 {
					add(u, d)
				}
			}
		}
	}
	for _, d := range down {
		linked := false
		for _, e := range edges {
			if e[1] == d {
				linked = true
				break
			}
		}
		if !linked {
			add(nearestCol(up, d), d)
		}
	}
	return edges
}

// lateralEdges derives the same-row edges as [col, col] pairs: consecutive
// occupied columns no more than 3 apart connect at 65% — the sideways
// corridors that hold a depth.
func lateralEdges(runSeed uint64, row int) [][2]int {
	cols := delveRow(runSeed, row)
	st := deriveSeed(runSeed, delveSalt(delveLatSalt, row, 0))
	var edges [][2]int
	for i := 0; i+1 < len(cols); i++ {
		roll := core.SplitMix64(&st) % 100 // drawn per gap, taken or not
		if cols[i+1]-cols[i] <= 3 && roll < 65 {
			edges = append(edges, [2]int{cols[i], cols[i+1]})
		}
	}
	return edges
}

// delveNeighbors lists a node's edges: sideways in its row, up into row-1,
// down into row+1. Order is canonical (laterals, then up, then down).
func delveNeighbors(runSeed uint64, n nodeAddr) []nodeAddr {
	var out []nodeAddr
	for _, e := range lateralEdges(runSeed, n.Row) {
		if e[0] == n.Col {
			out = append(out, nodeAddr{n.Row, e[1]})
		}
		if e[1] == n.Col {
			out = append(out, nodeAddr{n.Row, e[0]})
		}
	}
	if n.Row > 1 {
		for _, e := range downEdges(runSeed, n.Row-1) {
			if e[1] == n.Col {
				out = append(out, nodeAddr{n.Row - 1, e[0]})
			}
		}
	}
	for _, e := range downEdges(runSeed, n.Row) {
		if e[0] == n.Col {
			out = append(out, nodeAddr{n.Row + 1, e[1]})
		}
	}
	return out
}

// nodeExists: is there a node at this address?
func nodeExists(runSeed uint64, n nodeAddr) bool {
	if n.Row < 1 || n.Col < 0 || n.Col >= delveCols {
		return false
	}
	for _, c := range delveRow(runSeed, n.Row) {
		if c == n.Col {
			return true
		}
	}
	return false
}

// trunkNodeAt is the row's node nearest the entry column — where a deep
// start lands, and where legacy StartFloor configs map.
func trunkNodeAt(runSeed uint64, row int) nodeAddr {
	return nodeAddr{row, nearestCol(delveRow(runSeed, row), delveEntryCol)}
}

// Biome clumps. The map is tiled by coarse cells (cellCols wide, cellRows
// tall); each cell seeds one jittered blob center carrying a depth-weighted
// biome. A node's biome is its nearest center among the 3×3 neighboring
// cells — Voronoi regions, so biomes read as clumps with ragged borders.
const (
	cellCols = 4 // columns per biome cell
	cellRows = 5 // rows per biome cell
)

// blobCenter returns a cell's center in milli-(col,row) space and its biome.
func blobCenter(runSeed uint64, cx, cy int) (mx, my int64, biome *biomeDef) {
	st := deriveSeed(runSeed, delveSalt(delveCellSalt, cx, cy))
	mx = int64(cx)*int64(cellCols)*1000 + int64(core.SplitMix64(&st)%uint64(cellCols*1000))
	my = int64(cy)*int64(cellRows)*1000 + int64(core.SplitMix64(&st)%uint64(cellRows*1000))
	// Depth-weighted biome: the crypt owns the surface and fades by ~row 10,
	// the caves ramp in from row 3 and never leave, the frost starts at 7
	// and dominates the deeps. Weights are open for tuning.
	d := int(my / 1000)
	wCrypt := 30 - 4*max(0, d-2)
	if wCrypt < 0 {
		wCrypt = 0
	}
	wCaves := 0
	if d >= 3 {
		wCaves = min(6*(d-2), 24)
	}
	wFrost := 0
	if d >= 7 {
		wFrost = min(7*(d-6), 42)
	}
	total := wCrypt + wCaves + wFrost
	if total == 0 {
		return mx, my, biomeByID("crypt")
	}
	roll := int(core.SplitMix64(&st) % uint64(total))
	switch {
	case roll < wCrypt:
		biome = biomeByID("crypt")
	case roll < wCrypt+wCaves:
		biome = biomeByID("caves")
	default:
		biome = biomeByID("frost")
	}
	return mx, my, biome
}

// delveBiome: the biome of a node — nearest blob center in the 3×3 cell
// neighborhood, in a metric that makes cells roughly isotropic. The first
// two rows are always the crypt, whatever the blobs say: the early game
// must read as it always did.
func delveBiome(runSeed uint64, n nodeAddr) *biomeDef {
	if n.Row <= 2 {
		return biomeByID("crypt")
	}
	nx, ny := int64(n.Col)*1000+500, int64(n.Row)*1000+500
	ccx, ccy := n.Col/cellCols, (n.Row-1)/cellRows
	var best *biomeDef
	bd := int64(-1)
	for cy := ccy - 1; cy <= ccy+1; cy++ {
		if cy < 0 {
			continue
		}
		for cx := ccx - 1; cx <= ccx+1; cx++ {
			if cx < 0 || cx*cellCols >= delveCols {
				continue
			}
			mx, my, b := blobCenter(runSeed, cx, cy)
			dx, dy := nx-mx, ny-my
			// Normalize by cell shape: dx/cellCols² + dy/cellRows², scaled
			// integer (×400 with cells 4×5 → 25dx² + 16dy²).
			d := 25*dx*dx + 16*dy*dy
			if bd < 0 || d < bd {
				bd, best = d, b
			}
		}
	}
	return best
}

// modCountForRow: how many modifiers a node rolls — the depth scaling, in
// node rows (matching the old global-floor bands: rows 2–3 were floors
// 4–9, rows 4–6 floors 10–18, row 7+ floor 19 down).
func modCountForRow(row int) int {
	switch {
	case row < 2:
		return 0
	case row < 4:
		return 1
	case row < 7:
		return 2
	default:
		return 3
	}
}

// delveNodeMods rolls a node's modifiers — shared by all three of its
// floors, shown on the chart before you commit. One node in five is
// "juiced": one extra mod, and juicedNode reports it for the chart's pips.
func delveNodeMods(runSeed uint64, n nodeAddr) []floorModDef {
	count := modCountForRow(n.Row)
	if juicedNode(runSeed, n) {
		count++
	}
	st := deriveSeed(runSeed, delveSalt(delveNodeSalt, n.Row, n.Col))
	core.SplitMix64(&st) // the juice roll, consumed in juicedNode's order
	return rollModsFrom(&st, count)
}

// juicedNode: does this node roll the extra mod (and pay extra rarity)?
func juicedNode(runSeed uint64, n nodeAddr) bool {
	if modCountForRow(n.Row) == 0 {
		return false // the first row stays clean, juice included
	}
	st := deriveSeed(runSeed, delveSalt(delveNodeSalt, n.Row, n.Col))
	return core.SplitMix64(&st)%100 < 20
}

// delveSnap builds the chart as the wire sees it: everywhere the run has
// been, its immediate neighborhood in full (biome, mods, travelability),
// and one ring further as veiled silhouettes — the fog of the deep. Node
// order is canonical (rows ascending, columns ascending) — visited/cleared
// live in maps, but this never iterates them.
func (in *Instance) delveSnap(kind string) *protocol.DelveSnap {
	ds := &protocol.DelveSnap{Kind: kind, Row: in.node.Row, Col: in.node.Col}
	full := func(n nodeAddr) bool {
		if in.visited[n] {
			return true
		}
		for _, nb := range delveNeighbors(in.runSeed, n) {
			if in.visited[nb] {
				return true
			}
		}
		return false
	}
	included := make(map[nodeAddr]int) // 1 full, 2 veiled; lookup only
	var order []nodeAddr
	for row := 1; row <= in.maxRow+2; row++ {
		for _, col := range delveRow(in.runSeed, row) {
			n := nodeAddr{row, col}
			if full(n) {
				included[n] = 1
				order = append(order, n)
				continue
			}
			for _, nb := range delveNeighbors(in.runSeed, n) {
				if full(nb) {
					included[n] = 2
					order = append(order, n)
					break
				}
			}
		}
	}
	for _, n := range order {
		sn := protocol.DelveNodeSnap{
			Row: n.Row, Col: n.Col,
			Visited: in.visited[n], Cleared: in.cleared[n], Veiled: included[n] == 2,
		}
		if b := delveBiome(in.runSeed, n); b != nil {
			sn.Biome = b.ID
		}
		if included[n] == 1 {
			for _, m := range delveNodeMods(in.runSeed, n) {
				sn.Mods = append(sn.Mods, protocol.FloorModSnap{Name: m.Name, Reward: m.Reward})
			}
			sn.CanGo = in.canTravelTo(n)
		}
		// Each edge rides once, on its lesser endpoint, toward nodes the
		// snap itself includes.
		for _, nb := range delveNeighbors(in.runSeed, n) {
			if included[nb] != 0 && (nb.Row > n.Row || (nb.Row == n.Row && nb.Col > n.Col)) {
				sn.Edges = append(sn.Edges, nb.String())
			}
		}
		ds.Nodes = append(ds.Nodes, sn)
	}
	return ds
}
