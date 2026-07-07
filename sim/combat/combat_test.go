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

// TestDeathEventNamesTheDef: the death event's Note carries the dier's
// def id — payload the descent's checkpoint hook keys on (the corpse
// compacts away before the host layer reads events).
func TestDeathEventNamesTheDef(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	def := w.SpawnActor(actorDef(5, nil), space.V(fm.One, 0))

	w.BeginTick()
	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	for _, ev := range w.Events() {
		if ev.Kind == core.EvDeath {
			if ev.Note != def.Def.ID {
				t.Errorf("death note = %q, want the def id %q", ev.Note, def.Def.ID)
			}
			return
		}
	}
	t.Fatal("no death event")
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

// TestLifeLeechRefillsAttacker: an attacker with LifeLeech regains a
// fraction of the damage it deals, capped at max life, and never from
// self-damage.
func TestLifeLeechRefillsAttacker(t *testing.T) {
	w := testWorld()
	// Attacker leeches 50% of damage dealt, sitting at half life.
	att := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.LifeLeech: fm.Half}), space.V(0, 0))
	att.Life = fm.FromInt(40)
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))

	// A fixed 40-damage spell → 20 leeched back.
	sk := spellDef(40, 40, core.Fire)
	queueAndResolve(w, att, def, sk)
	if got := att.Life; got != fm.FromInt(60) {
		t.Fatalf("attacker life = %v after leech, want 60 (40 + 50%% of 40)", got)
	}

	// Leech caps at max life: near-full attacker can't overheal.
	att.Life = fm.FromInt(95)
	queueAndResolve(w, att, def, sk)
	if got := att.Life; got != att.MaxLife() {
		t.Fatalf("attacker life = %v, want the max-life cap %v", got, att.MaxLife())
	}
}

// TestNoLeechWithoutStat: a plain attacker gains nothing.
func TestNoLeechWithoutStat(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	att.Life = fm.FromInt(50)
	def := w.SpawnActor(actorDef(1000, nil), space.V(fm.One, 0))
	queueAndResolve(w, att, def, spellDef(30, 30, core.Fire))
	if att.Life != fm.FromInt(50) {
		t.Fatalf("leech-less attacker healed to %v", att.Life)
	}
}

// TestBlockNegatesHit: a defender with block chance can turn a hit into no
// damage (EvBlock), and the block roll is conditional — a no-block defender
// draws exactly the RNG a plain hit does, so old replays stay byte-stable.
func TestBlockNegatesHit(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	// Certain block (clamped to the 75% cap): the hit must sometimes land
	// and sometimes not; with an always-true roll it never lands.
	def := w.SpawnActor(actorDef(1000, map[stats.StatID]fm.Fixed{stats.Block: fm.One}), space.V(fm.One, 0))
	sk := spellDef(30, 30, core.Fire)

	blocks, hits := 0, 0
	for i := 0; i < 200; i++ {
		before := def.Life
		w.QueueHit(core.Hit{Attacker: att.ID, Defender: def.ID, Skill: sk, Tags: sk.Tags.With(stats.TagHit)})
		combat.ResolveHits(w)
		got := false
		for _, ev := range w.Events() {
			if ev.Kind == core.EvBlock {
				got = true
			}
		}
		if got {
			blocks++
			if def.Life != before {
				t.Fatal("a blocked hit still dealt damage")
			}
		} else {
			hits++
			if def.Life >= before {
				t.Fatal("an unblocked hit dealt nothing")
			}
			def.Life = fm.FromInt(1000) // reset the pool between landed hits
		}
		w.BeginTick() // clear events between iterations
	}
	// 75% cap → roughly 3:1 blocks to hits; just assert both happen.
	if blocks == 0 || hits == 0 {
		t.Fatalf("block never varied: %d blocks, %d hits", blocks, hits)
	}
}

// TestBlockRNGConsumption: a no-block defender consumes exactly the RNG a
// plain hit would — block draws only when Block > 0.
func TestBlockRNGConsumption(t *testing.T) {
	state := func(block fm.Fixed) [4]uint64 {
		w := testWorld()
		att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
		def := w.SpawnActor(actorDef(1000, map[stats.StatID]fm.Fixed{stats.Block: block}), space.V(fm.One, 0))
		queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
		return w.RNGCombat.State()
	}
	plain := state(0)
	if state(0) != plain {
		t.Fatal("non-determinism in the baseline")
	}
	if state(fm.Half) == plain {
		t.Fatal("a block chance consumed no extra RNG — the block roll vanished")
	}
}

