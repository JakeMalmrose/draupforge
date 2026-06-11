package space

import (
	"testing"
)

type lcg uint64

func (r *lcg) Uint64n(n uint64) uint64 {
	*r = *r*6364136223846793005 + 1442695040888963407
	return uint64(*r) % n
}

// TestGridSaveRoundTrip: a generated grid survives Encode → Decode exactly —
// hash words (solid layer), walkable centers (the eroded+pruned layer), and
// the metadata pathing and spawning read.
func TestGridSaveRoundTrip(t *testing.T) {
	rng := lcg(7)
	g := GenerateRooms(MapSpec{Width: 40, Height: 40, Rooms: 7}, &rng)

	r, err := DecodeGrid(g.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if r.Width != g.Width || r.Height != g.Height || r.Tile != g.Tile ||
		r.Clearance != g.Clearance || r.Spawn != g.Spawn {
		t.Fatal("grid metadata changed across save round-trip")
	}

	gw, rw := g.HashWords(), r.HashWords()
	if len(gw) != len(rw) {
		t.Fatalf("hash words: %d vs %d", len(gw), len(rw))
	}
	for i := range gw {
		if gw[i] != rw[i] {
			t.Fatalf("hash word %d differs: %016x != %016x", i, gw[i], rw[i])
		}
	}

	gc, rc := g.WalkableCenters(), r.WalkableCenters()
	if len(gc) != len(rc) {
		t.Fatalf("walkable centers: %d vs %d — the pruned walk layer must restore, not re-derive", len(gc), len(rc))
	}
	for i := range gc {
		if gc[i] != rc[i] {
			t.Fatalf("walkable center %d differs: %v != %v", i, gc[i], rc[i])
		}
	}
}

// TestGridSaveRejectsMalformed: dimension/row mismatches fail the decode.
func TestGridSaveRejectsMalformed(t *testing.T) {
	rng := lcg(7)
	g := GenerateRooms(MapSpec{Width: 8, Height: 8, Rooms: 1}, &rng)

	s := g.Encode()
	s.Rows = s.Rows[:len(s.Rows)-1]
	if _, err := DecodeGrid(s); err == nil {
		t.Error("decode accepted a save missing a row")
	}

	s = g.Encode()
	s.Rows[0] = s.Rows[0][:len(s.Rows[0])-1]
	if _, err := DecodeGrid(s); err == nil {
		t.Error("decode accepted a save with a short row")
	}
}
