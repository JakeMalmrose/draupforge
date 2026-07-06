// Nominal damage — the character sheet's numbers. NominalHit mirrors
// rollDamage step for step (base roll, added flat × effectiveness,
// conversion, per-portion inc/more) with the average of every roll instead
// of an RNG draw. Pure: no RNG, no events, no world — safe to call from
// the host layer between ticks.
package combat

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// NominalHit is the expected non-crit damage of one direct hit of sk cast
// by att through ctx, all damage types summed. Splash falloff, crits, and
// the defender's mitigation are the fight's business, not the sheet's.
func NominalHit(att *core.Actor, sk *core.SkillDef, ctx core.GemCtx) fm.Fixed {
	h := &core.Hit{Skill: sk, Gem: ctx}
	tags := sk.Tags.With(stats.TagHit)
	scale := core.GemDamageScale(ctx.Level)

	var base [core.DamageTypeCount]fm.Fixed
	for dt := core.DamageType(0); dt < core.DamageTypeCount; dt++ {
		p := damageParts(att, h, tags, dt.Tag())
		var rolled fm.Fixed
		if sk.BaseMax[dt] > 0 {
			avg := fm.Div(sk.BaseMin[dt]+sk.BaseMax[dt], fm.FromInt(2))
			rolled = fm.Mul(avg, scale)
		}
		base[dt] = rolled + fm.Mul(p.Flat, sk.Effectiveness)
	}

	// Conversion, identical to rollDamage: support fractions move damage
	// between types before multipliers, capped at 100% per source.
	native := base
	var converted [core.DamageTypeCount][core.DamageTypeCount]fm.Fixed
	var outFrac [core.DamageTypeCount]fm.Fixed
	for _, s := range ctx.Supports {
		for _, cv := range s.Conversions {
			if base[cv.From] <= 0 {
				continue
			}
			frac := cv.Fraction
			if room := fm.One - outFrac[cv.From]; frac > room {
				frac = room
			}
			if frac <= 0 {
				continue
			}
			outFrac[cv.From] += frac
			moved := fm.Mul(base[cv.From], frac)
			native[cv.From] -= moved
			converted[cv.To][cv.From] += moved
		}
	}

	var total fm.Fixed
	for dt := core.DamageType(0); dt < core.DamageTypeCount; dt++ {
		if native[dt] > 0 {
			p := damageParts(att, h, tags, dt.Tag())
			total += fm.Mul(native[dt], p.Multiplier())
		}
		for src := core.DamageType(0); src < core.DamageTypeCount; src++ {
			if amt := converted[dt][src]; amt > 0 {
				p := damageParts(att, h, tags, dt.Tag(), src.Tag())
				total += fm.Mul(amt, p.Multiplier())
			}
		}
	}
	return total
}
