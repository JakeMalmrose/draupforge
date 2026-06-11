package space

// A* over the grid's eroded walkability layer. Everything here is
// deterministic by construction: slice-backed state, a fixed neighbor
// order, and a heap that breaks f-score ties by insertion sequence.
//
// Costs and heuristic are integer milli-tiles (1000 straight, 1414
// diagonal; octile heuristic), so the search never touches Fixed math.

const (
	costStraight = 1000
	costDiagonal = 1414
)

// Fixed neighbor order — part of replay determinism.
var neighbors = [8][2]int{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
}

// astarScratch is reusable per-grid search state, sized once. Versioned
// visitation (gen counter) avoids clearing the arrays between searches.
// This is what makes FindPath single-goroutine, like the rest of the sim.
type astarScratch struct {
	g        []int64
	came     []int32
	gen      []uint32
	curGen   uint32
	open     nodeHeap
	pathBuf  []int32
	pointBuf []Vec2
}

func newAstarScratch(n int) *astarScratch {
	return &astarScratch{
		g:    make([]int64, n),
		came: make([]int32, n),
		gen:  make([]uint32, n),
	}
}

type heapNode struct {
	f   int64
	h   int64
	seq int32
	idx int32
}

// nodeHeap is a hand-rolled binary min-heap on (f, then insertion seq).
// The seq tie-break pins the expansion order — container/heap would be
// deterministic too, but the explicit ordering documents the contract.
type nodeHeap []heapNode

func (h nodeHeap) less(i, j int) bool {
	if h[i].f != h[j].f {
		return h[i].f < h[j].f
	}
	return h[i].seq < h[j].seq
}

func (h *nodeHeap) push(n heapNode) {
	*h = append(*h, n)
	i := len(*h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			break
		}
		(*h)[i], (*h)[p] = (*h)[p], (*h)[i]
		i = p
	}
}

func (h *nodeHeap) pop() heapNode {
	old := *h
	top := old[0]
	n := len(old) - 1
	old[0] = old[n]
	*h = old[:n]
	i := 0
	for {
		l, r := 2*i+1, 2*i+2
		s := i
		if l < n && h.less(l, s) {
			s = l
		}
		if r < n && h.less(r, s) {
			s = r
		}
		if s == i {
			break
		}
		(*h)[i], (*h)[s] = (*h)[s], (*h)[i]
		i = s
	}
	return top
}

func octile(ax, ay, bx, by int) int64 {
	dx, dy := ax-bx, ay-by
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dx < dy {
		dx, dy = dy, dx
	}
	return int64(dx)*costStraight + int64(dy)*(costDiagonal-costStraight)
}

// FindPath returns waypoints from from toward to, smoothed against the
// clearance circle. If to is unreachable (inside a wall, separate room
// component), the path leads to the closest approach instead — clicking a
// wall walks you up to it, PoE-style. An empty result means "no move at
// all is possible" (no walkable tile anywhere near from).
func (g *Grid) FindPath(from, to Vec2) []Vec2 {
	// Fast path: clear straight line, no search. Covers open rooms and
	// every-tick AI chases with line of sight.
	if g.Fits(to) && g.ClearLine(from, to) {
		return []Vec2{to}
	}

	sx, sy := g.TileAt(from)
	if !g.walkAt(sx, sy) {
		// Standing somewhere the eroded layer disowns (legal: erosion is
		// stricter than Fits). Search from the nearest walkable tile.
		p, ok := g.NearestWalkable(from)
		if !ok {
			return nil
		}
		sx, sy = g.TileAt(p)
	}
	gx, gy := g.TileAt(to)
	gx, gy = clampInt(gx, 0, g.Width-1), clampInt(gy, 0, g.Height-1)

	s := g.scratch
	s.curGen++
	s.open = s.open[:0]
	var seq int32

	start := int32(sy*g.Width + sx)
	goal := int32(gy*g.Width + gx)
	s.g[start] = 0
	s.came[start] = -1
	s.gen[start] = s.curGen
	h0 := octile(sx, sy, gx, gy)
	s.open.push(heapNode{f: h0, h: h0, seq: seq, idx: start})
	seq++

	// Track the closest approach for unreachable goals.
	bestIdx, bestH := start, h0

	reached := false
	for len(s.open) > 0 {
		n := s.open.pop()
		if n.idx == goal {
			bestIdx = n.idx
			reached = true
			break
		}
		// Stale heap entry (a cheaper g landed after this push)?
		if n.f-n.h > s.g[n.idx] {
			continue
		}
		if n.h < bestH {
			bestIdx, bestH = n.idx, n.h
		}
		x, y := int(n.idx)%g.Width, int(n.idx)/g.Width
		for _, d := range neighbors {
			nx, ny := x+d[0], y+d[1]
			if !g.walkAt(nx, ny) {
				continue
			}
			cost := int64(costStraight)
			if d[0] != 0 && d[1] != 0 {
				// No corner cutting: a diagonal needs both orthogonal
				// neighbors open so the clearance circle clears the corner.
				if !g.walkAt(x+d[0], y) || !g.walkAt(x, y+d[1]) {
					continue
				}
				cost = costDiagonal
			}
			ni := int32(ny*g.Width + nx)
			ng := s.g[n.idx] + cost
			if s.gen[ni] == s.curGen && ng >= s.g[ni] {
				continue
			}
			s.gen[ni] = s.curGen
			s.g[ni] = ng
			s.came[ni] = n.idx
			nh := octile(nx, ny, gx, gy)
			s.open.push(heapNode{f: ng + nh, h: nh, seq: seq, idx: ni})
			seq++
		}
	}

	// Reconstruct tile chain (reversed), then emit waypoint centers.
	s.pathBuf = s.pathBuf[:0]
	for i := bestIdx; i != -1; i = s.came[i] {
		s.pathBuf = append(s.pathBuf, i)
	}
	s.pointBuf = s.pointBuf[:0]
	for i := len(s.pathBuf) - 1; i >= 0; i-- {
		idx := s.pathBuf[i]
		s.pointBuf = append(s.pointBuf, g.TileCenter(int(idx)%g.Width, int(idx)/g.Width))
	}
	if reached && g.Fits(to) {
		// End on the exact request, not the tile center.
		s.pointBuf = append(s.pointBuf, to)
	}
	return g.smooth(from, s.pointBuf)
}

// smooth string-pulls the waypoint chain: from each anchor, keep extending
// to the furthest waypoint the clearance circle can slide straight to. The
// result is a fresh slice (Action state outlives the scratch buffers).
func (g *Grid) smooth(from Vec2, pts []Vec2) []Vec2 {
	if len(pts) == 0 {
		return nil
	}
	out := make([]Vec2, 0, 4)
	cur := from
	for i := 0; i < len(pts); {
		j := i
		for j+1 < len(pts) && g.ClearLine(cur, pts[j+1]) {
			j++
		}
		out = append(out, pts[j])
		cur = pts[j]
		i = j + 1
	}
	return out
}
