package space_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/space"
)

var caveSpec = space.MapSpec{Width: 48, Height: 48, Kind: space.MapCaves}

func TestGenerateCavesDeterministic(t *testing.T) {
	a := space.GenerateCaves(caveSpec, &testRand{s: 42})
	b := space.GenerateCaves(caveSpec, &testRand{s: 42})
	for y := 0; y < a.Height; y++ {
		for x := 0; x < a.Width; x++ {
			if a.Solid(x, y) != b.Solid(x, y) {
				t.Fatalf("tile (%d,%d) differs between same-seed generations", x, y)
			}
		}
	}
	if a.Spawn != b.Spawn {
		t.Errorf("spawn differs between same-seed generations: %v vs %v", a.Spawn, b.Spawn)
	}
}

func TestGenerateCavesBorderSolid(t *testing.T) {
	g := space.GenerateCaves(caveSpec, &testRand{s: 7})
	for i := 0; i < g.Width; i++ {
		if !g.Solid(i, 0) || !g.Solid(i, g.Height-1) {
			t.Fatalf("border tile open at column %d", i)
		}
	}
	for i := 0; i < g.Height; i++ {
		if !g.Solid(0, i) || !g.Solid(g.Width-1, i) {
			t.Fatalf("border tile open at row %d", i)
		}
	}
}

// TestGenerateCavesConnectedAndRoomy: every eroded-walkable tile is
// reachable from the spawn (the invariant scatter and pathing lean on),
// and the cavern keeps enough open ground to hold a floor's packs —
// checked across many seeds, since CA output varies more than rooms do.
func TestGenerateCavesConnectedAndRoomy(t *testing.T) {
	for seed := uint64(1); seed <= 40; seed++ {
		g := space.GenerateCaves(caveSpec, &testRand{s: seed})
		centers := g.WalkableCenters()
		minWalk := caveSpec.Width * caveSpec.Height / 12
		if len(centers) < minWalk {
			t.Fatalf("seed %d: %d walkable tiles, want >= %d", seed, len(centers), minWalk)
		}
		// Flood from spawn over walkable tiles; count what we reach.
		sx, sy := g.TileAt(g.Spawn)
		type cell struct{ x, y int }
		seen := map[cell]bool{{sx, sy}: true}
		queue := []cell{{sx, sy}}
		walkable := func(x, y int) bool {
			if x < 0 || y < 0 || x >= g.Width || y >= g.Height {
				return false
			}
			return g.Fits(g.TileCenter(x, y))
		}
		if !walkable(sx, sy) {
			t.Fatalf("seed %d: spawn tile is not walkable", seed)
		}
		for i := 0; i < len(queue); i++ {
			c := queue[i]
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				n := cell{c.x + d[0], c.y + d[1]}
				if seen[n] || !walkable(n.x, n.y) {
					continue
				}
				seen[n] = true
				queue = append(queue, n)
			}
		}
		if len(seen) != len(centers) {
			t.Fatalf("seed %d: flood reached %d tiles, grid reports %d walkable — disconnected cavern",
				seed, len(seen), len(centers))
		}
	}
}

// TestCavesDifferFromRooms: the two generators produce different terrain
// for the same seed — the biome switch is real, not cosmetic.
func TestCavesDifferFromRooms(t *testing.T) {
	rooms := space.GenerateRooms(space.MapSpec{Width: 48, Height: 48, Rooms: 9}, &testRand{s: 5})
	caves := space.GenerateCaves(caveSpec, &testRand{s: 5})
	same := true
	for y := 0; y < rooms.Height && same; y++ {
		for x := 0; x < rooms.Width; x++ {
			if rooms.Solid(x, y) != caves.Solid(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatal("cave and room generation produced identical terrain")
	}
}
