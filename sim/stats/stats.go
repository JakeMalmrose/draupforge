// Package stats implements the modifier algebra every number in the game
// flows through:
//
//	final = (base + Σflat) × (1 + Σincreased) × Π(1 + more_i)
//
// with Override winning outright. Increased values share one additive bucket;
// each More multiplier applies separately — that split is the genre's entire
// balance language. Tags are the conditionality system: a modifier applies
// iff the query's tag context contains all of the modifier's tags.
package stats

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

type StatID uint8

const (
	Life         StatID = iota // maximum life
	Mana                       // maximum mana
	EnergyShield               // maximum energy shield
	LifeRegen                  // life per second
	ManaRegen                  // mana per second
	Damage                     // queried with damage-type/skill tags
	DamageTaken                // multiplier on incoming damage, base One
	CritChance                 // fraction: 0.05 = 5%
	CritMulti                  // multiplier: 1.5 = 150%
	AttackSpeed                // multiplier, base One
	CastSpeed                  // multiplier, base One
	MoveSpeed                  // units per second
	Accuracy                   // rating vs Evasion
	Evasion                    // rating vs Accuracy
	Armour                     // physical hit mitigation rating
	FireRes                    // fraction, capped in the pipeline
	ColdRes
	LightningRes
	ChaosRes
	IgniteChance // fraction, added to skill base chance
	ShockChance  // fraction, added to skill base chance

	StatCount
)

type Tag uint8

const (
	TagPhysical Tag = iota
	TagFire
	TagCold
	TagLightning
	TagChaos
	TagAttack
	TagSpell
	TagMelee
	TagProjectile
	TagHit
	TagDoT
	TagFullLife // conditional tag, recomputed per-tick on the actor

	TagCount
)

// tagWords sizes TagSet to fit TagCount — adding a Tag past a 64-bit
// boundary widens the set at compile time, with no call-site or migration
// work. Nothing hashes or wires raw TagSet words (saves encode tag indices),
// so widening is invisible outside this type.
const tagWords = (int(TagCount) + 63) / 64

// TagSet is a bitset of Tags; subset checks are one AND+compare per word
// (the compiler unrolls these fixed-size loops). Comparable, so it keys the
// evaluator memo directly.
type TagSet [tagWords]uint64

func T(tags ...Tag) TagSet {
	var s TagSet
	for _, t := range tags {
		s[t>>6] |= 1 << (t & 63)
	}
	return s
}

func (s TagSet) With(t Tag) TagSet {
	s[t>>6] |= 1 << (t & 63)
	return s
}

func (s TagSet) Has(t Tag) bool { return s[t>>6]&(1<<(t&63)) != 0 }

func (s TagSet) ContainsAll(req TagSet) bool {
	for i, w := range req {
		if s[i]&w != w {
			return false
		}
	}
	return true
}

// Without returns s minus every tag in o (AND NOT) — the pipeline uses it to
// strip damage-type tags before a per-type query.
func (s TagSet) Without(o TagSet) TagSet {
	for i, w := range o {
		s[i] &^= w
	}
	return s
}

// Tags returns the set's members in ascending order — the width-independent
// form serialization encodes (tag indices survive future widenings; raw
// words would not).
func (s TagSet) Tags() []Tag {
	var out []Tag
	for t := Tag(0); t < TagCount; t++ {
		if s.Has(t) {
			out = append(out, t)
		}
	}
	return out
}

type Layer uint8

const (
	LayerFlat Layer = iota
	LayerIncreased
	LayerMore
	LayerOverride
)

type Modifier struct {
	Stat  StatID
	Layer Layer
	// Value semantics by layer — Flat/Override: absolute amount;
	// Increased/More: fraction (0.10 = 10%).
	Value fm.Fixed
	// Tags the query context must contain for this modifier to apply.
	Tags TagSet
	// Source identifies the granting item/buff/passive so it can be removed.
	Source uint64
}

// Parts is the decomposed evaluation of one (stat, tags) query, exposed so
// the damage pipeline can apply layers to externally-rolled base values.
type Parts struct {
	Flat        fm.Fixed
	Increased   fm.Fixed // summed fraction; may be negative
	More        fm.Fixed // composed multiplier, One-based
	Override    fm.Fixed
	HasOverride bool
}

// Multiplier returns the combined inc×more multiplier, flooring the
// increased bucket at zero (you can't deal negative damage via "reduced").
func (p Parts) Multiplier() fm.Fixed {
	inc := fm.Max(fm.One+p.Increased, 0)
	return fm.Mul(inc, p.More)
}

// Sheet is one actor's stat state: base values plus granted modifiers.
// Methods are not safe for concurrent use — the sim is single-threaded.
type Sheet struct {
	base    [StatCount]fm.Fixed
	mods    []Modifier
	version uint64
	memo    map[memoKey]memoVal
}

type memoKey struct {
	stat StatID
	tags TagSet
}

type memoVal struct {
	version uint64
	parts   Parts
}

func NewSheet(base [StatCount]fm.Fixed) *Sheet {
	return &Sheet{base: base, memo: make(map[memoKey]memoVal)}
}

func (s *Sheet) Base(stat StatID) fm.Fixed { return s.base[stat] }

func (s *Sheet) SetBase(stat StatID, v fm.Fixed) {
	s.base[stat] = v
	s.version++
}

func (s *Sheet) Add(m Modifier) {
	s.mods = append(s.mods, m)
	s.version++
}

// RemoveSource drops every modifier granted by src (unequip, buff expiry).
func (s *Sheet) RemoveSource(src uint64) {
	out := s.mods[:0]
	for _, m := range s.mods {
		if m.Source != src {
			out = append(out, m)
		}
	}
	s.mods = out
	s.version++
}

// Layers returns the decomposed modifier layers for a query, memoized until
// the modifier list changes. The memo map is lookup-only — never iterated —
// so it cannot threaten determinism.
func (s *Sheet) Layers(stat StatID, tags TagSet) Parts {
	key := memoKey{stat, tags}
	if v, ok := s.memo[key]; ok && v.version == s.version {
		return v.parts
	}
	p := Parts{More: fm.One}
	for _, m := range s.mods {
		if m.Stat != stat || !tags.ContainsAll(m.Tags) {
			continue
		}
		switch m.Layer {
		case LayerFlat:
			p.Flat += m.Value
		case LayerIncreased:
			p.Increased += m.Value
		case LayerMore:
			p.More = fm.Mul(p.More, fm.One+m.Value)
		case LayerOverride:
			p.HasOverride = true
			p.Override = m.Value
		}
	}
	if s.memo == nil {
		s.memo = make(map[memoKey]memoVal)
	}
	s.memo[key] = memoVal{s.version, p}
	return p
}

// Eval computes the final value of a stat under a tag context.
func (s *Sheet) Eval(stat StatID, tags TagSet) fm.Fixed {
	p := s.Layers(stat, tags)
	if p.HasOverride {
		return p.Override
	}
	return fm.Mul(s.base[stat]+p.Flat, p.Multiplier())
}
