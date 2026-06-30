package space

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// Grid is a tile map: solid tiles are walls, everything else is floor. It
// implements Walkable for movement and answers the two other terrain
// questions the sim asks — "does this segment cross a wall?" (projectiles,
// line of sight) and "how do I get there?" (A* in astar.go).
//
// All terrain queries share one clearance radius rather than each actor's
// own: the grid guarantees a circle of radius Clearance fits wherever it
// says an actor may stand, so one eroded walkability layer serves every
// actor. Tile (tx, ty) covers [tx·Tile, (tx+1)·Tile) on each axis; the map
// lives in the positive quadrant and everything out of bounds is solid.
//
// A Grid is immutable after Finalize and safe to share across reads, but
// pathing scratch space (astar.go) makes FindPath single-goroutine — the
// same rule the rest of the sim already lives by.
type Grid struct {
	Width, Height int
	Tile          fm.Fixed // world units per tile edge
	Clearance     fm.Fixed // actor radius all walkability promises hold for
	// Spawn is where players enter (mapgen: first room's center).
	Spawn Vec2
	// Stairs is where a descent to the next floor happens (mapgen: last
	// room's center; equal to Spawn on a single-room map).
	Stairs Vec2

	solid []bool // row-major y*Width+x
	walk  []bool // eroded: a Clearance circle at the tile center fits
	words []uint64
	walkC []Vec2 // centers of walkable tiles, row-major order

	scratch *astarScratch
}

func NewGrid(w, h int, tile, clearance fm.Fixed) *Grid {
	g := &Grid{
		Width: w, Height: h,
		Tile: tile, Clearance: clearance,
		solid: make([]bool, w*h),
	}
	for i := range g.solid {
		g.solid[i] = true
	}
	return g
}

func (g *Grid) SetSolid(x, y int, solid bool) {
	if x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return
	}
	g.solid[y*g.Width+x] = solid
}

// Solid reports the tile state; out of bounds is solid.
func (g *Grid) Solid(x, y int) bool {
	if x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return true
	}
	return g.solid[y*g.Width+x]
}

// Finalize computes the derived layers (eroded walkability, hash words,
// walkable centers). Call once after carving; terrain is immutable after.
func (g *Grid) Finalize() {
	g.walk = make([]bool, len(g.solid))
	g.walkC = g.walkC[:0]
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			c := g.TileCenter(x, y)
			if !g.Solid(x, y) && g.Fits(c) {
				g.walk[y*g.Width+x] = true
				g.walkC = append(g.walkC, c)
			}
		}
	}
	g.words = make([]uint64, 0, len(g.solid)/64+3)
	g.words = append(g.words, uint64(g.Width), uint64(g.Height),
		uint64(g.Tile.Milli())<<32|uint64(g.Clearance.Milli()))
	var word uint64
	for i, s := range g.solid {
		if s {
			word |= 1 << (i % 64)
		}
		if i%64 == 63 {
			g.words = append(g.words, word)
			word = 0
		}
	}
	g.words = append(g.words, word)
	g.scratch = newAstarScratch(g.Width * g.Height)
}

// HashWords exposes the packed terrain for world hashing: terrain shapes
// behavior, so two worlds with different maps must never hash equal.
func (g *Grid) HashWords() []uint64 { return g.words }

// WalkableCenters returns the centers of all eroded-walkable tiles in
// row-major order — the deterministic pool scatter-spawning draws from.
func (g *Grid) WalkableCenters() []Vec2 { return g.walkC }

func (g *Grid) walkAt(x, y int) bool {
	if x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return false
	}
	return g.walk[y*g.Width+x]
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// TileAt maps a world position to tile coordinates (which may be out of
// bounds; bounds-checking is the callers' lookup functions' job).
func (g *Grid) TileAt(p Vec2) (int, int) {
	return int(floorDiv(p.X.Milli(), g.Tile.Milli())),
		int(floorDiv(p.Y.Milli(), g.Tile.Milli()))
}

func (g *Grid) TileCenter(x, y int) Vec2 {
	half := g.Tile / 2
	return Vec2{
		fm.Mul(fm.FromInt(int64(x)), g.Tile) + half,
		fm.Mul(fm.FromInt(int64(y)), g.Tile) + half,
	}
}

// Fits reports whether a circle of radius Clearance at p overlaps no solid
// tile — the standing-room test all walkability derives from.
func (g *Grid) Fits(p Vec2) bool {
	r := g.Clearance
	x0, y0 := g.TileAt(Vec2{p.X - r, p.Y - r})
	x1, y1 := g.TileAt(Vec2{p.X + r, p.Y + r})
	rsq := fm.Mul(r, r)
	for ty := y0; ty <= y1; ty++ {
		for tx := x0; tx <= x1; tx++ {
			if !g.Solid(tx, ty) {
				continue
			}
			minX := fm.Mul(fm.FromInt(int64(tx)), g.Tile)
			minY := fm.Mul(fm.FromInt(int64(ty)), g.Tile)
			dx := p.X - fm.Clamp(p.X, minX, minX+g.Tile)
			dy := p.Y - fm.Clamp(p.Y, minY, minY+g.Tile)
			if fm.Mul(dx, dx)+fm.Mul(dy, dy) < rsq {
				return false
			}
		}
	}
	return true
}

