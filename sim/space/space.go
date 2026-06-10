// Package space provides fixed-point 2D geometry for the sim: vectors,
// distances, and the collision tests combat needs. Pathing slots in later
// behind the Walkable interface.
package space

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

type Vec2 struct {
	X, Y fm.Fixed
}

func V(x, y fm.Fixed) Vec2 { return Vec2{x, y} }

func (v Vec2) Add(o Vec2) Vec2 { return Vec2{v.X + o.X, v.Y + o.Y} }
func (v Vec2) Sub(o Vec2) Vec2 { return Vec2{v.X - o.X, v.Y - o.Y} }

func (v Vec2) Scale(k fm.Fixed) Vec2 {
	return Vec2{fm.Mul(v.X, k), fm.Mul(v.Y, k)}
}

func (v Vec2) LenSq() fm.Fixed {
	return fm.Mul(v.X, v.X) + fm.Mul(v.Y, v.Y)
}

func (v Vec2) Len() fm.Fixed { return fm.Sqrt(v.LenSq()) }

// Normalize returns a unit vector; the zero vector normalizes to zero.
func (v Vec2) Normalize() Vec2 {
	l := v.Len()
	if l == 0 {
		return Vec2{}
	}
	return Vec2{fm.Div(v.X, l), fm.Div(v.Y, l)}
}

func Dot(a, b Vec2) fm.Fixed {
	return fm.Mul(a.X, b.X) + fm.Mul(a.Y, b.Y)
}

func Dist(a, b Vec2) fm.Fixed { return b.Sub(a).Len() }

// SegCircleHit reports whether the segment p0→p1 passes within r of center c.
// It returns the closest point on the segment and its parameter t in [0, One].
// The closest point approximates the entry point — good enough for per-tick
// projectile sweeps where segments are short.
func SegCircleHit(p0, p1, c Vec2, r fm.Fixed) (Vec2, fm.Fixed, bool) {
	d := p1.Sub(p0)
	lsq := d.LenSq()
	var t fm.Fixed
	if lsq > 0 {
		t = fm.Clamp(fm.Div(Dot(c.Sub(p0), d), lsq), 0, fm.One)
	}
	p := p0.Add(d.Scale(t))
	if Dist(p, c) <= r {
		return p, t, true
	}
	return Vec2{}, 0, false
}

// Walkable is the seam where pathing/terrain plugs in. Movement consumers
// depend on this interface from day one so navgrids arrive without churn.
type Walkable interface {
	CanMove(from, to Vec2) bool
}

// OpenPlane is the v1 arena: everything is walkable.
type OpenPlane struct{}

func (OpenPlane) CanMove(_, _ Vec2) bool { return true }
