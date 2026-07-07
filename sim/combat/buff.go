// Content-defined buffs: the general arm of the timed-status container.
// Where ailments scale with the hit that inflicted them, a buff is a fixed
// package of modifiers from a BuffDef — applied whole, refreshed whole,
// removed whole at expiry by the same TickStatuses that retires ailments.
package combat

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// ResolveBuffs drains the world's pending-buff queue in order. It runs
// before ResolveHits so a buff from this tick's effect point shapes this
// tick's incoming hits — the effect point fired first chronologically.
func ResolveBuffs(w *core.World) {
	buffs := w.PendingBuffs
	w.PendingBuffs = w.PendingBuffs[:0]
	for _, b := range buffs {
		tgt := w.ActorByID(b.Target)
		if tgt == nil || tgt.Dead || b.Buff == nil {
			continue
		}
		ApplyBuff(w, tgt, b.Buff, b.Source)
	}
}

// ApplyBuff installs a buff on the target, or refreshes its timer if the
// same def is already active — the modifiers are a constant package, so
// they stay in place. A curse first evicts any OTHER curse on the target
// (one hex per head; the newest wins, PoE's cap-overflow rule). Consumes
// no RNG.
func ApplyBuff(w *core.World, target *core.Actor, def *core.BuffDef, src core.EntityID) {
	ev := core.EvBuff
	if def.Curse {
		ev = core.EvCurse
		out := target.Statuses[:0]
		for _, s := range target.Statuses {
			if s.Buff != nil && s.Buff.Curse && s.Buff != def {
				target.Sheet.RemoveSource(s.Buff.ModSource())
				continue
			}
			out = append(out, s)
		}
		target.Statuses = out
	}
	for i := range target.Statuses {
		s := &target.Statuses[i]
		if s.Buff != def {
			continue
		}
		s.TicksLeft = def.DurationTicks
		s.Source = src
		w.Emit(core.Event{Kind: ev, Actor: src, Other: target.ID, Note: def.ID})
		return
	}
	target.Statuses = append(target.Statuses, core.Status{
		Kind: core.StatusBuff, Buff: def, Magnitude: fm.One,
		TicksLeft: def.DurationTicks, Source: src,
	})
	modSrc := def.ModSource()
	for _, m := range def.Mods {
		target.Sheet.Add(stats.Modifier{
			Stat: m.Stat, Layer: m.Layer, Value: m.Value, Tags: m.Tags, Source: modSrc,
		})
	}
	w.Emit(core.Event{Kind: ev, Actor: src, Other: target.ID, Note: def.ID})
}
