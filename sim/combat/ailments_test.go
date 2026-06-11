package combat_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/combat"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

func findStatus(a *core.Actor, kind core.StatusKind) *core.Status {
	for i := range a.Statuses {
		if a.Statuses[i].Kind == kind {
			return &a.Statuses[i]
		}
	}
	return nil
}

func TestChillAppliesAndSlows(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{
		stats.MoveSpeed: fm.FromInt(10),
		stats.CastSpeed: fm.One,
	}), space.V(fm.One, 0))

	// 10 cold into 100 life = 10% of max life → ×3 = 30% chill (the cap).
	queueAndResolve(w, att, def, spellDef(10, 10, core.Cold))

	st := findStatus(def, core.StatusChill)
	if st == nil {
		t.Fatal("cold hit did not chill")
	}
	if st.Magnitude != fm.FromMilli(300) {
		t.Errorf("chill magnitude = %d, want 300 (30%%)", st.Magnitude)
	}
	if got := def.Sheet.Eval(stats.MoveSpeed, 0); got != fm.FromInt(7) {
		t.Errorf("move speed under 30%% chill = %d, want 7000", got)
	}
	if got := def.Sheet.Eval(stats.CastSpeed, 0); got != fm.FromMilli(700) {
		t.Errorf("cast speed under 30%% chill = %d, want 700", got)
	}
}

func TestChillBelowFloorDoesNotApply(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))

	// 10 cold into 1000 life = 1% → ×3 = 3%, under the 5% floor.
	queueAndResolve(w, att, def, spellDef(10, 10, core.Cold))
	if findStatus(def, core.StatusChill) != nil {
		t.Error("trash hit applied a chill below the magnitude floor")
	}
}

func TestChillExpiresAndRestoresSpeed(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.MoveSpeed: fm.FromInt(10)}), space.V(fm.One, 0))

	queueAndResolve(w, att, def, spellDef(10, 10, core.Cold))
	for i := 0; i < 2*core.TicksPerSecond; i++ {
		combat.TickStatuses(w)
	}
	if len(def.Statuses) != 0 {
		t.Fatal("chill did not expire after 2s")
	}
	if got := def.Sheet.Eval(stats.MoveSpeed, 0); got != fm.FromInt(10) {
		t.Errorf("move speed after chill expiry = %d, want full 10000", got)
	}
}

func TestStrongerChillReplacesWeakerOnly(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(200, map[stats.StatID]fm.Fixed{stats.MoveSpeed: fm.FromInt(10)}), space.V(fm.One, 0))

	weak := spellDef(4, 4, core.Cold)     // 2% of 200 → 6% chill
	strong := spellDef(16, 16, core.Cold) // 8% of 200 → 24% chill

	queueAndResolve(w, att, def, weak)
	queueAndResolve(w, att, def, strong)
	if len(def.Statuses) != 1 {
		t.Fatalf("statuses = %d, want 1 (no stacking)", len(def.Statuses))
	}
	if mag := def.Statuses[0].Magnitude; mag != fm.FromMilli(240) {
		t.Errorf("magnitude = %d, want the stronger 240", mag)
	}

	// Weaker reapplication must not downgrade it, and must not refresh it.
	combat.TickStatuses(w)
	ticksBefore := def.Statuses[0].TicksLeft
	queueAndResolve(w, att, def, weak)
	if def.Statuses[0].Magnitude != fm.FromMilli(240) || def.Statuses[0].TicksLeft != ticksBefore {
		t.Error("weaker chill downgraded or refreshed a stronger one")
	}
	// The sheet must hold exactly one chill's worth of slow, never two.
	if got := def.Sheet.Eval(stats.MoveSpeed, 0); got != fm.FromMilli(7600) {
		t.Errorf("move speed = %d, want 7600 (one 24%% chill)", got)
	}
}

func TestShockIncreasesDamageTaken(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, nil), space.V(fm.One, 0))

	sk := spellDef(10, 10, core.Lightning)
	sk.ShockChance = fm.One // guaranteed
	queueAndResolve(w, att, def, sk)

	st := findStatus(def, core.StatusShock)
	if st == nil {
		t.Fatal("lightning hit with 100% shock chance did not shock")
	}
	// 10 into 100 life → 30% increased damage taken.
	if st.Magnitude != fm.FromMilli(300) {
		t.Errorf("shock magnitude = %d, want 300", st.Magnitude)
	}

	// A follow-up 10 fire hit lands for 13 instead of 10.
	lifeBefore := def.Life
	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	if got := lifeBefore - def.Life; got != fm.FromInt(13) {
		t.Errorf("damage while shocked = %d, want 13000", got)
	}
}

func TestNoShockWithoutChance(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, nil), space.V(fm.One, 0))

	queueAndResolve(w, att, def, spellDef(10, 10, core.Lightning)) // 0% chance
	if findStatus(def, core.StatusShock) != nil {
		t.Error("shock applied with zero shock chance")
	}
}

// TestAilmentRNGConsumption pins the streams' alignment contract: chill is
// chance-free so a cold hit draws exactly what a chaos hit draws (damage +
// crit), and shock's single roll mirrors ignite's, so lightning and fire
// hits draw identically (damage + crit + one ailment roll). Replays of old
// fire-only content stay byte-stable.
func TestAilmentRNGConsumption(t *testing.T) {
	state := func(dt core.DamageType) [4]uint64 {
		w := testWorld()
		att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
		def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
		queueAndResolve(w, att, def, spellDef(10, 10, dt))
		return w.RNGCombat.State()
	}
	if state(core.Cold) != state(core.Chaos) {
		t.Error("chill consumed combat RNG; cold and chaos hits must draw identically")
	}
	if state(core.Lightning) != state(core.Fire) {
		t.Error("lightning and fire hits drew different RNG; shock must mirror ignite's single roll")
	}
}
