package stats

import (
	"testing"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

func TestModifierAlgebra(t *testing.T) {
	// base 100 life, +20 flat, 50% increased, two separate 10% more:
	// (100+20) × 1.5 × 1.1 × 1.1 = 217.8
	var base [StatCount]fm.Fixed
	base[Life] = fm.FromInt(100)
	s := NewSheet(base)
	s.Add(Modifier{Stat: Life, Layer: LayerFlat, Value: fm.FromInt(20)})
	s.Add(Modifier{Stat: Life, Layer: LayerIncreased, Value: fm.FromMilli(500)})
	s.Add(Modifier{Stat: Life, Layer: LayerMore, Value: fm.FromMilli(100)})
	s.Add(Modifier{Stat: Life, Layer: LayerMore, Value: fm.FromMilli(100)})

	if got := s.Eval(Life, 0); got != fm.FromMilli(217800) {
		t.Errorf("Eval(Life) = %d, want 217800", got)
	}
}

func TestIncreasedSharesOneBucket(t *testing.T) {
	// Two 50% increased = 2.0×, NOT 1.5×1.5. The genre's core distinction.
	var base [StatCount]fm.Fixed
	base[Damage] = fm.FromInt(10)
	s := NewSheet(base)
	s.Add(Modifier{Stat: Damage, Layer: LayerIncreased, Value: fm.FromMilli(500)})
	s.Add(Modifier{Stat: Damage, Layer: LayerIncreased, Value: fm.FromMilli(500)})

	if got := s.Eval(Damage, 0); got != fm.FromInt(20) {
		t.Errorf("two 50%% increased on 10 = %d, want 20000", got)
	}
}

func TestTagGating(t *testing.T) {
	var base [StatCount]fm.Fixed
	base[Damage] = fm.FromInt(100)
	s := NewSheet(base)
	// "10% increased fire damage" — applies only when the query has Fire.
	s.Add(Modifier{Stat: Damage, Layer: LayerIncreased, Value: fm.FromMilli(100), Tags: T(TagFire)})

	if got := s.Eval(Damage, T(TagFire, TagSpell, TagHit)); got != fm.FromInt(110) {
		t.Errorf("fire query = %d, want 110000", got)
	}
	if got := s.Eval(Damage, T(TagCold, TagSpell, TagHit)); got != fm.FromInt(100) {
		t.Errorf("cold query = %d, want 100000 (fire mod must not apply)", got)
	}
}

func TestOverrideWins(t *testing.T) {
	var base [StatCount]fm.Fixed
	base[MoveSpeed] = fm.FromInt(5)
	s := NewSheet(base)
	s.Add(Modifier{Stat: MoveSpeed, Layer: LayerIncreased, Value: fm.FromMilli(500)})
	s.Add(Modifier{Stat: MoveSpeed, Layer: LayerOverride, Value: fm.FromInt(1)})

	if got := s.Eval(MoveSpeed, 0); got != fm.FromInt(1) {
		t.Errorf("override = %d, want 1000", got)
	}
}

func TestReducedFloorsAtZero(t *testing.T) {
	var base [StatCount]fm.Fixed
	base[Damage] = fm.FromInt(100)
	s := NewSheet(base)
	s.Add(Modifier{Stat: Damage, Layer: LayerIncreased, Value: -fm.FromMilli(1500)}) // 150% reduced

	if got := s.Eval(Damage, 0); got != 0 {
		t.Errorf("150%% reduced = %d, want 0 (floored)", got)
	}
}

func TestRemoveSourceAndMemoInvalidation(t *testing.T) {
	var base [StatCount]fm.Fixed
	base[Life] = fm.FromInt(100)
	s := NewSheet(base)
	s.Add(Modifier{Stat: Life, Layer: LayerFlat, Value: fm.FromInt(50), Source: 7})

	if got := s.Eval(Life, 0); got != fm.FromInt(150) {
		t.Fatalf("with mod = %d, want 150000", got)
	}
	s.RemoveSource(7)
	if got := s.Eval(Life, 0); got != fm.FromInt(100) {
		t.Errorf("after RemoveSource = %d, want 100000 (memo must invalidate)", got)
	}
}
