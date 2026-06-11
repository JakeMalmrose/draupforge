package sim_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// TestAdrenalineEndToEnd: the whole player-visible loop — use_skill through
// the command gate, windup, buff lands (event, sheet effect, wire bit),
// then expires clean.
func TestAdrenalineEndToEnd(t *testing.T) {
	s := sim.New(content.DB(), 21)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	baseSpeed := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{})

	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "adrenaline"}})
	sawEvent := false
	for i := 0; i < 30 && !sawEvent; i++ {
		s.Step(nil)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvBuff && ev.Other == player && ev.Note == "adrenaline" {
				sawEvent = true
			}
		}
	}
	if !sawEvent {
		t.Fatal("no buff event within a second of casting")
	}

	want := fm.Mul(baseSpeed, fm.FromMilli(1300))
	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != want {
		t.Errorf("buffed move speed = %d, want %d (+30%% increased)", got, want)
	}
	snap := s.BuildSnapshot()
	for _, as := range snap.Actors {
		if as.ID == uint64(player) && as.Ail&protocol.AilBuffed == 0 {
			t.Error("buffed player's wire snapshot lacks the AilBuffed bit")
		}
	}

	for i := 0; i < 4*core.TicksPerSecond; i++ {
		s.Step(nil)
	}
	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != baseSpeed {
		t.Errorf("post-expiry move speed = %d, want base %d", got, baseSpeed)
	}
	if len(a.Statuses) != 0 {
		t.Error("buff status survived its duration")
	}
}

// TestSaveRestoreActiveBuff: a save taken mid-buff restores the status, its
// sheet modifiers, and its remaining timer — the future stays bit-identical
// through the expiry edge.
func TestSaveRestoreActiveBuff(t *testing.T) {
	s := sim.New(content.DB(), 22)
	player := mustSpawn(t, s, "player", 0, 0)
	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "adrenaline"}})
	for i := 0; i < 30; i++ {
		s.Step(nil)
	}
	if len(s.W.ActorByID(player).Statuses) == 0 {
		t.Fatal("test setup: no buff active at save time")
	}

	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	r, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.W.Hash(), s.W.Hash(); got != want {
		t.Fatalf("restored hash %016x, want %016x", got, want)
	}
	for i := 0; i < 5*core.TicksPerSecond; i++ {
		s.Step(nil)
		r.Step(nil)
		if got, want := r.W.Hash(), s.W.Hash(); got != want {
			t.Fatalf("buffed restore diverged %d ticks in: %016x != %016x", i+1, got, want)
		}
	}
}
