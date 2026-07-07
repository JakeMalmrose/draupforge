// Auras — toggled reservation skills (SkillAura gems). While a gem's aura
// runs, its AuraMods package sits on the caster's sheet and on every owned
// minion's, and the caster reserves Reserve of max mana. No radius: an aura
// covers the caster and all their minions wherever they stand (they leash
// to the owner anyway), which keeps application event-driven — toggle,
// minion materialization, character injection — with no per-tick scans.
package core

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// applyAuraTo grants sk's aura package to one actor, values scaled by the
// gem level's GemAuraScale, under the skill's aura source.
func applyAuraTo(t *Actor, sk *SkillDef, level int) {
	src := sk.AuraModSource()
	scale := GemAuraScale(level)
	for _, m := range sk.AuraMods {
		t.Sheet.Add(stats.Modifier{
			Stat: m.Stat, Layer: m.Layer, Tags: m.Tags,
			Value: fm.Mul(m.Value, scale), Source: src,
		})
	}
}

// ActivateAura turns sk's aura on for a: the package (plus the mana
// reservation) on a itself, the package on every living minion a owns.
// Current mana clamps under the new, smaller maximum.
func ActivateAura(w *World, a *Actor, sk *SkillDef, level int) {
	applyAuraTo(a, sk, level)
	if sk.Reserve > 0 {
		a.Sheet.Add(stats.Modifier{
			Stat: stats.Mana, Layer: stats.LayerMore,
			Value: -sk.Reserve, Source: sk.AuraModSource(),
		})
		if max := a.MaxMana(); a.Mana > max {
			a.Mana = max
		}
	}
	for _, m := range w.Actors {
		if !m.Dead && m.Owner == a.ID {
			applyAuraTo(m, sk, level)
		}
	}
}

// DeactivateAura removes sk's aura from a and every living minion a owns.
// One RemoveSource strips the package and the reservation together.
func DeactivateAura(w *World, a *Actor, sk *SkillDef) {
	a.Sheet.RemoveSource(sk.AuraModSource())
	for _, m := range w.Actors {
		if !m.Dead && m.Owner == a.ID {
			m.Sheet.RemoveSource(sk.AuraModSource())
		}
	}
}

// ApplyOwnerAuras grants a newly-materialized minion every aura its owner
// is running — the DrainSpawns hook that makes late summons inherit.
func (w *World) ApplyOwnerAuras(m *Actor) {
	if m.Owner == 0 {
		return
	}
	o := w.ActorByID(m.Owner)
	if o == nil {
		return
	}
	for i := range o.Gems {
		if g := &o.Gems[i]; g.AuraOn {
			applyAuraTo(m, g.Skill, g.Level)
		}
	}
}

// ActivateCharacterAuras re-applies the running auras a character arrived
// with (injection rebuilds the sheet from scratch; AuraOn is the durable
// record of what was on). Minions never transfer, so self is the whole job.
func ActivateCharacterAuras(w *World, a *Actor) {
	for i := range a.Gems {
		if g := &a.Gems[i]; g.AuraOn {
			ActivateAura(w, a, g.Skill, g.Level)
		}
	}
}