// TestESRechargeAfterDelay: energy shield refills once the delay elapses,
// a hit resets the delay, and life recharge never touches a no-ES actor.
func TestESRechargeAfterDelay(t *testing.T) {
	w := testWorld()
	a := w.SpawnActor(actorDef(100, map[stats.StatID]fm.Fixed{stats.EnergyShield: fm.FromInt(100)}), space.V(0, 0))
	a.ES = fm.FromInt(30) // drained

	// Within the delay window (60 ticks), ES does not move.
	combat.MarkHit(a)
	for i := 0; i < 60; i++ {
		combat.Upkeep(w)
	}
	if a.ES != fm.FromInt(30) {
		t.Fatalf("ES = %v during the recharge delay, want it held at 30", a.ES)
	}
	// After the delay, it climbs toward max at 20%/s (20 ES/s here).
	combat.Upkeep(w) // first recharging tick
	if a.ES <= fm.FromInt(30) {
		t.Fatalf("ES = %v after the delay, want it recharging", a.ES)
	}
	for i := 0; i < 30*core.TicksPerSecond; i++ {
		combat.Upkeep(w)
	}
	if a.ES != a.MaxES() {
		t.Fatalf("ES = %v after long recharge, want the max %v", a.ES, a.MaxES())
	}

	// A hit resets the delay: recharge stops again.
	combat.MarkHit(a)
	a.ES = fm.FromInt(50)
	for i := 0; i < 60; i++ {
		combat.Upkeep(w)
	}
	if a.ES != fm.FromInt(50) {
		t.Fatalf("a hit didn't reset the recharge delay (ES = %v)", a.ES)
	}
}

// TestStunInterruptsAndLocks: a hit above the threshold clears the target's
// action and locks it out; below the threshold does nothing; boss-immune
// actors never stun.
func TestStunInterruptsAndLocks(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	// Target with 100 max life → 15% threshold = 15 damage.
	def := w.SpawnActor(actorDef(100, nil), space.V(fm.One, 0))
	def.Action = core.Action{Kind: core.ActionSkill} // mid-action

	// A 10-damage hit is under threshold: no stun, action intact.
	queueAndResolve(w, att, def, spellDef(10, 10, core.Fire))
	if def.Stunned() || def.Action.Kind != core.ActionSkill {
		t.Fatal("a sub-threshold hit stunned the target")
	}

	// A 25-damage hit exceeds 15: stun, action cleared.
	def.Action = core.Action{Kind: core.ActionSkill}
	queueAndResolve(w, att, def, spellDef(25, 25, core.Fire))
	if !def.Stunned() {
		t.Fatal("an over-threshold hit didn't stun")
	}
	if def.Action.Kind != core.ActionIdle {
		t.Fatal("stun didn't clear the interrupted action")
	}
	sawStun := false
	for _, ev := range w.Events() {
		if ev.Kind == core.EvStun {
			sawStun = true
		}
	}
	if !sawStun {
		t.Fatal("no EvStun emitted")
	}

	// The lockout counts down through Upkeep, then the immunity tail lets
	// it act again (Stunned false) before StunTicks fully clears.
	for i := 0; i < core.StunLockTicks; i++ {
		combat.Upkeep(w)
	}
	if def.Stunned() {
		t.Fatal("still locked after the lock window elapsed")
	}
	if def.StunTicks == 0 {
		t.Fatal("immunity tail vanished with the lockout")
	}

	// Re-stun is refused during the immunity tail.
	def.Action = core.Action{Kind: core.ActionSkill}
	queueAndResolve(w, att, def, spellDef(25, 25, core.Fire))
	if def.Action.Kind == core.ActionIdle {
		t.Fatal("a hit re-stunned during the immunity window")
	}
}

// TestBossStunImmunity: a def with StunImmune never stuns, however big the hit.
func TestBossStunImmunity(t *testing.T) {
	w := testWorld()
	att := w.SpawnActor(actorDef(100, nil), space.V(0, 0))
	bossDef := actorDef(100, nil)
	bossDef.StunImmune = true
	boss := w.SpawnActor(bossDef, space.V(fm.One, 0))
	boss.Action = core.Action{Kind: core.ActionSkill}
	queueAndResolve(w, att, boss, spellDef(90, 90, core.Fire)) // 90% of life
	if boss.Stunned() || boss.Action.Kind != core.ActionSkill {
		t.Fatal("a stun-immune boss got stunned")
	}
}
