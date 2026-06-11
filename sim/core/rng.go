package core

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// RNG is xoshiro256** — hand-rolled so the output sequence is owned by this
// repo, not by a Go stdlib version. Replays and golden tests depend on it
// never changing.
type RNG struct {
	s [4]uint64
}

// SplitMix64 advances state and returns the next value. Used to expand seeds
// and to derive independent stream seeds from a single world seed.
func SplitMix64(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func NewRNG(seed uint64) *RNG {
	r := &RNG{}
	for i := range r.s {
		r.s[i] = SplitMix64(&seed)
	}
	return r
}

func rotl(x uint64, k uint) uint64 { return x<<k | x>>(64-k) }

func (r *RNG) Uint64() uint64 {
	result := rotl(r.s[1]*5, 7) * 9
	t := r.s[1] << 17
	r.s[2] ^= r.s[0]
	r.s[3] ^= r.s[1]
	r.s[1] ^= r.s[2]
	r.s[0] ^= r.s[3]
	r.s[2] ^= t
	r.s[3] = rotl(r.s[3], 45)
	return result
}

// Uint64n returns a value in [0, n). Modulo bias is negligible at game scales
// and the simplicity keeps the consumption pattern obvious.
func (r *RNG) Uint64n(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	return r.Uint64() % n
}

// Chance rolls against a fractional probability (One = always). It always
// consumes exactly one value so stat changes don't shift the stream.
func (r *RNG) Chance(p fm.Fixed) bool {
	roll := r.Uint64n(uint64(fm.One))
	if p <= 0 {
		return false
	}
	if p >= fm.One {
		return true
	}
	return roll < uint64(p)
}

// Range returns a uniform value in [min, max] at milli precision.
func (r *RNG) Range(min, max fm.Fixed) fm.Fixed {
	if max <= min {
		return min
	}
	span := uint64(max-min) + 1
	return min + fm.Fixed(r.Uint64n(span))
}

// State exposes the internal state for world hashing and saves.
func (r *RNG) State() [4]uint64 { return r.s }

// RestoreRNG resumes a stream exactly where State captured it.
func RestoreRNG(state [4]uint64) *RNG { return &RNG{s: state} }
