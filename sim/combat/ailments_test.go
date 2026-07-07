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
	if got := def.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(7) {
		t.Errorf("move speed under 30%% chill = %d, want 7000", got)
	}
	if got := def.Sheet.Eval(stats.CastSpeed, stats.TagSet{}); got != fm.FromMilli(700) {
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
	if got := def.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(10) {
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
	if got := def.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromMilli(7600) {
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

func TestBleedAppliesPhysicalDoT(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
	sk := spellDef(10, 10, core.Physical)
	sk.BleedChance = fm.One

	queueAndResolve(w, att, def, sk)

	if len(def.DoTs) != 1 || def.DoTs[0].Type != core.Physical {
		t.Fatalf("DoTs after certain bleed = %+v, want one physical", def.DoTs)
	}
	// bleed dps = 35% of the 10 phys hit; per-tick is that over the tick rate.
	wantPerTick := fm.Div(fm.Mul(fm.FromInt(10), fm.Fixed(350)), fm.FromInt(core.TicksPerSecond))
	if d := def.DoTs[0]; d.PerTick != wantPerTick {
		t.Errorf("bleed per tick = %d, want %d", d.PerTick, wantPerTick)
	}
	if d := def.DoTs[0]; d.TicksLeft != 6*core.TicksPerSecond {
		t.Errorf("bleed duration = %d ticks, want %d", d.TicksLeft, 6*core.TicksPerSecond)
	}
}

func TestStrongerBleedReplacesWeakerOnly(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(10000, nil), space.V(fm.One, 0))
	big := spellDef(20, 20, core.Physical)
	big.BleedChance = fm.One
	small := spellDef(5, 5, core.Physical)
	small.BleedChance = fm.One

	queueAndResolve(w, att, def, big)
	if len(def.DoTs) != 1 {
		t.Fatalf("no bleed from certain-chance hit")
	}
	strong := def.DoTs[0].PerTick
	queueAndResolve(w, att, def, small)
	if len(def.DoTs) != 1 {
		t.Fatalf("bleeds stacked: %d DoTs, want 1 (strongest wins)", len(def.DoTs))
	}
	if def.DoTs[0].PerTick != strong {
		t.Errorf("weaker bleed changed the strong one: %d, want %d", def.DoTs[0].PerTick, strong)
	}
}

// TestBleedRNGConsumption pins bleed's conditional-consumption contract:
// with no bleed chance anywhere a physical hit draws exactly what a chaos
// hit draws (damage + crit) — every existing replay stays byte-stable —
// and once any chance is present, the single roll mirrors ignite's, so a
// physical hit with bleed chance draws what a fire hit draws.
func TestBleedRNGConsumption(t *testing.T) {
	state := func(dt core.DamageType, bleedChance fm.Fixed) [4]uint64 {
		w := testWorld()
		att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
		def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
		sk := spellDef(10, 10, dt)
		sk.BleedChance = bleedChance
		queueAndResolve(w, att, def, sk)
		return w.RNGCombat.State()
	}
	if state(core.Physical, 0) != state(core.Chaos, 0) {
		t.Error("chanceless physical hit consumed combat RNG; physical and chaos must draw identically")
	}
	if state(core.Physical, fm.Half) != state(core.Fire, 0) {
		t.Error("physical hit with bleed chance must draw exactly one ailment roll, like fire's ignite")
	}
}

// TestBleedChanceFromSheet pins the gear path: a flat BleedChance modifier
// on the attacker's sheet (what the bleed_chance affix grants) bleeds
// without any skill base chance.
func TestBleedChanceFromSheet(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
	att.Sheet.Add(stats.Modifier{Stat: stats.BleedChance, Layer: stats.LayerFlat, Value: fm.One, Source: 42})

	queueAndResolve(w, att, def, spellDef(10, 10, core.Physical))
	if len(def.DoTs) != 1 || def.DoTs[0].Type != core.Physical {
		t.Fatalf("sheet bleed chance did not bleed: DoTs = %+v", def.DoTs)
	}
}

// TestBleedChanceFromSupportFold pins the support path: a cast context
// carrying a flat BleedChance support mod (Rupture's shape) bleeds without
// any sheet stat — rollBleed folds the chance query like damage queries.
func TestBleedChanceFromSupportFold(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
	sk := spellDef(10, 10, core.Physical)
	sup := &core.SupportDef{ID: "test_rupture", Mods: []core.BuffMod{
		{Stat: stats.BleedChance, Layer: stats.LayerFlat, Value: fm.One},
	}}

	w.QueueHit(core.Hit{
		Attacker: att.ID, Defender: def.ID, Skill: sk,
		Tags: sk.Tags.With(stats.TagHit),
		Gem:  core.GemCtx{Supports: []*core.SupportDef{sup}},
	})
	combat.ResolveHits(w)
	if len(def.DoTs) != 1 || def.DoTs[0].Type != core.Physical {
		t.Fatalf("support-granted bleed chance did not bleed: DoTs = %+v", def.DoTs)
	}
}

func TestPoisonStacksInstances(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(10000, nil), space.V(fm.One, 0))
	sk := spellDef(10, 10, core.Physical)
	sk.PoisonChance = fm.One

	queueAndResolve(w, att, def, sk)
	queueAndResolve(w, att, def, sk)

	if len(def.DoTs) != 2 {
		t.Fatalf("poison instances = %d, want 2 — poison stacks, it never merges", len(def.DoTs))
	}
	for i, d := range def.DoTs {
		if d.Type != core.Chaos {
			t.Errorf("instance %d type = %v, want chaos", i, d.Type)
		}
		if d.TicksLeft != 2*core.TicksPerSecond {
			t.Errorf("instance %d duration = %d ticks, want %d", i, d.TicksLeft, 2*core.TicksPerSecond)
		}
	}
}

func TestPoisonScalesPhysPlusChaos(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(10000, nil), space.V(fm.One, 0))
	sk := spellDef(10, 10, core.Physical)
	sk.BaseMin[core.Chaos] = fm.FromInt(10)
	sk.BaseMax[core.Chaos] = fm.FromInt(10)
	sk.PoisonChance = fm.One

	queueAndResolve(w, att, def, sk)

	if len(def.DoTs) != 1 {
		t.Fatalf("no poison from certain-chance hit")
	}
	// poison dps = 30% of the 20 phys+chaos total; per-tick over the tick rate.
	want := fm.Div(fm.Mul(fm.FromInt(20), fm.Fixed(300)), fm.FromInt(core.TicksPerSecond))
	if got := def.DoTs[0].PerTick; got != want {
		t.Errorf("poison per tick = %d, want %d (phys+chaos basis)", got, want)
	}
}

