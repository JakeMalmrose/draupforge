package sim_test

// Staged skills — the deliberate action-model growth RISKS.md #1 called
// for. The contract: stage durations bind at use time, each stage's aim
// locks when its telegraph appears (tracked aims re-lock per stage), a
// blast is a binary in-or-out dodge, rings fire evenly spaced projectile
// circles, the caster is committed for the whole sequence, and none of the
// stage machinery itself consumes RNG.

import (
	"fmt"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// stagedFixture: an open-plane world with the Barrow King at the origin and
// a leveled-up player standing dist milli-units east — inside the slam
// zone, tough enough to eat the full sequence.
func stagedFixture(t *testing.T, seed uint64, dist int64) (*sim.Sim, core.EntityID, core.EntityID) {
	t.Helper()
	s := sim.New(content.DB(), seed)
	king := mustSpawn(t, s, "barrow_king", 0, 0)
	player := mustSpawn(t, s, "player", dist, 0)
	p := s.W.ActorByID(player)
	p.SetLevel(20)
	p.Life, p.Mana, p.ES = p.MaxLife(), p.MaxMana(), p.MaxES()
	return s, king, player
}

// castStaged issues the staged cast and asserts it was accepted.
func castStaged(t *testing.T, s *sim.Sim, caster, target core.EntityID, skill string) {
	t.Helper()
	aim := s.W.ActorByID(target).Pos
	s.Step([]core.Command{{Actor: caster, Kind: core.CmdUseSkill, Skill: skill, TargetID: target, Point: aim}})
	a := s.W.ActorByID(caster)
	if a.Action.Kind != core.ActionSkill || a.Action.Skill.ID != skill {
		t.Fatalf("cast of %s was rejected", skill)
	}
}

// hitsOn counts this tick's hit events landing on the given actor.
func hitsOn(s *sim.Sim, id core.EntityID) int {
	n := 0
	for _, ev := range s.W.LastEvents {
		if ev.Kind == core.EvHit && ev.Other == id {
			n++
		}
	}
	return n
}

// TestStagedSlamTimeline: barrow_slam's three blasts land on the exact
// stage-boundary ticks its authored timeline dictates, and the action ends
// after the recovery stage — the windup/effect/recovery arc generalized.
func TestStagedSlamTimeline(t *testing.T) {
	s, king, player := stagedFixture(t, 7, 2000)
	castStaged(t, s, king, player, "barrow_slam")

	// Stage ticks are [24, 15, 21, 27]; a stage fires when its countdown
	// (started the cast tick) reaches zero: ticks 23, 38, 59 after the
	// cast, with the action clearing at 86.
	wantHits := map[int]int{23: 1, 38: 1, 59: 1}
	for tick := 1; tick <= 86; tick++ {
		s.Step(nil)
		if got, want := hitsOn(s, player), wantHits[tick]; got != want {
			t.Fatalf("tick %d: %d hits on player, want %d", tick, got, want)
		}
	}
	if a := s.W.ActorByID(king); a.Action.Kind != core.ActionIdle {
		t.Fatalf("after the sequence the king is %v, want idle", a.Action.Kind)
	}
}

// TestStagedBlastDodgeAndTracking: leaving a locked zone dodges that blast,
// but the next stage re-locks onto where you fled — standing still there
// eats the finisher.
func TestStagedBlastDodgeAndTracking(t *testing.T) {
	s, king, player := stagedFixture(t, 7, 2000)
	castStaged(t, s, king, player, "barrow_slam")

	for tick := 1; tick <= 23; tick++ {
		s.Step(nil)
	}
	if got := hitsOn(s, player); got != 1 {
		t.Fatalf("first blast: %d hits, want 1", got)
	}
	// Flee after stage 2's zone locked (it locked on the same tick the
	// first blast fired): the second blast must whiff.
	flee := space.V(fm.FromInt(10), fm.FromInt(10))
	s.W.ActorByID(player).Pos = flee

	total := 1
	for tick := 24; tick <= 86; tick++ {
		s.Step(nil)
		total += hitsOn(s, player)
		if tick == 38 && total != 1 {
			t.Fatalf("dodged blast still hit (total %d)", total)
		}
	}
	// Stage 3 locked the fled position — the finisher tracked and landed.
	if total != 2 {
		t.Fatalf("sequence landed %d hits, want 2 (dodge one, tracked one)", total)
	}
}

// TestStagedRings: a ring stage fires one full, evenly spaced circle of the
// skill's projectiles; the storm's two rings double it, and the machinery
// consumes no combat RNG when nothing is hit.
func TestStagedRings(t *testing.T) {
	s, king, player := stagedFixture(t, 7, 30000) // far: nothing gets hit
	before := s.W.RNGCombat.State()

	castStaged(t, s, king, player, "grave_volley")
	for tick := 1; tick <= 26; tick++ {
		s.Step(nil)
	}
	live := 0
	dirs := map[string]bool{}
	for _, p := range s.W.Projectiles {
		if !p.Dead {
			live++
			dirs[fmt.Sprintf("%d,%d", p.Vel.X, p.Vel.Y)] = true
		}
	}
	if live != 10 {
		t.Fatalf("grave_volley ring: %d projectiles, want 10", live)
	}
	if len(dirs) != 10 {
		t.Fatalf("ring directions collapse: %d distinct of 10", len(dirs))
	}

	// Let the ring expire, then the storm: two rings, twenty bones.
	for s.W.ActorByID(king).Action.Kind != core.ActionIdle || len(s.W.Projectiles) > 0 {
		s.Step(nil)
	}
	castStaged(t, s, king, player, "grave_storm")
	spawned := map[core.EntityID]bool{}
	for tick := 1; tick <= 40; tick++ {
		s.Step(nil)
		for _, p := range s.W.Projectiles {
			spawned[p.ID] = true
		}
	}
	if len(spawned) != 20 {
		t.Fatalf("grave_storm: %d projectiles, want 20", len(spawned))
	}

	// Nothing was hit and these skills don't wiggle: the combat stream
	// never moved. Every future stage effect keeps this property or pins
	// its own consumption.
	for s.W.ActorByID(king).Action.Kind != core.ActionIdle || len(s.W.Projectiles) > 0 {
		s.Step(nil)
	}
	if s.W.RNGCombat.State() != before {
		t.Fatal("staged machinery consumed combat RNG without hitting anything")
	}
}

// TestStagedCommitted: a staged caster is committed — move commands during
// the sequence are dropped, same as legacy windup.
func TestStagedCommitted(t *testing.T) {
	s, king, player := stagedFixture(t, 7, 2000)
	castStaged(t, s, king, player, "barrow_slam")
	pos := s.W.ActorByID(king).Pos
	s.Step([]core.Command{{Actor: king, Kind: core.CmdMove, Point: space.V(fm.FromInt(20), 0)}})
	a := s.W.ActorByID(king)
	if a.Action.Kind != core.ActionSkill || a.Pos != pos {
		t.Fatal("staged caster accepted a move mid-sequence")
	}
}

// TestStagedSaveRoundTrip: saving mid-sequence and loading continues
// bit-exactly — stage index, bound durations, and the locked aim survive.
func TestStagedSaveRoundTrip(t *testing.T) {
	s, king, player := stagedFixture(t, 11, 2000)
	castStaged(t, s, king, player, "barrow_slam")
	for tick := 1; tick <= 30; tick++ { // mid stage 1, aim locked
		s.Step(nil)
	}
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	if restored.W.Hash() != s.W.Hash() {
		t.Fatal("restored world hashes differently at the boundary")
	}
	for tick := 0; tick < 80; tick++ {
		s.Step(nil)
		restored.Step(nil)
		if s.W.Hash() != restored.W.Hash() {
			t.Fatalf("hash diverged %d ticks after restore", tick+1)
		}
	}
}

// TestTelegraphedBlastsIgnoreEvasion: a blast that catches you always
// lands — the dodge is spatial, not an evasion roll (and the skipped
// accuracy roll is pinned RNG behavior: dodging cleanly must consume no
// combat randomness either way).
func TestTelegraphedBlastsIgnoreEvasion(t *testing.T) {
	s, king, player := stagedFixture(t, 7, 2000)
	p := s.W.ActorByID(player)
	p.Sheet.Add(stats.Modifier{
		Stat: stats.Evasion, Layer: stats.LayerFlat,
		Value: fm.FromInt(1000000), Source: 424242,
	})
	castStaged(t, s, king, player, "barrow_slam")
	hits := 0
	for tick := 1; tick <= 86; tick++ {
		s.Step(nil)
		hits += hitsOn(s, player)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvMiss && ev.Actor == king {
				t.Fatal("a telegraphed blast rolled evasion and missed")
			}
		}
	}
	if hits != 3 {
		t.Fatalf("evasion-stacked player took %d blast hits, want all 3", hits)
	}
}

// TestBossKingPhases: skill selection is a pure function of distance and
// life — slam in close, volley at range, storm once below half life.
func TestBossKingPhases(t *testing.T) {
	cases := []struct {
		name  string
		dist  int64
		wound bool
		want  string
	}{
		{"close", 4000, false, "barrow_slam"},
		{"ranged", 15000, false, "grave_volley"},
		{"enraged", 15000, true, "grave_storm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, king, _ := stagedFixture(t, 7, tc.dist)
			k := s.W.ActorByID(king)
			if tc.wound {
				k.Life = fm.Div(k.MaxLife(), fm.FromInt(3))
			}
			s.Step(nil) // AI decides
			if k.Action.Kind != core.ActionSkill || k.Action.Skill.ID != tc.want {
				got := "idle/move"
				if k.Action.Kind == core.ActionSkill {
					got = k.Action.Skill.ID
				}
				t.Fatalf("king chose %s, want %s", got, tc.want)
			}
		})
	}
}
