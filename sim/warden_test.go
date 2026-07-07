package sim_test

// Item 7's set-piece: the Ashen Warden curses and channels through his real
// AI, and the new crit supports fold at the pipeline.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// TestWardenHexesFromRange: outside slam range with line of sight, the
// warden's normal ranged behavior is the hex — the player ends up cursed
// with hex_of_embers.
func TestWardenHexesFromRange(t *testing.T) {
	s := sim.New(content.DB(), 91)
	player := mustSpawn(t, s, "player", 0, 0)
	mustSpawn(t, s, "ashen_warden", 9000, 0)
	p := s.W.ActorByID(player)

	for i := 0; i < 10*core.TicksPerSecond; i++ {
		s.Step(nil)
		if c := curseOn(p); c != nil {
			if c.ID != "hex_of_embers" {
				t.Fatalf("cursed with %s, want hex_of_embers", c.ID)
			}
			if got, want := p.Sheet.Eval(stats.FireRes, stats.TagSet{}), fm.FromMilli(-200); got != want {
				t.Errorf("hexed fire res = %d, want %d", got, want)
			}
			return
		}
	}
	t.Fatal("the warden never hexed the player")
}

// TestWardenChannelsWhenEnraged: below half life the ranged pick is the
// flame gout — a channelled skill; the warden holds PhaseChannel and
// projectiles flow until his mana pool runs dry.
func TestWardenChannelsWhenEnraged(t *testing.T) {
	s := sim.New(content.DB(), 92)
	player := mustSpawn(t, s, "player", 0, 0)
	wid := mustSpawn(t, s, "ashen_warden", 9000, 0)
	w := s.W.ActorByID(wid)
	w.Life = fm.Div(w.MaxLife(), fm.FromInt(3)) // enraged
	_ = player

	channelled := false
	for i := 0; i < 15*core.TicksPerSecond; i++ {
		s.Step(nil)
		if w.Action.Kind == core.ActionSkill && w.Action.Phase == core.PhaseChannel {
			channelled = true
			break
		}
	}
	if !channelled {
		t.Fatal("the enraged warden never channelled")
	}
	if len(s.W.Projectiles) == 0 {
		// The first gout fires at the windup's end, before the loop.
		for i := 0; i < 10 && len(s.W.Projectiles) == 0; i++ {
			s.Step(nil)
		}
	}
	if len(s.W.Projectiles) == 0 {
		t.Error("the channel spat no flame")
	}
}

// TestCritSupportsFold pins the pipeline fold: a support granting crit
// chance forces crits (chance 1 via fold), and a crit-multi support scales
// the hit by exactly the folded multiplier.
func TestCritSupportsFold(t *testing.T) {
	s := sim.New(content.DB(), 93)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	dummy := mustSpawn(t, s, "training_dummy", 6000, 0)
	d := s.W.ActorByID(dummy)
	a.Sheet.SetBase(stats.CritChance, 0)

	sk := &core.SkillDef{ID: "probe", Kind: core.SkillProjectile, Tags: stats.T(stats.TagSpell, stats.TagFire), Effectiveness: fm.One}
	sk.BaseMin[core.Fire] = fm.FromInt(100)
	sk.BaseMax[core.Fire] = fm.FromInt(100)
	sup := &core.SupportDef{ID: "test_crit", Mods: []core.BuffMod{
		{Stat: stats.CritChance, Layer: stats.LayerFlat, Value: fm.One},
		{Stat: stats.CritMulti, Layer: stats.LayerFlat, Value: fm.FromMilli(500)},
	}}
	s.W.QueueHit(core.Hit{
		Attacker: core.EntityID(player), Defender: core.EntityID(dummy), Skill: sk,
		Tags: sk.Tags.With(stats.TagHit),
		Gem:  core.GemCtx{Supports: []*core.SupportDef{sup}},
	})
	s.Step(nil)
	var dealt fm.Fixed
	for _, ev := range s.W.LastEvents {
		if ev.Kind == core.EvHit && ev.Note == "probe" {
			dealt = ev.Amount
		}
	}
	_ = d
	// Certain crit at (1.5 base + 0.5 folded) multi = 100 × 2 = 200.
	if got, want := dealt, fm.FromInt(200); got != want {
		t.Errorf("folded crit hit dealt %d, want %d", got, want)
	}
}
