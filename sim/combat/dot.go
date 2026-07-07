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
				// Negative resistance amplifies DoT ticks too — same rule
				// as the hit path, and how flammability feeds ignite.
				res := fm.Min(a.Sheet.Eval(resistStat(d.Type), dotTags.With(d.Type.Tag())), maxResist)
				if res != 0 {
					dmg = fm.Mul(dmg, fm.One-res)
				}
			}
			absorbed := fm.Min(a.ES, dmg)
			a.ES -= absorbed
			a.Life -= dmg - absorbed
			MarkHit(a) // a DoT tick delays recharge too
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

// Energy-shield recharge (open for tuning): after esRechargeDelay ticks
// without taking damage, ES refills at esRechargeRate of its maximum per
// second. Any damage taken (MarkHit) resets the delay — ES is the buffer
// that rewards not getting hit, unlike life regen which is always on.
const (
	esRechargeDelay = 2 * core.TicksPerSecond // 2s untouched before it kicks in
	esRechargeRate  = fm.Fixed(200)           // 20% of max ES per second
)

// MarkHit resets an actor's ES recharge delay — call whenever it takes
// damage (hits and DoTs alike).
func MarkHit(a *core.Actor) { a.RechargeDelay = esRechargeDelay }

// Upkeep applies per-tick regeneration (life, mana) and energy-shield
// recharge, all clamped to pool maxima.
func Upkeep(w *core.World) {
	perTick := fm.FromInt(core.TicksPerSecond)
	for _, a := range w.Actors {
		if a.Dead {
			continue
		}
		// Stun lockout + immunity countdown (Upkeep is the tick's first
		// phase, so the command gate this tick sees the decremented value).
		if a.StunTicks > 0 {
			a.StunTicks--
		}
		// Short-lived minions burn down here; expiry is a quiet despawn
		// (no death event, no loot, no XP), swept by compaction like a
		// cap despawn.
		if a.LifespanTicks > 0 {
			a.LifespanTicks--
			if a.LifespanTicks == 0 {
				a.Dead = true
				continue
			}
		}
		if regen := a.Sheet.Eval(stats.ManaRegen, stats.TagSet{}); regen > 0 {
			a.Mana = fm.Min(a.Mana+fm.Div(regen, perTick), a.MaxMana())
		}
		if regen := a.Sheet.Eval(stats.LifeRegen, stats.TagSet{}); regen > 0 {
			a.Life = fm.Min(a.Life+fm.Div(regen, perTick), a.MaxLife())
		}
		// ES recharge: count the delay down, then refill once it elapses.
		if a.RechargeDelay > 0 {
			a.RechargeDelay--
			continue
		}
		if maxES := a.MaxES(); maxES > 0 && a.ES < maxES {
			a.ES = fm.Min(a.ES+fm.Div(fm.Mul(maxES, esRechargeRate), perTick), maxES)
		}
	}
}
