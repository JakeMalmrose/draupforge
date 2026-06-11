package space_test

import (
	"testing"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// testRand is a tiny splitmix64 so these tests don't reach into sim/core
// (which sits above space in the import order).
type testRand struct{ s uint64 }

func (r *testRand) Uint64n(n uint64) uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return (z ^ (z >> 31)) % n
}

var testSpec = space.MapSpec{Width: 40, Height: 40, Rooms: 7}

func TestGenerateRoomsDeterministic(t *testing.T) {
	a := space.GenerateRooms(testSpec, &testRand{s: 42})
	b := space.GenerateRooms(testSpec, &testRand{s: 42})
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

func TestGenerateRoomsBorderSolid(t *testing.T) {
	g := space.GenerateRooms(testSpec, &testRand{s: 7})
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

// TestGenerateRoomsConnected: every eroded-walkable tile is reachable from
// the spawn — the guarantee that lets pathing and scatter-spawning assume
// one component. Checked across several seeds.
func TestGenerateRoomsConnected(t *testing.T) {
	for seed := uint64(1); seed <= 25; seed++ {
		g := space.GenerateRooms(testSpec, &testRand{s: seed})
		centers := g.WalkableCenters()
		if len(centers) == 0 {
			t.Fatalf("seed %d: map has no walkable tiles", seed)
		}
		if g.Spawn == (space.Vec2{}) {
			t.Fatalf("seed %d: no spawn point set", seed)
		}

		// Flood fill over walkable tiles from the spawn.
		start, ok := g.NearestWalkable(g.Spawn)
		if !ok {
			t.Fatalf("seed %d: spawn has no walkable tile", seed)
		}
		sx, sy := g.TileAt(start)
		visited := map[[2]int]bool{{sx, sy}: true}
		queue := [][2]int{{sx, sy}}
		for i := 0; i < len(queue); i++ {
			x, y := queue[i][0], queue[i][1]
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				c := g.TileCenter(nx, ny)
				if visited[[2]int{nx, ny}] || g.Solid(nx, ny) || !g.Fits(c) {
					continue
				}
				visited[[2]int{nx, ny}] = true
				queue = append(queue, [2]int{nx, ny})
			}
		}
		if len(visited) != len(centers) {
			t.Errorf("seed %d: %d of %d walkable tiles reachable from spawn — disconnected map",
				seed, len(visited), len(centers))
		}
	}
}

func TestGenerateRoomsDefaults(t *testing.T) {
	g := space.GenerateRooms(space.MapSpec{Width: 30, Height: 30, Rooms: 4}, &testRand{s: 3})
	if g.Tile != fm.One {
		t.Errorf("default tile = %v, want 1.0", g.Tile)
	}
	if g.Clearance != fm.FromMilli(650) {
		t.Errorf("default clearance = %v, want 0.65", g.Clearance)
	}
}
