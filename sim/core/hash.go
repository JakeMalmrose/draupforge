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

func (s *hasher) i64(v int64) { s.u64(uint64(v)) }

func (s *hasher) item(item *Item) {
	s.u64(uint64(item.ID))
	s.str(item.Base.ID)
	s.u64(uint64(item.Rarity))
	s.i64(item.Implicit.Milli())
	for _, af := range item.Affixes {
		s.str(af.Def.ID)
		s.i64(af.Value.Milli())
	}
}
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

	// Terrain shapes behavior; worlds with different maps must hash apart.
	// Open-plane worlds skip this, keeping pre-grid golden traces valid.
	if w.Grid != nil {
		for _, word := range w.Grid.HashWords() {
			s.u64(word)
		}
	}

	for _, a := range w.Actors {
		s.u64(uint64(a.ID))
		s.i64(a.Pos.X.Milli())
		s.i64(a.Pos.Y.Milli())
		s.i64(a.Life.Milli())
		s.i64(a.Mana.Milli())
		s.i64(a.ES.Milli())
		s.u64(uint64(a.Level))
		s.i64(a.XP)
		s.u64(uint64(a.Action.Kind))
		s.u64(uint64(a.Action.Phase))
		s.u64(uint64(a.Action.TicksLeft))
		s.u64(uint64(len(a.DoTs)))
		for _, d := range a.DoTs {
			s.u64(uint64(d.Type))
			s.i64(d.PerTick.Milli())
			s.u64(uint64(d.TicksLeft))
		}
		s.u64(uint64(len(a.Statuses)))
		for _, st := range a.Statuses {
			s.u64(uint64(st.Kind))
			if st.Buff != nil {
				s.str(st.Buff.ID)
			}
			s.i64(st.Magnitude.Milli())
			s.u64(uint64(st.TicksLeft))
		}
		for slot := EquipSlot(0); slot < EquipSlotCount; slot++ {
			item := a.Equipment[slot]
			if item == nil {
				s.u64(0)
				continue
			}
			s.item(item)
		}
		s.u64(uint64(len(a.Inventory)))
		for i := range a.Inventory {
			s.item(&a.Inventory[i])
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
		s.item(&d.Item)
	}

	for _, r := range []*RNG{w.RNGCombat, w.RNGLoot, w.RNGAI, w.RNGMap} {
		for _, word := range r.State() {
			s.u64(word)
		}
	}
	return s.h
}
