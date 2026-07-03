// Package combat implements the damage pipeline: an explicit sequence of
// named stages a Hit flows through, each independently testable. DoTs run a
// separate, simpler path (dot.go) — they are not hits.
package combat

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

const (
	maxResist    = fm.Fixed(750) // 75% resistance cap
	maxArmourRed = fm.Fixed(900) // 90% physical reduction cap
	minHitChance = fm.Fixed(50)  // attacks always have ≥5% to hit
	igniteFrac   = fm.Fixed(500) // ignite dps = 50% of the fire hit
	igniteTicks  = 4 * core.TicksPerSecond
)

// ResolveHits drains the world's pending-hit queue in order.
func ResolveHits(w *core.World) {
	hits := w.PendingHits
	w.PendingHits = w.PendingHits[:0]
	for i := range hits {
		resolve(w, &hits[i])
	}
}

func resolve(w *core.World, h *core.Hit) {
	att := w.ActorByID(h.Attacker)
	def := w.ActorByID(h.Defender)
	// A projectile can outlive its caster; without a stat sheet there is no
	// damage to compute, so the hit fizzles. Revisit if that feels bad.
	if att == nil || def == nil || def.Dead {
		return
	}
	tags := h.Tags

	// Stage: hit check. Attacks roll accuracy vs evasion; spells always
	// hit, and so do telegraphed zones — their dodge is spatial, and the
	// skipped roll is replay-relevant (telegraphed hits consume no
	// accuracy RNG).
	if tags.Has(stats.TagAttack) && !h.Telegraphed && !rollHitCheck(w, att, def, tags) {
		h.Evaded = true
		w.Emit(core.Event{Kind: core.EvMiss, Actor: att.ID, Other: def.ID, Note: h.Skill.ID})
		return
	}

	// Stage: base roll + added + conversion + inc/more (the order DESIGN.md
	// locked; conversion is live now — support gems are its first source).
	rollDamage(w, att, h, tags)

	// Stage: crit. One roll for the whole hit, multiplier on every type.
	rollCrit(w, att, h, tags)

	// Stage: mitigation per type, then defender's damage-taken multiplier.
	total := mitigate(att, def, h, tags)

	// Stage: apply to pools, ES before life. Splash hits mark their event
	// note so clients can style them apart from the direct impact.
	note := h.Skill.ID
	if h.AreaScale > 0 {
		note += ":aoe"
	}
	applyDamage(w, att, def, total, note, h.Crit)

	// Stage: life leech — a fraction of the hit's dealt damage refills the
	// attacker (hits only; DoTs run a separate path). Instant and capped at
	// max life, no RNG. Self-damage never leeches.
	if att.ID != def.ID && !att.Dead {
		if leech := att.Sheet.Eval(stats.LifeLeech, tags); leech > 0 {
			att.Life = fm.Min(att.Life+fm.Mul(total, leech), att.MaxLife())
		}
	}

	// Stage: post-hit effects, fixed order (ignite → chill → shock; the
	// order is part of the RNG-consumption contract).
	if !def.Dead {
		rollIgnite(w, att, def, h, tags)
		applyChill(w, att, def, h)
		rollShock(w, att, def, h, tags)
	}
}

func rollHitCheck(w *core.World, att, def *core.Actor, tags stats.TagSet) bool {
	acc := att.Sheet.Eval(stats.Accuracy, tags)
	ev := def.Sheet.Eval(stats.Evasion, tags)
	chance := fm.One
	if acc+ev > 0 {
		chance = fm.Clamp(fm.Div(acc, acc+ev), minHitChance, fm.One)
	}
	return w.RNGCombat.Chance(chance)
}

// damageTypeTags is every damage-type tag, for stripping before a per-type
// query: rolling the cold portion of a fire-tagged skill must not let "added
// fire damage" apply — the type tag is replaced, never accumulated.
var damageTypeTags = stats.T(stats.TagPhysical, stats.TagFire, stats.TagCold, stats.TagLightning, stats.TagChaos)

