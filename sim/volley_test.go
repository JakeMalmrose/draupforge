package sim_test

// The anti-shotgun rule: one cast damages each target at most once. A
// multi-projectile fan shares a volley id; a target the volley already
// damaged is flown past (pass-through) and any queued sibling hit on it is
// dropped in the pipeline. Extra projectiles buy coverage, never stacked
// single-target damage — the session-70 balance patch's centerpiece.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// fanCast readies a player with fireball + Greater Multiple Projectiles
// (five-wide fan) and a tanky dummy dead ahead.
func fanCast(t *testing.T) (*sim.Sim, core.EntityID, core.EntityID) {
	t.Helper()
	s := sim.New(content.DB(), 11)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "fireball")
	p := s.W.ActorByID(player)
	g := &p.Gems[len(p.Gems)-1]
	g.Sockets = 1
	for _, sup := range s.W.Content.Supports {
		if sup.ID == "greater_projectiles" {
			g.Supports = []*core.SupportDef{sup}
		}
	}
	if len(g.Supports) == 0 {
		t.Fatal("greater_projectiles support missing from content")
	}
	p.Mana = fm.FromInt(500)
	dummy := mustSpawn(t, s, "training_dummy", 4000, 0)
	s.W.ActorByID(dummy).SetLevel(20) // plenty of life to count hits on
	s.W.ActorByID(dummy).Life = s.W.ActorByID(dummy).MaxLife()
	return s, player, dummy
}

func countHitsOn(s *sim.Sim, target core.EntityID, ticks int) int {
	hits := 0
	for i := 0; i < ticks; i++ {
		s.Step(nil)
		for _, ev := range s.W.Events() {
			if ev.Kind == core.EvHit && ev.Other == target {
				hits++
			}
		}
	}
	return hits
}

func TestVolleyDamagesTargetOnce(t *testing.T) {
	s, player, dummy := fanCast(t)
	castOnce(t, s, player, "fireball", space.V(fm.FromInt(4), 0))
	if hits := countHitsOn(s, dummy, 40); hits != 1 {
		t.Fatalf("a five-projectile cast damaged the target %d times, want exactly 1", hits)
	}
}

func TestVolleyDoesNotBlockNextCast(t *testing.T) {
	s, player, dummy := fanCast(t)
	castOnce(t, s, player, "fireball", space.V(fm.FromInt(4), 0))
	first := countHitsOn(s, dummy, 40)
	for s.W.ActorByID(player).Action.Kind == core.ActionSkill {
		s.Step(nil) // recovery
	}
	castOnce(t, s, player, "fireball", space.V(fm.FromInt(4), 0))
	second := countHitsOn(s, dummy, 40)
	if first != 1 || second != 1 {
		t.Fatalf("hits per cast = %d then %d, want 1 and 1 — a new cast is a new volley", first, second)
	}
}

// A solo projectile (no fan) carries no volley and behaves exactly as
// before — the anti-shotgun memory only engages for real fans.
func TestSoloProjectileHasNoVolley(t *testing.T) {
	s := sim.New(content.DB(), 11)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "fireball")
	s.W.ActorByID(player).Mana = fm.FromInt(500)
	castOnce(t, s, player, "fireball", space.V(fm.FromInt(4), 0))
	for _, p := range s.W.Projectiles {
		if p.Volley != 0 {
			t.Fatalf("solo cast projectile carries volley %d, want 0", p.Volley)
		}
	}
}
