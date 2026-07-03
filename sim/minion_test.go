package sim_test

// The first minion skill and its supporting design (RISKS #2's second act):
// summons ride the spawn queue at the gem's level, the cap despawns the
// oldest quietly (no death, no loot), minion AI heels to its owner and
// leashes to the owner's position, and every kill a minion makes credits
// the summoner — XP, flask charges, orbs.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// summonOnce casts summon_skeleton and steps through the windup.
func summonOnce(t *testing.T, s *sim.Sim, caster core.EntityID) {
	t.Helper()
	s.Step([]core.Command{{Actor: caster, Kind: core.CmdUseSkill, Skill: "summon_skeleton"}})
	a := s.W.ActorByID(caster)
	if a.Action.Kind != core.ActionSkill {
		t.Fatal("summon cast was rejected")
	}
	for a.Action.Kind == core.ActionSkill && a.Action.Phase == core.PhaseWindup {
		s.Step(nil)
	}
}

func minionsOf(s *sim.Sim, owner core.EntityID) []*core.Actor {
	var out []*core.Actor
	for _, a := range s.W.Actors {
		if !a.Dead && a.Owner == owner {
			out = append(out, a)
		}
	}
	return out
}

func TestSummonSkeleton(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "summon_skeleton")
	p := s.W.ActorByID(player)
	p.Gems[len(p.Gems)-1].Level = 5 // minions fight at the gem's level
	p.Mana = fm.FromInt(200)

	summonOnce(t, s, player)
	minions := minionsOf(s, player)
	if len(minions) != 1 {
		t.Fatalf("%d minions after one cast, want 1", len(minions))
	}
	m := minions[0]
	if m.Def.ID != "skeleton_warrior" || m.Team != core.TeamPlayers {
		t.Fatalf("minion = %s team %v, want a player-team skeleton", m.Def.ID, m.Team)
	}
	if m.Level != 5 {
		t.Fatalf("minion level = %d, want the gem's 5", m.Level)
	}
}

func TestSummonCapDespawnsOldest(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "summon_skeleton")
	s.W.ActorByID(player).Mana = fm.FromInt(500)

	var ids []core.EntityID
	for i := 0; i < 4; i++ {
		s.W.ActorByID(player).Mana = fm.FromInt(50) // upkeep clamps to max each tick
		summonOnce(t, s, player)
		for s.W.ActorByID(player).Action.Kind == core.ActionSkill {
			s.Step(nil) // let recovery finish before the next cast
		}
		for _, m := range minionsOf(s, player) {
			seen := false
			for _, id := range ids {
				if id == m.ID {
					seen = true
				}
			}
			if !seen {
				ids = append(ids, m.ID)
			}
		}
	}
	minions := minionsOf(s, player)
	if len(minions) != 3 {
		t.Fatalf("%d minions after four casts, want the cap of 3", len(minions))
	}
	for _, m := range minions {
		if m.ID == ids[0] {
			t.Fatal("the oldest skeleton survived past the cap")
		}
	}
	// A despawn is not a death: no death events for the culled skeleton.
	for _, ev := range s.W.LastEvents {
		if ev.Kind == core.EvDeath && ev.Actor == ids[0] {
			t.Fatal("cap despawn emitted a death event")
		}
	}
}

// TestMinionKillCreditsOwner: the skeleton does the killing; the player
// gets the XP and the flask charges.
func TestMinionKillCreditsOwner(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "summon_skeleton")
	p := s.W.ActorByID(player)
	p.Mana = fm.FromInt(200)
	p.FlaskCharges[0] = 0
	summonOnce(t, s, player)

	// A dummy in claw range of the skeleton; the minion AI does the rest.
	dummy := mustSpawn(t, s, "training_dummy", 2500, 0)
	xpBefore := p.XP
	for i := 0; i < 300; i++ {
		s.Step(nil)
		if d := s.W.ActorByID(dummy); d == nil {
			break
		}
	}
	if s.W.ActorByID(dummy) != nil {
		t.Fatal("the skeleton never killed the dummy")
	}
	if p.XP <= xpBefore {
		t.Fatal("minion kill paid the player no XP")
	}
	if p.FlaskCharges[0] == 0 {
		t.Fatal("minion kill fed the player no flask charges")
	}
}

// TestMinionHeels: no enemies around, the skeleton follows its wandering
// owner instead of standing where it was born.
func TestMinionHeels(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "summon_skeleton")
	s.W.ActorByID(player).Mana = fm.FromInt(200)
	summonOnce(t, s, player)

	// Walk far east; the skeleton should trail inside the follow gap.
	dest := space.V(fm.FromInt(20), 0)
	for i := 0; i < 240; i++ {
		s.Step([]core.Command{{Actor: player, Kind: core.CmdMove, Point: dest}})
	}
	p := s.W.ActorByID(player)
	m := minionsOf(s, player)[0]
	if d := space.Dist(p.Pos, m.Pos); d > fm.FromInt(6) {
		t.Fatalf("skeleton is %v from its owner after the walk, want it heeling", d)
	}
}

// TestMinionSaveRoundTrip: the owner link survives a save/load and the
// restored world stays bit-identical.
func TestMinionSaveRoundTrip(t *testing.T) {
	s := sim.New(content.DB(), 9)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "summon_skeleton")
	s.W.ActorByID(player).Mana = fm.FromInt(200)
	summonOnce(t, s, player)
	for s.W.ActorByID(player).Action.Kind == core.ActionSkill {
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
	if got := minionsOf(restored, player); len(got) != 1 || got[0].Owner != player {
		t.Fatalf("restored minions = %v, want one owned by the player", got)
	}
	for i := 0; i < 60; i++ {
		s.Step(nil)
		restored.Step(nil)
		if s.W.Hash() != restored.W.Hash() {
			t.Fatalf("hash diverged %d ticks after restore", i+1)
		}
	}
}

// TestBonelordRaisesTheCap: the ExtraMinions sheet stat (unique-only, read
// at the cap site) lets a fourth skeleton stand.
func TestBonelordRaisesTheCap(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "summon_skeleton")
	p := s.W.ActorByID(player)
	p.Sheet.Add(stats.Modifier{
		Stat: stats.ExtraMinions, Layer: stats.LayerFlat,
		Value: fm.One, Source: 909090,
	})
	for i := 0; i < 5; i++ {
		p.Mana = fm.FromInt(50)
		summonOnce(t, s, player)
		for p.Action.Kind == core.ActionSkill {
			s.Step(nil)
		}
	}
	if got := len(minionsOf(s, player)); got != 4 {
		t.Fatalf("%d minions with Bonelord's cap, want 4", got)
	}
}
