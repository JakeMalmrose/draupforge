package core

import (
	"testing"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

func TestRNGDeterminism(t *testing.T) {
	a, b := NewRNG(42), NewRNG(42)
	for i := 0; i < 1000; i++ {
		if a.Uint64() != b.Uint64() {
			t.Fatalf("same-seed streams diverged at step %d", i)
		}
	}
	c := NewRNG(43)
	if a.Uint64() == c.Uint64() {
		t.Error("different seeds produced identical output (suspicious)")
	}
}

func TestChanceBounds(t *testing.T) {
	r := NewRNG(1)
	for i := 0; i < 100; i++ {
		if r.Chance(fm.One) != true {
			t.Fatal("Chance(One) must always succeed")
		}
		if r.Chance(0) != false {
			t.Fatal("Chance(0) must always fail")
		}
	}
}

func TestRangeBounds(t *testing.T) {
	r := NewRNG(7)
	min, max := fm.FromInt(20), fm.FromInt(30)
	for i := 0; i < 1000; i++ {
		v := r.Range(min, max)
		if v < min || v > max {
			t.Fatalf("Range produced %d outside [%d, %d]", v, min, max)
		}
	}
	if got := r.Range(max, min); got != max {
		t.Errorf("inverted Range = %d, want min returned (%d)", got, max)
	}
}
