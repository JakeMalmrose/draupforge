// Ailments beyond ignite: chill and shock. These are statuses — timed
// packages of stat modifiers on the defender's sheet — not DoTs. A status
// grants its modifiers under StatusKind.ModSource() when applied and removes
// them on expiry; one instance per kind, strongest magnitude wins (weaker
// applications are discarded, mirroring how ignite refuses to downgrade).
package combat

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

const (
	// Ailment magnitude scales with hit size: damage as a fraction of the
	// defender's max life, ×3 — so a hit for 10% of max life lands a 30%
	// chill. Below the floor the ailment doesn't apply at all; trash hits
	// shouldn't smear permanent 1% slows across a fight.
	ailmentScale = fm.Fixed(3000) // ×3
	ailmentFloor = fm.Fixed(50)   // 5%
	chillCap     = fm.Fixed(300)  // 30% slow at most
	shockCap     = fm.Fixed(500)  // 50% increased taken at most

	chillTicks = 2 * core.TicksPerSecond
	shockTicks = 2 * core.TicksPerSecond
)

// ailmentMagnitude converts post-mitigation damage into an ailment strength,
// or 0 when the hit is too small to matter.
func ailmentMagnitude(dmg, maxLife, limit fm.Fixed) fm.Fixed {
	if maxLife <= 0 {
		return 0
	}
	mag := fm.Mul(fm.Div(dmg, maxLife), ailmentScale)
	if mag < ailmentFloor {
		return 0
	}
	return fm.Min(mag, limit)
}

// applyChill: every cold-damage hit chills — no roll, no RNG. The slow is a
// negative More multiplier on move/attack/cast speed so stacked "increased
// speed" gear can never cancel it out of the additive bucket.
func applyChill(w *core.World, att, def *core.Actor, h *core.Hit) {
	mag := ailmentMagnitude(h.Damage[core.Cold], def.MaxLife(), chillCap)
	if mag <= 0 {
		return
	}
	if setStatus(def, core.StatusChill, mag, chillTicks, att.ID) {
		h.Chilled = true
		w.Emit(core.Event{Kind: core.EvChill, Actor: att.ID, Other: def.ID, Amount: mag})
	}
}

// rollShock: lightning-damage hits shock on a chance roll (skill base +
// ShockChance stats), scaling damage taken from all sources while it lasts.
// The roll happens only when lightning damage landed, so fights without
// lightning consume no extra combat RNG — replays of old content stay put.
func rollShock(w *core.World, att, def *core.Actor, h *core.Hit, tags stats.TagSet) {
	if h.Damage[core.Lightning] <= 0 {
		return
	}
	chance := h.Skill.ShockChance + att.Sheet.Eval(stats.ShockChance, tags)
	if !w.RNGCombat.Chance(chance) {
		return
	}
	mag := ailmentMagnitude(h.Damage[core.Lightning], def.MaxLife(), shockCap)
	if mag <= 0 {
		return
	}
	if setStatus(def, core.StatusShock, mag, shockTicks, att.ID) {
		h.Shocked = true
		w.Emit(core.Event{Kind: core.EvShock, Actor: att.ID, Other: def.ID, Amount: mag})
	}
}

// setStatus installs or strengthens one status on def, returning whether
// anything changed. A weaker-or-equal application is discarded outright —
// it does not even refresh the duration, or trash hits would keep a strong
// chill alive forever.
func setStatus(def *core.Actor, kind core.StatusKind, mag fm.Fixed, ticks uint32, src core.EntityID) bool {
	for i := range def.Statuses {
		s := &def.Statuses[i]
		if s.Kind != kind {
			continue
		}
		if mag <= s.Magnitude {
			return false
		}
		def.Sheet.RemoveSource(kind.ModSource())
		s.Magnitude, s.TicksLeft, s.Source = mag, ticks, src
		grantStatusMods(def, kind, mag)
		return true
	}
	def.Statuses = append(def.Statuses, core.Status{
		Kind: kind, Magnitude: mag, TicksLeft: ticks, Source: src,
	})
	grantStatusMods(def, kind, mag)
	return true
}

// grantStatusMods is the one place a StatusKind maps to its sheet effect.
func grantStatusMods(a *core.Actor, kind core.StatusKind, mag fm.Fixed) {
	src := kind.ModSource()
	switch kind {
	case core.StatusChill:
		for _, st := range []stats.StatID{stats.MoveSpeed, stats.AttackSpeed, stats.CastSpeed} {
			a.Sheet.Add(stats.Modifier{Stat: st, Layer: stats.LayerMore, Value: -mag, Source: src})
		}
	case core.StatusShock:
		a.Sheet.Add(stats.Modifier{Stat: stats.DamageTaken, Layer: stats.LayerIncreased, Value: mag, Source: src})
	}
}

// TickStatuses advances every status timer by one tick, removing the
// granted modifiers at expiry. Note for actions in flight: chill slows
// movement immediately (speed is read every tick) but a windup already
// counted keeps its tick count — speeds bind at use time, like cast speed.
func TickStatuses(w *core.World) {
	for _, a := range w.Actors {
		if a.Dead || len(a.Statuses) == 0 {
			continue
		}
		out := a.Statuses[:0]
		for _, s := range a.Statuses {
			s.TicksLeft--
			if s.TicksLeft > 0 {
				out = append(out, s)
				continue
			}
			a.Sheet.RemoveSource(s.Kind.ModSource())
		}
		a.Statuses = out
	}
}
