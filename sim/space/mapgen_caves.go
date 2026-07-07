package space

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// Cave map generation — the second map kind (ROADMAP v2 Track 2 item 2:
// biomes). Cellular-automata caverns instead of rooms-and-corridors: seed
// the interior with noise, smooth it into blobs, erode by clearance, and
// keep the cavern the spawn can reach. Same Rand discipline as the rooms
// generator: all randomness through the injected stream, deterministic in
// the seed, no map iteration.

// MapCaves is the MapSpec.Kind value selecting this generator. The zero
// Kind ("") stays rooms-and-corridors, so every existing scenario, save,
// and golden is untouched.
const MapCaves = "caves"

const (
	caveOpenPct     = 58 // seed noise: interior tiles start open at this %
	caveSmoothIters = 4
	// caveMinWalkable guards degenerate seeds: after erosion and pruning,
	// a cave keeping fewer than 1/12 of its tiles walkable re-carves as a
	// rooms map instead (deterministic — the fallback is part of the same
	// draw sequence).
	caveMinWalkableDiv = 12
)

// GenerateCaves builds a cavern grid. Spawn is the walkable tile nearest
// the map center; everything unreachable from it is pruned, same invariant
// as the rooms generator ("every walkable tile is reachable from spawn").
func GenerateCaves(spec MapSpec, rng Rand) *Grid {
	if spec.Tile <= 0 {
		spec.Tile = fm.One
	}
	if spec.Clearance <= 0 {
		spec.Clearance = defaultClearance
	}
	w, h := spec.Width, spec.Height
	g := NewGrid(w, h, spec.Tile, spec.Clearance)

	// Seed noise on the interior; the border stays solid by construction.
	open := make([]bool, w*h)
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			open[y*w+x] = rng.Uint64n(100) < caveOpenPct
		}
	}
	// Smooth: an open tile needs 4 open neighbors to stay open, a solid
	// tile needs 6 to open up — the classic hysteresis that grows caverns
	// and seals pinholes. Out-of-interior neighbors count solid.
	for it := 0; it < caveSmoothIters; it++ {
		next := make([]bool, w*h)
		for y := 1; y < h-1; y++ {
			for x := 1; x < w-1; x++ {
				n := 0
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						if dx == 0 && dy == 0 {
							continue
						}
						if open[(y+dy)*w+(x+dx)] {
							n++
						}
					}
				}
				if open[y*w+x] {
					next[y*w+x] = n >= 4
				} else {
					next[y*w+x] = n >= 6
				}
			}
		}
		open = next
	}
	// Dilate once: clearance erosion (Fits needs a ~0.65u circle clear)
	// devours the craggy CA edge — fatten every cavern by one tile ring so
	// the eroded interior survives as real walkable ground.
	dilated := make([]bool, w*h)
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			dilated[y*w+x] = open[y*w+x] || open[y*w+x-1] || open[y*w+x+1] ||
				open[(y-1)*w+x] || open[(y+1)*w+x]
		}
	}
	open = dilated
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			if open[y*w+x] {
				g.SetSolid(x, y, false)
			}
		}
	}
	g.Finalize()

	wc := g.WalkableCenters()
	if len(wc) == 0 {
		return caveFallback(spec, rng)
	}
	center := g.TileCenter(w/2, h/2)
	best, bd := wc[0], Dist(wc[0], center)
	for _, p := range wc[1:] {
		if d := Dist(p, center); d < bd {
			best, bd = p, d
		}
	}
	g.Spawn = best
	g.pruneUnreachable(g.Spawn)
	if len(g.WalkableCenters()) < w*h/caveMinWalkableDiv {
		return caveFallback(spec, rng)
	}
	return g
}

// caveFallback re-carves a degenerate cave seed as a rooms map. Specs that
// never named a room count (caves don't need one) get a sane default so
// the fallback actually carves something.
func caveFallback(spec MapSpec, rng Rand) *Grid {
	if spec.Rooms <= 0 {
		spec.Rooms = max(4, spec.Width*spec.Height/256)
	}
	return GenerateRooms(spec, rng)
}
