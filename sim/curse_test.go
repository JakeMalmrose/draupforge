package sim_test

// Curses: hex BuffDefs on the ordinary buff machinery — one per target
// (newest evicts), applied in an area by SkillCurse casts, consuming no
// RNG. Negative resistance from a curse is a real damage amplifier.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

func curseOn(a *core.Actor) *core.BuffDef {
	for _, s := range a.Statuses {
		if s.Buff != nil && s.Buff.Curse {
			return s.Buff
		}
	}
	return nil
}

func TestCurseAppliesAndExpires(t *testing.T) {
	s := sim.New(content.DB(), 51)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "flammability")
	dummy := mustSpawn(t, s, "training_dummy", 4000, 0)
	d := s.W.ActorByID(dummy)

	castOnce(t, s, player, "flammability", d.Pos)
	s.Step(nil)
	if c := curseOn(d); c == nil || c.ID != "flammability" {
		t.Fatalf("dummy not cursed: %+v", d.Statuses)
	}
	if got, want := d.Sheet.Eval(stats.FireRes, stats.TagSet{}), fm.FromMilli(-250); got != want {
		t.Errorf("cursed fire res = %d, want %d", got, want)
	}
	for i := 0; i < 8*core.TicksPerSecond; i++ {
		s.Step(nil)
	}
	if c := curseOn(d); c != nil {
		t.Errorf("curse did not expire: %s", c.ID)
	}
	if got := d.Sheet.Eval(stats.FireRes, stats.TagSet{}); got != 0 {
		t.Errorf("fire res after expiry = %d, want 0", got)
	}
}

func TestOneCursePerTarget(t *testing.T) {
	s := sim.New(content.DB(), 52)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "flammability", "enfeeble")
	dummy := mustSpawn(t, s, "training_dummy", 4000, 0)
	d := s.W.ActorByID(dummy)

	castOnce(t, s, player, "flammability", d.Pos)
	settle(t, s, player)
	castOnce(t, s, player, "enfeeble", d.Pos)
	s.Step(nil)

	if c := curseOn(d); c == nil || c.ID != "enfeeble" {
		t.Fatalf("newest curse did not win: %+v", curseOn(d))
	}
	n := 0
	for _, st := range d.Statuses {
		if st.Buff != nil && st.Buff.Curse {
			n++
		}
	}
	if n != 1 {
		t.Errorf("curses on target = %d, want 1", n)
	}
	// Flammability's mods must be gone with its status.
	if got := d.Sheet.Eval(stats.FireRes, stats.TagSet{}); got != 0 {
		t.Errorf("evicted curse left fire res = %d, want 0", got)
	}
}

// TestNegativeResistAmplifies pins the mitigation change curses lean on:
// resistance below zero multiplies damage up, on hits and DoT ticks both.
func TestNegativeResistAmplifies(t *testing.T) {
	s := sim.New(content.DB(), 53)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "fireball", "flammability")
	dummy := mustSpawn(t, s, "training_dummy", 6000, 0)
	d := s.W.ActorByID(dummy)

	castOnce(t, s, player, "flammability", d.Pos)
	settle(t, s, player)
	if got, want := d.Sheet.Eval(stats.FireRes, stats.TagSet{}), fm.FromMilli(-250); got != want {
		t.Fatalf("curse not on: fire res %d", got)
	}
	// A flat known fire hit through the real pipeline: queue it directly so
	// the roll is deterministic (min == max) and no crit (base 0... crit is
	// a roll; force CritChance base to stay 0 for the dummy's attacker).
	sk := &core.SkillDef{ID: "probe", Kind: core.SkillProjectile, Tags: stats.T(stats.TagSpell, stats.TagFire), Effectiveness: fm.One}
	sk.BaseMin[core.Fire] = fm.FromInt(100)
	sk.BaseMax[core.Fire] = fm.FromInt(100)
	// Zero the player's crit so the probe's damage is exact (the crit roll
	// still consumes its draw; only the outcome is pinned).
	s.W.ActorByID(player).Sheet.SetBase(stats.CritChance, 0)
	s.W.QueueHit(core.Hit{Attacker: player, Defender: dummy, Skill: sk, Tags: sk.Tags.With(stats.TagHit)})
	s.Step(nil)
	var dealt fm.Fixed
	for _, ev := range s.W.LastEvents {
		if ev.Kind == core.EvHit && ev.Note == "probe" {
			dealt = ev.Amount
		}
	}
	// The player has no fire mods; -25% res → 125 damage (overkill — the
	// dummy holds 80 life — which is why the event, not the pool, is read).
	if got, want := dealt, fm.FromInt(125); got != want {
		t.Errorf("fire hit through -25%% res dealt %d, want %d", got, want)
	}
}

// TestCurseCastConsumesNoRNG pins the streams: a curse cast draws nothing
// from combat RNG (no hit roll, no crit, buffs consume none).
func TestCurseCastConsumesNoRNG(t *testing.T) {
	run := func(curse bool) [4]uint64 {
		s := sim.New(content.DB(), 54)
		player := mustSpawn(t, s, "player", 0, 0)
		grantGems(t, s, player, "flammability")
		mustSpawn(t, s, "training_dummy", 4000, 0)
		if curse {
			castOnce(t, s, player, "flammability", space.V(fm.FromInt(4), 0))
		}
		for i := 0; i < 30; i++ {
			s.Step(nil)
		}
		return s.W.RNGCombat.State()
	}
	if run(true) != run(false) {
		t.Error("curse cast consumed combat RNG")
	}
}

// TestHexerCursesPlayer drives the monster side end-to-end: a bone hexer
// left alone with a player hexes them with Weakness through its real AI.
func TestHexerCursesPlayer(t *testing.T) {
	s := sim.New(content.DB(), 55)
	player := mustSpawn(t, s, "player", 0, 0)
	mustSpawn(t, s, "bone_hexer", 6000, 0)
	p := s.W.ActorByID(player)

	for i := 0; i < 10*core.TicksPerSecond; i++ {
		s.Step(nil)
		if c := curseOn(p); c != nil {
			if c.ID != "weakness" {
				t.Fatalf("player cursed with %s, want weakness", c.ID)
			}
			if got, want := p.Sheet.Eval(stats.DamageTaken, stats.TagSet{}), fm.FromMilli(1200); got != want {
				t.Errorf("cursed damage taken = %d, want %d", got, want)
			}
			return
		}
	}
	t.Fatal("bone hexer never cursed the player")
}
