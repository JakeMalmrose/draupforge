package space

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// Rooms-and-corridors map generation. The generator consumes randomness
// through the Rand interface so it stays a leaf package; the sim feeds it
// the world's dedicated map stream (RNGMap), which keeps map rolls from
// reshuffling combat/loot/AI streams in replays.

// Rand is the slice of core.RNG mapgen needs.
type Rand interface {
	Uint64n(n uint64) uint64
}

// MapSpec sizes a generated map. Zero Tile/Clearance take the defaults:
// 1-unit tiles, clearance just over the fattest actor (zombie, 0.6).
type MapSpec struct {
	Width, Height int // tiles
	Rooms         int // target room count; placement gives up gracefully
	Tile          fm.Fixed
	Clearance     fm.Fixed
}

const (
	defaultClearance = fm.Fixed(650)
	minRoomSide      = 4
	maxRoomSide      = 9
	// Corridors are carved 3 tiles wide: after Clearance erosion that
	// leaves exactly the center line walkable, so rooms always connect.
	corridorHalf = 1
)

type room struct{ x, y, w, h int }

func (r room) centerTile() (int, int) { return r.x + r.w/2, r.y + r.h/2 }

func (r room) overlaps(o room, gap int) bool {
	return r.x-gap < o.x+o.w && o.x-gap < r.x+r.w &&
		r.y-gap < o.y+o.h && o.y-gap < r.y+r.h
}

// GenerateRooms builds a fully solid grid, carves non-overlapping rooms,
// and links each room to the previous with an L-corridor — a connected
// dungeon by construction (the mapgen test flood-fills to prove it).
// Spawn is the first room's center.
func GenerateRooms(spec MapSpec, rng Rand) *Grid {
	if spec.Tile <= 0 {
		spec.Tile = fm.One
	}
	if spec.Clearance <= 0 {
		spec.Clearance = defaultClearance
	}
	g := NewGrid(spec.Width, spec.Height, spec.Tile, spec.Clearance)

	var rooms []room
	attempts := spec.Rooms * 8
	for try := 0; try < attempts && len(rooms) < spec.Rooms; try++ {
		w := minRoomSide + int(rng.Uint64n(maxRoomSide-minRoomSide+1))
		h := minRoomSide + int(rng.Uint64n(maxRoomSide-minRoomSide+1))
		if spec.Width-w-2 < 1 || spec.Height-h-2 < 1 {
			continue // map too small for this room
		}
		r := room{
			x: 1 + int(rng.Uint64n(uint64(spec.Width-w-2))),
			y: 1 + int(rng.Uint64n(uint64(spec.Height-h-2))),
			w: w, h: h,
		}
		clash := false
		for _, o := range rooms {
			if r.overlaps(o, 1) {
				clash = true
				break
			}
		}
		if clash {
			continue
		}
		rooms = append(rooms, r)
		g.carveRect(r.x, r.y, r.w, r.h)
	}

	for i := 1; i < len(rooms); i++ {
		ax, ay := rooms[i-1].centerTile()
		bx, by := rooms[i].centerTile()
		if rng.Uint64n(2) == 0 {
			g.carveCorridorH(ax, bx, ay)
			g.carveCorridorV(ay, by, bx)
		} else {
			g.carveCorridorV(ay, by, ax)
			g.carveCorridorH(ax, bx, by)
		}
	}

	if len(rooms) > 0 {
		cx, cy := rooms[0].centerTile()
		g.Spawn = g.TileCenter(cx, cy)
	}
	g.Finalize()
	g.pruneUnreachable(g.Spawn)
	return g
}

// carve helpers keep the outer border solid by construction.

func (g *Grid) carveRect(x, y, w, h int) {
	for ty := y; ty < y+h; ty++ {
		for tx := x; tx < x+w; tx++ {
			if tx >= 1 && ty >= 1 && tx < g.Width-1 && ty < g.Height-1 {
				g.SetSolid(tx, ty, false)
			}
		}
	}
}

func (g *Grid) carveCorridorH(x0, x1, y int) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	g.carveRect(x0-corridorHalf, y-corridorHalf, x1-x0+1+2*corridorHalf, 1+2*corridorHalf)
}

func (g *Grid) carveCorridorV(y0, y1, x int) {
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	g.carveRect(x-corridorHalf, y0-corridorHalf, 1+2*corridorHalf, y1-y0+1+2*corridorHalf)
}
