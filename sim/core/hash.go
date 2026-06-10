package core

// Hash folds the world's gameplay-relevant state (including RNG stream
// states) into one FNV-1a value. Golden replay tests and determinism checks
// compare these per tick — any unintended behavior change shows up here.

const (
	fnvOffset = 1469598103934665603
	fnvPrime  = 1099511628211
)

type hasher struct{ h uint64 }

func (s *hasher) u64(v uint64) {
	for i := 0; i < 8; i++ {
		s.h ^= v & 0xff
		s.h *= fnvPrime
		v >>= 8
	}
}

func (s *hasher) i64(v int64)    { s.u64(uint64(v)) }
func (s *hasher) str(v string) {
	for i := 0; i < len(v); i++ {
		s.h ^= uint64(v[i])
		s.h *= fnvPrime
	}
	s.u64(uint64(len(v)))
}

func (w *World) Hash() uint64 {
	s := &hasher{h: fnvOffset}
	s.u64(w.Tick)

	for _, a := range w.Actors {
		s.u64(uint64(a.ID))
		s.i64(a.Pos.X.Milli())
		s.i64(a.Pos.Y.Milli())
		s.i64(a.Life.Milli())
		s.i64(a.Mana.Milli())
		s.i64(a.ES.Milli())
		s.u64(uint64(a.Action.Kind))
		s.u64(uint64(a.Action.Phase))
		s.u64(uint64(a.Action.TicksLeft))
		s.u64(uint64(len(a.DoTs)))
		for _, d := range a.DoTs {
			s.u64(uint64(d.Type))
			s.i64(d.PerTick.Milli())
			s.u64(uint64(d.TicksLeft))
		}
	}

	for _, p := range w.Projectiles {
		s.u64(uint64(p.ID))
		s.i64(p.Pos.X.Milli())
		s.i64(p.Pos.Y.Milli())
		s.u64(uint64(p.TicksLeft))
	}

	for _, d := range w.Drops {
		s.u64(uint64(d.ID))
		s.str(d.Item.Base.ID)
		s.u64(uint64(d.Item.Rarity))
		for _, af := range d.Item.Affixes {
			s.str(af.Def.ID)
			s.i64(af.Value.Milli())
		}
	}

	for _, r := range []*RNG{w.RNGCombat, w.RNGLoot, w.RNGAI, w.RNGMap} {
		for _, word := range r.State() {
			s.u64(word)
		}
	}
	return s.h
}
