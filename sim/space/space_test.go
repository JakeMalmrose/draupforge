package space

import (
	"testing"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

func TestDist(t *testing.T) {
	a := V(0, 0)
	b := V(fm.FromInt(3), fm.FromInt(4))
	if got := Dist(a, b); got != fm.FromInt(5) {
		t.Errorf("Dist 3-4-5 = %d, want 5000", got)
	}
}

func TestNormalize(t *testing.T) {
	v := V(fm.FromInt(10), 0).Normalize()
	if v.X != fm.One || v.Y != 0 {
		t.Errorf("Normalize(10,0) = %+v, want (1000,0)", v)
	}
	z := V(0, 0).Normalize()
	if z.X != 0 || z.Y != 0 {
		t.Errorf("Normalize(0,0) = %+v, want zero", z)
	}
}

func TestSegCircleHit(t *testing.T) {
	// Segment along X axis passing right through a circle at (5, 0).
	p0, p1 := V(0, 0), V(fm.FromInt(10), 0)
	c := V(fm.FromInt(5), 0)
	if _, _, ok := SegCircleHit(p0, p1, c, fm.Half); !ok {
		t.Error("expected hit through circle center")
	}
	// Circle offset just within radius.
	c2 := V(fm.FromInt(5), fm.FromMilli(400))
	if _, _, ok := SegCircleHit(p0, p1, c2, fm.Half); !ok {
		t.Error("expected graze hit at 0.4 offset with 0.5 radius")
	}
	// Circle out of reach.
	c3 := V(fm.FromInt(5), fm.FromInt(2))
	if _, _, ok := SegCircleHit(p0, p1, c3, fm.Half); ok {
		t.Error("expected miss at 2.0 offset with 0.5 radius")
	}
	// Circle behind the segment start.
	c4 := V(-fm.FromInt(3), 0)
	if _, _, ok := SegCircleHit(p0, p1, c4, fm.Half); ok {
		t.Error("expected miss behind segment")
	}
}