// ClearLine reports whether the Clearance circle can slide from from to to
// without touching a wall, by sampling Fits every half-clearance. from is
// assumed valid (it's where something already stands). Conservative against
// per-tick movement steps, which are far shorter than the sample interval.
func (g *Grid) ClearLine(from, to Vec2) bool {
	d := to.Sub(from)
	step := g.Clearance / 2
	if step <= 0 {
		step = g.Tile / 4
	}
	n := fm.Div(d.Len(), step).Int() + 1
	for i := int64(1); i <= n; i++ {
		t := fm.Div(fm.FromInt(i), fm.FromInt(n))
		if !g.Fits(from.Add(d.Scale(t))) {
			return false
		}
	}
	return true
}

// CanMove implements Walkable.
func (g *Grid) CanMove(from, to Vec2) bool { return g.ClearLine(from, to) }

// SegmentHit walks the segment through the tile raster (Amanatides & Woo
// in fixed point) and returns the parameter t in [0, One] where it first
// enters a solid tile. This is the zero-width test — projectiles and line
// of sight — as opposed to ClearLine's body-width one.
func (g *Grid) SegmentHit(from, to Vec2) (fm.Fixed, bool) {
	tx, ty := g.TileAt(from)
	if g.Solid(tx, ty) {
		return 0, true
	}
	ex, ey := g.TileAt(to)
	if tx == ex && ty == ey {
		return 0, false
	}
	d := to.Sub(from)

	const far = fm.Fixed(1) << 62
	stepX, stepY := 0, 0
	tMaxX, tMaxY, tDeltaX, tDeltaY := far, far, far, far
	if d.X != 0 {
		stepX = 1
		next := tx + 1
		if d.X < 0 {
			stepX, next = -1, tx
		}
		bx := fm.Mul(fm.FromInt(int64(next)), g.Tile)
		tMaxX = fm.Div(bx-from.X, d.X)
		tDeltaX = fm.Div(g.Tile, fm.Abs(d.X))
	}
	if d.Y != 0 {
		stepY = 1
		next := ty + 1
		if d.Y < 0 {
			stepY, next = -1, ty
		}
		by := fm.Mul(fm.FromInt(int64(next)), g.Tile)
		tMaxY = fm.Div(by-from.Y, d.Y)
		tDeltaY = fm.Div(g.Tile, fm.Abs(d.Y))
	}

	for {
		var t fm.Fixed
		if tMaxX < tMaxY {
			tx += stepX
			t = tMaxX
			tMaxX += tDeltaX
		} else {
			ty += stepY
			t = tMaxY
			tMaxY += tDeltaY
		}
		if t > fm.One {
			return 0, false
		}
		if g.Solid(tx, ty) {
			return fm.Clamp(t, 0, fm.One), true
		}
		if tx == ex && ty == ey {
			return 0, false
		}
	}
}

// pruneUnreachable drops eroded-walkable tiles 4-connected-unreachable from
// start. Rare generator artifact: a corridor grazing a room corner can leave
// a tile joined only diagonally, which the pathfinder's no-corner-cut rule
// can't traverse either (a diagonal needs both orthogonal neighbors open, so
// A*-reachable and 4-connected-reachable are the same set). Pruning keeps
// the invariant "every walkable tile is reachable from spawn" — scatter
// spawns and pathing both lean on it.
func (g *Grid) pruneUnreachable(start Vec2) {
	p, ok := g.NearestWalkable(start)
	if !ok {
		return
	}
	sx, sy := g.TileAt(p)
	reached := make([]bool, len(g.walk))
	queue := [][2]int{{sx, sy}}
	reached[sy*g.Width+sx] = true
	for i := 0; i < len(queue); i++ {
		x, y := queue[i][0], queue[i][1]
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]
			if !g.walkAt(nx, ny) || reached[ny*g.Width+nx] {
				continue
			}
			reached[ny*g.Width+nx] = true
			queue = append(queue, [2]int{nx, ny})
		}
	}
	g.walkC = g.walkC[:0]
	for i := range g.walk {
		g.walk[i] = g.walk[i] && reached[i]
		if g.walk[i] {
			g.walkC = append(g.walkC, g.TileCenter(i%g.Width, i/g.Width))
		}
	}
}

// NearestWalkable returns p if an actor fits there, else the center of the
// closest eroded-walkable tile by BFS (deterministic neighbor order). The
// second return is false only on a grid with no walkable tiles at all.
func (g *Grid) NearestWalkable(p Vec2) (Vec2, bool) {
	tx, ty := g.TileAt(p)
	if g.walkAt(tx, ty) && g.Fits(p) {
		return p, true
	}
	visited := make([]bool, g.Width*g.Height)
	queue := make([][2]int, 0, 64)
	push := func(x, y int) {
		if x < 0 || y < 0 || x >= g.Width || y >= g.Height || visited[y*g.Width+x] {
			return
		}
		visited[y*g.Width+x] = true
		queue = append(queue, [2]int{x, y})
	}
	push(clampInt(tx, 0, g.Width-1), clampInt(ty, 0, g.Height-1))
	for i := 0; i < len(queue); i++ {
		x, y := queue[i][0], queue[i][1]
		if g.walkAt(x, y) {
			return g.TileCenter(x, y), true
		}
		push(x+1, y)
		push(x-1, y)
		push(x, y+1)
		push(x, y-1)
	}
	return p, false
}
