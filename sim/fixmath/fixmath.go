// Package fixmath provides the fixed-point arithmetic used for all gameplay
// math. Fixed carries three decimal places (milli-units). Determinism across
// architectures is the whole point — floats never enter the sim.
package fixmath

import "math/bits"

type Fixed int64

const (
	// Scale is the number of milli-units per whole unit.
	Scale Fixed = 1000
	One   Fixed = Scale
	Half  Fixed = Scale / 2
)

// FromInt converts a whole number to Fixed.
func FromInt(n int64) Fixed { return Fixed(n) * Scale }

// FromMilli wraps a raw milli-unit count (1500 → 1.5).
func FromMilli(n int64) Fixed { return Fixed(n) }

// Int truncates toward zero to whole units.
func (f Fixed) Int() int64 { return int64(f / Scale) }

// Milli returns the raw milli-unit count.
func (f Fixed) Milli() int64 { return int64(f) }

// Mul multiplies two Fixed values through a 128-bit intermediate, so the
// product can never silently wrap (RISKS.md #2 — the stat evaluator's
// multiplier chain is where power creep lands first). A result outside the
// Fixed range panics: determinism tests catch it loudly instead of the sim
// shipping corrupted damage.
func Mul(a, b Fixed) Fixed {
	ua, ub := uint64(a), uint64(b)
	if a < 0 {
		ua = -ua
	}
	if b < 0 {
		ub = -ub
	}
	hi, lo := bits.Mul64(ua, ub)
	if hi >= uint64(Scale) {
		panic("fixmath: Mul overflow")
	}
	q, _ := bits.Div64(hi, lo, uint64(Scale))
	if (a < 0) != (b < 0) {
		if q > 1<<63 {
			panic("fixmath: Mul overflow")
		}
		return -Fixed(q) // q == 1<<63 negates to MinInt64 exactly
	}
	if q > 1<<63-1 {
		panic("fixmath: Mul overflow")
	}
	return Fixed(q)
}

// Div divides a by b. Panics on division by zero: a zero divisor in the sim
// is always a logic bug, and silently returning 0 would hide it.
func Div(a, b Fixed) Fixed {
	if b == 0 {
		panic("fixmath: division by zero")
	}
	return a * Scale / b
}

func Min(a, b Fixed) Fixed {
	if a < b {
		return a
	}
	return b
}

func Max(a, b Fixed) Fixed {
	if a > b {
		return a
	}
	return b
}

func Clamp(f, lo, hi Fixed) Fixed {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}

func Abs(f Fixed) Fixed {
	if f < 0 {
		return -f
	}
	return f
}

// Sqrt returns the square root, rounded down to the nearest milli-unit.
// Non-positive inputs return 0.
func Sqrt(f Fixed) Fixed {
	if f <= 0 {
		return 0
	}
	// value v = f/1000; sqrt(v) in milli-units = sqrt(f*1000).
	return Fixed(isqrt(uint64(f) * uint64(Scale)))
}

// isqrt is integer Newton's method: returns floor(sqrt(n)).
func isqrt(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	x := uint64(1) << ((bits.Len64(n) + 1) / 2)
	for {
		y := (x + n/x) / 2
		if y >= x {
			return x
		}
		x = y
	}
}
