// Package progress turns kills into character growth: XP awards off death
// events and the level curve. It consumes no RNG — progression is a pure
// function of who killed what.
package progress

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// MaxLevel caps progression; XP gained at the cap is discarded.
const MaxLevel = 50

// XPToNext is the XP needed to advance from level to level+1. Quadratic:
// early levels fall fast, later ones make monster XP values matter.
func XPToNext(level int) int64 {
	return 100 * int64(level) * int64(level)
}

// AwardXP scans this tick's death events and pays the killer. Runs after
// combat phases so every death is visible. XP is progress into the current
// level; level-ups apply growth modifiers (Actor.SetLevel) and refill pools
// — the classic ding heal.
func AwardXP(w *core.World) {
	for _, ev := range w.Events() {
		if ev.Kind != core.EvDeath {
			continue
		}
		dier := w.ActorByID(ev.Actor)
		killer := w.ActorByID(ev.Other)
		if dier == nil || killer == nil || killer.Dead {
			continue
		}
		if dier.Def.XPValue == 0 || killer.Team == dier.Team {
			continue
		}
		// Leveled monsters pay leveled XP (linear for now) so floor-scaled
		// packs keep up with the quadratic curve. Level 1 is the identity —
		// existing scenarios and goldens are unaffected. Rarity multiplies
		// on top: magic ×3, rare ×6 (open for tuning).
		xp := dier.Def.XPValue * int64(dier.Level)
		switch dier.Rarity {
		case core.RarityMagic:
			xp *= 3
		case core.RarityRare:
			xp *= 6
		}
		killer.XP += xp
		// Kills also feed the killer's flasks — same reward hook as XP.
		for i := range killer.FlaskCharges {
			killer.FlaskCharges[i] = min(killer.FlaskCharges[i]+core.FlaskGainPerKill, core.FlaskMaxCharges)
		}
		for killer.Level < MaxLevel && killer.XP >= XPToNext(killer.Level) {
			killer.XP -= XPToNext(killer.Level)
			killer.SetLevel(killer.Level + 1)
			killer.Life = killer.MaxLife()
			killer.Mana = killer.MaxMana()
			killer.ES = killer.MaxES()
			w.Emit(core.Event{
				Kind:   core.EvLevelUp,
				Tick:   w.Tick,
				Actor:  killer.ID,
				Amount: fm.FromInt(int64(killer.Level)),
			})
		}
		if killer.Level >= MaxLevel {
			killer.XP = 0 // nothing left to climb; don't accumulate forever
		}
	}
}
