package combat_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/combat"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// Test fixtures build minimal worlds with hand-made defs — no content
// package needed, every number under the test's control.

func testWorld() *core.World {
	return core.NewWorld(&core.ContentDB{}, 12345)
}

func actorDef(life int64, mods map[stats.StatID]fm.Fixed) *core.ActorDef {
	def := &core.ActorDef{ID: "test", Radius: fm.Half}
	def.BaseStats[stats.Life] = fm.FromInt(life)
	def.BaseStats[stats.DamageTaken] = fm.One
	def.BaseStats[stats.CritMulti] = fm.FromMilli(1500)
	for k, v := range mods {
		def.BaseStats[k] = v
	}
	return def
}

func spellDef(min, max int64, dt core.DamageType) *core.SkillDef {
	sk := &core.SkillDef{
		ID: "test_spell", Kind: core.SkillProjectile,
		Tags:          stats.T(stats.TagSpell, dt.Tag()),
		Effectiveness: fm.One,
	}
	sk.BaseMin[dt] = fm.FromInt(min)
	sk.BaseMax[dt] = fm.FromInt(max)
	return sk
}

func queueAndResolve(w *core.World, att, def *core.Actor, sk *core.SkillDef) {
	w.QueueHit(core.Hit{
		Attacker: att.ID, Defender: def.ID, Skill: sk,
		Tags: sk.Tags.With(stats.TagHit),
	})
	combat.ResolveHits(w)
}

func TestSpellAlwaysHitsAndDamages(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	// Massive evasion must not matter: spells skip the hit check.
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.Evasion: fm.FromInt(100000)}), space.V(fm.One, 0))

	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	if def.Life != fm.FromInt(90) {
		t.Errorf("life after flat 10 fire spell = %d, want 90000", def.Life)
	}
}

func TestResistanceMitigation(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.FireRes: fm.FromMilli(500)}), space.V(fm.One, 0))

	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	if def.Life != fm.FromInt(95) {
		t.Errorf("life after 10 fire vs 50%% res = %d, want 95000", def.Life)
	}
}

func TestResistanceCapAt75(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.FireRes: fm.FromMilli(2000)}), space.V(fm.One, 0))

	queueAndResolve(w, att, def, spellDef(40, 40, core.Fire))
	// 200% res capped to 75%: 40 × 0.25 = 10 damage through.
	if def.Life != fm.FromInt(90) {
		t.Errorf("life after 40 fire vs capped res = %d, want 90000", def.Life)
	}
}

func TestEnergyShieldAbsorbsFirst(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.EnergyShield: fm.FromInt(6)}), space.V(fm.One, 0))

	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	if def.ES != 0 {
		t.Errorf("ES = %d, want 0 (fully absorbed)", def.ES)
	}
	if def.Life != fm.FromInt(96) {
		t.Errorf("life = %d, want 96000 (only overflow hits life)", def.Life)
	}
}

func TestArmourMitigatesPhysical(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.Armour: fm.FromInt(100)}), space.V(fm.One, 0))

	sk := spellDef(10, 10, core.Physical) // spell-tagged to skip evasion roll
	queueAndResolve(w, att, def, sk)
	// reduction = 100/(100+10×10) = 50% → 5 through.
	if def.Life != fm.FromInt(95) {
		t.Errorf("life after 10 phys vs 100 armour = %d, want 95000", def.Life)
	}
}

func TestDeathEmitsEventOnce(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(5, nil), space.V(fm.One, 0))

	w.BeginTick()
	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire)) // hit the corpse

	deaths := 0
	for _, ev := range w.Events() {
		if ev.Kind == core.EvDeath {
			deaths++
		}
	}
	if deaths != 1 {
		t.Errorf("death events = %d, want exactly 1", deaths)
	}
	if !def.Dead {
		t.Error("defender not marked dead")
	}
}

