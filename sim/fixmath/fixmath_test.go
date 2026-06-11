package fixmath

import "testing"

func TestBasicArithmetic(t *testing.T) {
	if got := Mul(FromMilli(1500), FromInt(2)); got != FromInt(3) {
		t.Errorf("1.5 * 2 = %d, want 3000", got)
	}
	if got := Div(FromInt(3), FromInt(2)); got != FromMilli(1500) {
		t.Errorf("3 / 2 = %d, want 1500", got)
	}
	if got := Mul(FromInt(100), FromMilli(250)); got != FromInt(25) {
		t.Errorf("100 * 0.25 = %d, want 25000", got)
	}
	if got := FromMilli(2999).Int(); got != 2 {
		t.Errorf("2.999 truncated = %d, want 2", got)
	}
}

func TestSqrt(t *testing.T) {
	cases := []struct{ in, want Fixed }{
		{FromInt(0), 0},
		{FromInt(1), FromInt(1)},
		{FromInt(4), FromInt(2)},
		{FromInt(9), FromInt(3)},
		{FromMilli(2250), FromMilli(1500)}, // sqrt(2.25) = 1.5
		{FromInt(2), FromMilli(1414)},      // sqrt(2) ≈ 1.414
		{FromInt(1000000), FromInt(1000)},
		{-FromInt(4), 0},
	}
	for _, c := range cases {
		if got := Sqrt(c.in); got != c.want {
			t.Errorf("Sqrt(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestMulWideIntermediate exercises products whose raw a*b would wrap an
// int64 intermediate (the pre-bits.Mul64 implementation corrupted these
// silently) but whose result still fits.
func TestMulWideIntermediate(t *testing.T) {
	// 5e12 * 3 = 1.5e13 units; raw milli product 1.5e19 > MaxInt64.
	a, b := FromInt(5_000_000_000_000), FromInt(3)
	if got := Mul(a, b); got != FromInt(15_000_000_000_000) {
		t.Errorf("Mul wide = %d, want 1.5e13 units", got)
	}
	if got := Mul(-a, b); got != -FromInt(15_000_000_000_000) {
		t.Errorf("Mul wide negative = %d", got)
	}
	if got := Mul(-a, -b); got != FromInt(15_000_000_000_000) {
		t.Errorf("Mul wide double-negative = %d", got)
	}
}

func TestMulOverflowPanics(t *testing.T) {
	cases := []struct{ a, b Fixed }{
		{FromInt(4_000_000_000_000), FromInt(4_000_000_000)},   // positive overflow
		{-FromInt(4_000_000_000_000), FromInt(4_000_000_000)},  // negative overflow
		{Fixed(1<<63 - 1), Fixed(1<<63 - 1)},                   // extreme operands
	}
	for _, c := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Mul(%d, %d) did not panic", c.a, c.b)
				}
			}()
			Mul(c.a, c.b)
		}()
	}
}

func TestDivByZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Div by zero did not panic")
		}
	}()
	Div(One, 0)
}

func TestClamp(t *testing.T) {
	if got := Clamp(FromInt(5), 0, One); got != One {
		t.Errorf("Clamp(5, 0, 1) = %d, want 1000", got)
	}
	if got := Clamp(-One, 0, One); got != 0 {
		t.Errorf("Clamp(-1, 0, 1) = %d, want 0", got)
	}
}
