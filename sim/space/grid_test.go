package space_test

import (
	"testing"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// gridFromRows builds a finalized grid from an ASCII picture: '#' solid,
// anything else floor. Tile = 1 unit, clearance = 0.65 (the engine default).
func gridFromRows(rows []string) *space.Grid {
	g := space.NewGrid(len(rows[0]), len(rows), fm.One, fm.FromMilli(650))
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			g.SetSolid(x, y, row[x] == '#')
		}
	}
	g.Finalize()
	return g
}

func TestFitsAndErosion(t *testing.T) {
	g := gridFromRows([]string{
		"#######",
		"#.....#",
		"#.....#",
		"#.....#",
		"#.....#",
		"#.....#",
		"#######",
	})
	if !g.Fits(g.TileCenter(3, 3)) {
		t.Error("center of an open room should fit the clearance circle")
	}
	// A tile center 0.5 from a wall can't hold a 0.65 circle.
	if g.Fits(g.TileCenter(1, 1)) {
		t.Error("tile adjacent to a wall should not fit the clearance circle")
	}
	// The eroded layer feeds WalkableCenters: 5x5 floor erodes to 3x3.
	if got, want := len(g.WalkableCenters()), 9; got != want {
		t.Errorf("walkable centers = %d, want %d (5x5 floor eroded by one ring)", got, want)
	}
}

func TestSegmentHit(t *testing.T) {
	g := gridFromRows([]string{
		"#########",
		"#...#...#",
		"#...#...#",
		"#...#...#",
		"#########",
	})
	from := space.V(fm.FromMilli(1500), fm.FromMilli(2500))
	to := space.V(fm.FromMilli(7500), fm.FromMilli(2500))
	tt, hit := g.SegmentHit(from, to)
	if !hit {
		t.Fatal("segment through a wall column reported no hit")
	}
	// Wall starts at x=4; entry t = (4 - 1.5) / 6 ≈ 0.4166.
	if tt < fm.FromMilli(380) || tt > fm.FromMilli(450) {
		t.Errorf("wall entry t = %v, want ≈0.417", tt)
	}
	if _, hit := g.SegmentHit(from, space.V(fm.FromMilli(3500), fm.FromMilli(2500))); hit {
		t.Error("segment stopping short of the wall reported a hit")
	}
	if _, hit := g.SegmentHit(from, space.V(fm.FromMilli(1500), fm.FromMilli(1200))); hit {
		t.Error("segment inside one open tile column reported a hit")
	}
}

// pathRows is a room split by a wall with a 3-tile gap at the bottom —
// wide enough that erosion leaves a walkable line through it.
var pathRows = []string{
	"############",
	"#....#.....#",
	"#....#.....#",
	"#....#.....#",
	"#....#.....#",
	"#....#.....#",
	"#....#.....#",
	"#..........#",
	"#..........#",
	"#..........#",
	"############",
}

func TestFindPathAroundWall(t *testing.T) {
	g := gridFromRows(pathRows)
	from := g.TileCenter(2, 2)
	to := g.TileCenter(8, 2)
	path := g.FindPath(from, to)
	if len(path) == 0 {
		t.Fatal("no path found across the gap")
	}
	if got := path[len(path)-1]; got != to {
		t.Errorf("path ends at %v, want exact target %v", got, to)
	}
	// Every leg must be clear for the clearance circle.
	cur := from
	total := fm.Fixed(0)
	for _, wp := range path {
		if !g.ClearLine(cur, wp) {
			t.Errorf("path leg %v -> %v crosses a wall", cur, wp)
		}
		total += space.Dist(cur, wp)
		cur = wp
	}
	// Straight-line distance is 6; the detour through the gap is much longer.
	if total < fm.FromInt(10) {
		t.Errorf("path length %v suspiciously short — did it cut the wall?", total)
	}
}

func TestFindPathUnreachableWalksToClosestApproach(t *testing.T) {
	g := gridFromRows([]string{
		"##########",
		"#...##...#",
		"#...##...#",
		"#...##...#",
		"##########",
	})
	from := g.TileCenter(2, 2)
	to := g.TileCenter(7, 2) // sealed off
	path := g.FindPath(from, to)
	if len(path) == 0 {
		t.Fatal("unreachable target should still yield a closest-approach path")
	}
	end := path[len(path)-1]
	if end.X >= fm.FromInt(4) {
		t.Errorf("closest approach %v should stay on the near side of the divider", end)
	}
}

func TestFindPathDeterministic(t *testing.T) {
	g := gridFromRows(pathRows)
	from := g.TileCenter(2, 2)
	to := g.TileCenter(8, 2)
	a := g.FindPath(from, to)
	b := g.FindPath(from, to) // scratch reuse must not perturb results
	if len(a) != len(b) {
		t.Fatalf("path lengths differ across runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("waypoint %d differs across runs: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestNearestWalkable(t *testing.T) {
	g := gridFromRows(pathRows)
	inWall := space.V(fm.FromMilli(5500), fm.FromMilli(2500)) // inside the divider
	p, ok := g.NearestWalkable(inWall)
	if !ok {
		t.Fatal("grid has walkable tiles; NearestWalkable found none")
	}
	if !g.Fits(p) {
		t.Errorf("NearestWalkable returned %v where the clearance circle doesn't fit", p)
	}
}