func TestIgniteAppliesAndTicks(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))

	sk := spellDef(30, 30, core.Fire)
	sk.IgniteChance = fm.One // guaranteed
	queueAndResolve(w, att, def, sk)

	if len(def.DoTs) != 1 {
		t.Fatalf("DoTs = %d, want 1 ignite", len(def.DoTs))
	}
	// ignite dps = 50% of the 30 fire hit = 15/s → 0.5/tick for 4s.
	wantPerTick := fm.Div(fm.FromInt(15), fm.FromInt(core.TicksPerSecond))
	if def.DoTs[0].PerTick != wantPerTick {
		t.Errorf("ignite per-tick = %d, want %d", def.DoTs[0].PerTick, wantPerTick)
	}

	lifeBefore := def.Life
	for i := 0; i < 4*core.TicksPerSecond; i++ {
		combat.TickDoTs(w)
	}
	if len(def.DoTs) != 0 {
		t.Error("ignite did not expire after 4s")
	}
	burned := lifeBefore - def.Life
	if burned != fm.Mul(wantPerTick, fm.FromInt(4*core.TicksPerSecond)) {
		t.Errorf("total burn = %d, want %d", burned, fm.Mul(wantPerTick, fm.FromInt(4*core.TicksPerSecond)))
	}
}

func TestStrongerIgniteReplacesWeaker(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))

	weak := spellDef(10, 10, core.Fire)
	weak.IgniteChance = fm.One
	strong := spellDef(40, 40, core.Fire)
	strong.IgniteChance = fm.One

	queueAndResolve(w, att, def, weak)
	queueAndResolve(w, att, def, strong)
	if len(def.DoTs) != 1 {
		t.Fatalf("DoTs = %d, want 1 (no stacking)", len(def.DoTs))
	}
	wantPerTick := fm.Div(fm.FromInt(20), fm.FromInt(core.TicksPerSecond))
	if def.DoTs[0].PerTick != wantPerTick {
		t.Errorf("surviving ignite per-tick = %d, want the stronger %d", def.DoTs[0].PerTick, wantPerTick)
	}

	// And the weak one must not overwrite it back.
	queueAndResolve(w, att, def, weak)
	if def.DoTs[0].PerTick != wantPerTick {
		t.Error("weaker ignite overwrote a stronger one")
	}
}

func TestAddedFlatDamageScalesWithModifiers(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))

	// +5 flat fire, 50% increased fire, 20% more spell damage on a 10 base:
	// (10+5) × 1.5 × 1.2 = 27.
	att.Sheet.Add(stats.Modifier{Stat: stats.Damage, Layer: stats.LayerFlat, Value: fm.FromInt(5), Tags: stats.T(stats.TagFire)})
	att.Sheet.Add(stats.Modifier{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(500), Tags: stats.T(stats.TagFire)})
	att.Sheet.Add(stats.Modifier{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(200), Tags: stats.T(stats.TagSpell)})

	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	if got := fm.FromInt(1000) - def.Life; got != fm.FromInt(27) {
		t.Errorf("damage dealt = %d, want 27000", got)
	}
}

// TestCritFlagOnHitEvent: a guaranteed crit marks its hit event, a
// guaranteed non-crit doesn't — the flag the client keys damage-number
// emphasis off.
func TestCritFlagOnHitEvent(t *testing.T) {
	for _, tc := range []struct {
		chance fm.Fixed
		want   bool
	}{
		{fm.One, true},
		{0, false},
	} {
		w := testWorld()
		att := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.CritChance: tc.chance}), space.V(0, 0))
		def := w.SpawnActor(actorDef(100, nil), space.V(fm.One, 0))
		queueAndResolve(w, att, def, spellDef(5, 5, core.Fire))
		var hit *core.Event
		for i, ev := range w.Events() {
			if ev.Kind == core.EvHit {
				hit = &w.Events()[i]
			}
		}
		if hit == nil {
			t.Fatal("no hit event")
		}
		if hit.Crit != tc.want {
			t.Errorf("crit chance %v: event crit = %v, want %v", tc.chance, hit.Crit, tc.want)
		}
	}
}