func rollDamage(w *core.World, att *core.Actor, h *core.Hit, tags stats.TagSet) {
	sk := h.Skill
	scale := core.GemDamageScale(h.Gem.Level)

	// Base roll + added flat (scaled by effectiveness), per type in enum
	// order — RNG consumption order is the replay contract. The gem level
	// scales only the skill's own roll; added damage is the supports'/
	// gear's own contribution.
	var base [core.DamageTypeCount]fm.Fixed
	for dt := core.DamageType(0); dt < core.DamageTypeCount; dt++ {
		p := damageParts(att, h, tags, dt.Tag())
		var rolled fm.Fixed
		if sk.BaseMax[dt] > 0 {
			rolled = fm.Mul(w.RNGCombat.Range(sk.BaseMin[dt], sk.BaseMax[dt]), scale)
		}
		base[dt] = rolled + fm.Mul(p.Flat, sk.Effectiveness)
	}

	// Conversion: fractions of the pre-multiplier totals move between
	// types (base → added → converted); a source's outgoing fractions cap
	// at 100%, in support-then-definition order.
	native := base
	var converted [core.DamageTypeCount][core.DamageTypeCount]fm.Fixed
	var outFrac [core.DamageTypeCount]fm.Fixed
	for _, s := range h.Gem.Supports {
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

	// Inc/more per portion. A converted portion is scaled by modifiers of
	// both its source and destination types — its query context carries
	// both type tags, and tag subsetting does the rest.
	for dt := core.DamageType(0); dt < core.DamageTypeCount; dt++ {
		var total fm.Fixed
		if native[dt] > 0 {
			p := damageParts(att, h, tags, dt.Tag())
			total += fm.Mul(native[dt], p.Multiplier())
		}
		for src := core.DamageType(0); src < core.DamageTypeCount; src++ {
			amt := converted[dt][src]
			if amt <= 0 {
				continue
			}
			p := damageParts(att, h, tags, dt.Tag(), src.Tag())
			total += fm.Mul(amt, p.Multiplier())
		}
		if total > 0 {
			h.Damage[dt] = total
		}
	}

	// Splash hits (projectile explosions): the whole roll scales by the
	// distance falloff baked at impact time.
	if h.AreaScale > 0 {
		for dt := range h.Damage {
			h.Damage[dt] = fm.Mul(h.Damage[dt], h.AreaScale)
		}
	}
}

// damageParts is one damage query: the hit's tags with the damage-type tags
// replaced by the given ones, evaluated on the attacker's sheet with the
// cast's support modifiers folded in.
func damageParts(att *core.Actor, h *core.Hit, tags stats.TagSet, typeTags ...stats.Tag) stats.Parts {
	dtags := tags.Without(damageTypeTags)
	for _, t := range typeTags {
		dtags = dtags.With(t)
	}
	p := att.Sheet.Layers(stats.Damage, dtags)
	return h.Gem.FoldSupportMods(p, stats.Damage, dtags)
}

func rollCrit(w *core.World, att *core.Actor, h *core.Hit, tags stats.TagSet) {
	if !w.RNGCombat.Chance(att.Sheet.Eval(stats.CritChance, tags)) {
		return
	}
	h.Crit = true
	mult := att.Sheet.Eval(stats.CritMulti, tags)
	for dt := range h.Damage {
		h.Damage[dt] = fm.Mul(h.Damage[dt], mult)
	}
}

func resistStat(dt core.DamageType) stats.StatID {
	switch dt {
	case core.Fire:
		return stats.FireRes
	case core.Cold:
		return stats.ColdRes
	case core.Lightning:
		return stats.LightningRes
	default:
		return stats.ChaosRes
	}
}

func mitigate(att, def *core.Actor, h *core.Hit, tags stats.TagSet) fm.Fixed {
	taken := def.Sheet.Eval(stats.DamageTaken, tags)
	var total fm.Fixed
	for dt := core.DamageType(0); dt < core.DamageTypeCount; dt++ {
		d := h.Damage[dt]
		if d <= 0 {
			continue
		}
		if dt == core.Physical {
			arm := def.Sheet.Eval(stats.Armour, tags)
			if arm > 0 {
				// reduction = armour / (armour + 10×damage): strong vs small
				// hits, weak vs big ones.
				red := fm.Min(fm.Div(arm, arm+fm.Mul(fm.FromInt(10), d)), maxArmourRed)
				d = fm.Mul(d, fm.One-red)
			}
		} else {
			res := fm.Min(def.Sheet.Eval(resistStat(dt), tags), maxResist)
			if res > 0 {
				d = fm.Mul(d, fm.One-res)
			}
		}
		d = fm.Mul(d, taken)
		h.Damage[dt] = d
		total += d
	}
	return total
}

func applyDamage(w *core.World, att, def *core.Actor, total fm.Fixed, note string, crit bool) {
	if total <= 0 {
		return
	}
	absorbed := fm.Min(def.ES, total)
	def.ES -= absorbed
	def.Life -= total - absorbed
	w.Emit(core.Event{Kind: core.EvHit, Actor: att.ID, Other: def.ID, Amount: total, Note: note, Crit: crit})
	if def.Life <= 0 && !def.Dead {
		kill(w, def, att.ID)
	}
}

func kill(w *core.World, def *core.Actor, killer core.EntityID) {
	def.Dead = true
	def.Life = 0
	w.Emit(core.Event{Kind: core.EvDeath, Actor: def.ID, Other: killer})
}

func rollIgnite(w *core.World, att, def *core.Actor, h *core.Hit, tags stats.TagSet) {
	fire := h.Damage[core.Fire]
	if fire <= 0 {
		return
	}
	chance := h.Skill.IgniteChance + att.Sheet.Eval(stats.IgniteChance, tags)
	if !w.RNGCombat.Chance(chance) {
		return
	}
	perTick := fm.Div(fm.Mul(fire, igniteFrac), fm.FromInt(core.TicksPerSecond))
	if perTick <= 0 {
		return
	}
	h.Ignited = true
	// Strongest ignite wins; weaker ones are discarded, not stacked.
	for i := range def.DoTs {
		d := &def.DoTs[i]
		if d.Type == core.Fire {
			if perTick > d.PerTick {
				d.PerTick = perTick
				d.TicksLeft = igniteTicks
				d.Source = att.ID
				w.Emit(core.Event{Kind: core.EvIgnite, Actor: att.ID, Other: def.ID, Amount: perTick})
			}
			return
		}
	}
	def.DoTs = append(def.DoTs, core.DoT{
		Type: core.Fire, PerTick: perTick, TicksLeft: igniteTicks, Source: att.ID,
	})
	w.Emit(core.Event{Kind: core.EvIgnite, Actor: att.ID, Other: def.ID, Amount: perTick})
}
