package combat

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// TickDoTs advances every active damage-over-time effect by one tick.
// DoTs skip hit/crit/armour entirely; only resistances apply.
func TickDoTs(w *core.World) {
	for _, a := range w.Actors {
		if a.Dead || len(a.DoTs) == 0 {
			continue
		}
		dotTags := stats.T(stats.TagDoT)
		var killerID core.EntityID
		out := a.DoTs[:0]
		for _, d := range a.DoTs {
			dmg := d.PerTick
			if d.Type != core.Physical {
				res := fm.Min(a.Sheet.Eval(resistStat(d.Type), dotTags.With(d.Type.Tag())), maxResist)
				if res > 0 {
					dmg = fm.Mul(dmg, fm.One-res)
				}
			}
			absorbed := fm.Min(a.ES, dmg)
			a.ES -= absorbed
			a.Life -= dmg - absorbed
			if a.Life <= 0 {
				killerID = d.Source
			}
			d.TicksLeft--
			if d.TicksLeft > 0 {
				out = append(out, d)
			}
		}
		a.DoTs = out
		if a.Life <= 0 && !a.Dead {
			kill(w, a, killerID)
		}
	}
}

// Upkeep applies per-tick regeneration, clamped to pool maxima.
func Upkeep(w *core.World) {
	for _, a := range w.Actors {
		if a.Dead {
			continue
		}
		if regen := a.Sheet.Eval(stats.ManaRegen, stats.TagSet{}); regen > 0 {
			a.Mana = fm.Min(a.Mana+fm.Div(regen, fm.FromInt(core.TicksPerSecond)), a.MaxMana())
		}
		if regen := a.Sheet.Eval(stats.LifeRegen, stats.TagSet{}); regen > 0 {
			a.Life = fm.Min(a.Life+fm.Div(regen, fm.FromInt(core.TicksPerSecond)), a.MaxLife())
		}
	}
}