// TestPoisonRNGConsumption pins poison's conditional-consumption contract,
// same shape as bleed's: no chance anywhere → no draw (physical equals the
// chaos-hit baseline), chance present → exactly one ailment roll (equals a
// fire hit's ignite draw).
func TestPoisonRNGConsumption(t *testing.T) {
	state := func(dt core.DamageType, poisonChance fm.Fixed) [4]uint64 {
		w := testWorld()
		att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
		def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
		sk := spellDef(10, 10, dt)
		sk.PoisonChance = poisonChance
		queueAndResolve(w, att, def, sk)
		return w.RNGCombat.State()
	}
	if state(core.Physical, 0) != state(core.Chaos, 0) {
		t.Error("chanceless physical hit consumed combat RNG; physical and chaos must draw identically")
	}
	if state(core.Physical, fm.Half) != state(core.Fire, 0) {
		t.Error("physical hit with poison chance must draw exactly one ailment roll, like fire's ignite")
	}
}

// TestPoisonChanceFromSupportFold pins the support path (Envenom's shape):
// a cast context carrying a flat PoisonChance support mod poisons without
// any sheet stat or skill base chance.
func TestPoisonChanceFromSupportFold(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
	sk := spellDef(10, 10, core.Physical)
	sup := &core.SupportDef{ID: "test_envenom", Mods: []core.BuffMod{
		{Stat: stats.PoisonChance, Layer: stats.LayerFlat, Value: fm.One},
	}}

	w.QueueHit(core.Hit{
		Attacker: att.ID, Defender: def.ID, Skill: sk,
		Tags: sk.Tags.With(stats.TagHit),
		Gem:  core.GemCtx{Supports: []*core.SupportDef{sup}},
	})
	combat.ResolveHits(w)
	if len(def.DoTs) != 1 || def.DoTs[0].Type != core.Chaos {
		t.Fatalf("support-granted poison chance did not poison: DoTs = %+v", def.DoTs)
	}
}
