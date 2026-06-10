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
	maxResist     = fm.Fixed(750) // 75% resistance cap
	maxArmourRed  = fm.Fixed(900) // 90% physical reduction cap
	minHitChance  = fm.Fixed(50)  // attacks always have ≥5% to hit
	igniteFrac    = fm.Fixed(500) // ignite dps = 50% of the fire hit
	igniteTicks   = 4 * core.TicksPerSecond
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

	// Stage: hit check. Attacks roll accuracy vs evasion; spells always hit.
	if tags.Has(stats.TagAttack) && !rollHitCheck(w, att, def, tags) {
		h.Evaded = true
		w.Emit(core.Event{Kind: core.EvMiss, Actor: att.ID, Other: def.ID, Note: h.Skill.ID})
		return
	}

	// Stage: base roll + flat added (scaled by effectiveness) + inc/more.
	rollDamage(w, att, h, tags)

	// Stage: conversion. Identity for now; the slot in the order is the
	// decision that matters (base → added → converted, mods apply after).

	// Stage: crit. One roll for the whole hit, multiplier on every type.
	rollCrit(w, att, h, tags)

	// Stage: mitigation per type, then defender's damage-taken multiplier.
	total := mitigate(att, def, h, tags)

	// Stage: apply to pools, ES before life.
	applyDamage(w, att, def, total, h.Skill.ID)

	// Stage: post-hit effects.
	if !def.Dead {
		rollIgnite(w, att, def, h, tags)
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
	for dt := core.DamageType(0); dt < core.DamageTypeCount; dt++ {
		dtags := (tags &^ damageTypeTags).With(dt.Tag())
		p := att.Sheet.Layers(stats.Damage, dtags)
		var rolled fm.Fixed
		if sk.BaseMax[dt] > 0 {
			rolled = w.RNGCombat.Range(sk.BaseMin[dt], sk.BaseMax[dt])
		}
		base := rolled + fm.Mul(p.Flat, sk.Effectiveness)
		if base <= 0 {
			continue
		}
		h.Damage[dt] = fm.Mul(base, p.Multiplier())
	}
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

func applyDamage(w *core.World, att, def *core.Actor, total fm.Fixed, note string) {
	if total <= 0 {
		return
	}
	absorbed := fm.Min(def.ES, total)
	def.ES -= absorbed
	def.Life -= total - absorbed
	w.Emit(core.Event{Kind: core.EvHit, Actor: att.ID, Other: def.ID, Amount: total, Note: note})
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
