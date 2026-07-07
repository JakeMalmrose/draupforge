package sim_test

// Channelling and cooldowns: the action model's deliberate growth. A
// channelled skill repeats its effect while mana feeds it and yields to its
// owner's next order; a cooldown-gated blink teleports honestly around
// walls and refuses re-use until it clears.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// beginChannel casts incinerate and steps through the windup into the
// channel loop.
func beginChannel(t *testing.T, s *sim.Sim, player core.EntityID, aim space.Vec2) *core.Actor {
	t.Helper()
	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "incinerate", Point: aim}})
	a := s.W.ActorByID(player)
	for i := 0; a.Action.Kind == core.ActionSkill && a.Action.Phase == core.PhaseWindup; i++ {
		if i > 60 {
			t.Fatal("windup never ended")
		}
		s.Step(nil)
	}
	if a.Action.Kind != core.ActionSkill || a.Action.Phase != core.PhaseChannel {
		t.Fatalf("not channelling: kind %v phase %v", a.Action.Kind, a.Action.Phase)
	}
	return a
}

func TestChannelRepeatsAndDrainsMana(t *testing.T) {
	s := sim.New(content.DB(), 61)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "incinerate")
	a := beginChannel(t, s, player, space.V(fm.FromInt(4), 0))

	manaAfterFirst := a.Mana
	projBefore := len(s.W.Projectiles)
	// Ride the channel through several repeats: mana drains 2 per gout,
	// projectiles keep coming, the action never ends.
	for i := 0; i < 3*6; i++ {
		s.Step(nil)
	}
	if a.Action.Kind != core.ActionSkill || a.Action.Phase != core.PhaseChannel {
		t.Fatal("channel ended by itself with mana to spare")
	}
	if a.Mana >= manaAfterFirst {
		t.Errorf("channel repeats drained no mana: %d -> %d", manaAfterFirst, a.Mana)
	}
	if len(s.W.Projectiles) <= projBefore-1 {
		t.Error("channel repeats spawned no projectiles")
	}
}

func TestChannelStarvesWithoutMana(t *testing.T) {
	s := sim.New(content.DB(), 62)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "incinerate")
	a := beginChannel(t, s, player, space.V(fm.FromInt(4), 0))

	a.Mana = fm.FromInt(1) // less than one repeat's cost
	for i := 0; i < 12; i++ {
		s.Step(nil)
	}
	if a.Action.Kind == core.ActionSkill {
		t.Fatal("starved channel kept running")
	}
}

func TestChannelBreaksOnMove(t *testing.T) {
	s := sim.New(content.DB(), 63)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "incinerate")
	beginChannel(t, s, player, space.V(fm.FromInt(4), 0))

	s.Step([]core.Command{{Actor: player, Kind: core.CmdMove, Point: space.V(fm.FromInt(3), fm.FromInt(3))}})
	a := s.W.ActorByID(player)
	if a.Action.Kind != core.ActionMove {
		t.Fatalf("move did not break the channel: kind %v", a.Action.Kind)
	}
}

func TestChannelBreaksOnStop(t *testing.T) {
	s := sim.New(content.DB(), 64)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "incinerate")
	beginChannel(t, s, player, space.V(fm.FromInt(4), 0))

	s.Step([]core.Command{{Actor: player, Kind: core.CmdStop}})
	if a := s.W.ActorByID(player); a.Action.Kind != core.ActionIdle {
		t.Fatalf("stop did not break the channel: kind %v", a.Action.Kind)
	}
}

func TestBlinkTeleportsAndCoolsDown(t *testing.T) {
	s := sim.New(content.DB(), 65)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "blink")
	a := s.W.ActorByID(player)
	start := a.Pos

	castOnce(t, s, player, "blink", space.V(fm.FromInt(20), 0))
	settle(t, s, player)
	// Open plane: clamped to the 7u range exactly.
	if got := space.Dist(start, a.Pos); got != fm.FromInt(7) {
		t.Errorf("blink distance = %d, want 7000 (range clamp)", got)
	}
	if !a.OnCooldown("blink") {
		t.Fatal("blink started no cooldown")
	}

	// Re-cast while cooling: refused (no action, no mana spent).
	mana := a.Mana
	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "blink", Point: space.V(0, 0)}})
	if a.Action.Kind == core.ActionSkill {
		t.Fatal("cooldown did not gate the recast")
	}
	if a.Mana < mana {
		t.Error("refused recast still took mana")
	}

	// Cooldown burns down in Upkeep and clears.
	for i := 0; i < 91; i++ {
		s.Step(nil)
	}
	if a.OnCooldown("blink") {
		t.Fatalf("cooldown never cleared: %d ticks left", a.CooldownLeft("blink"))
	}
	pos := a.Pos
	castOnce(t, s, player, "blink", space.V(fm.FromInt(20), 0))
	settle(t, s, player)
	if a.Pos == pos {
		t.Error("cleared cooldown still refused the blink")
	}
}

func TestCooldownSurvivesSaveLoad(t *testing.T) {
	s := sim.New(content.DB(), 66)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "blink")
	castOnce(t, s, player, "blink", space.V(fm.FromInt(5), 0))
	settle(t, s, player)

	blob, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	w2, err := core.LoadWorld(content.DB(), blob)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w2.Hash(), s.W.Hash(); got != want {
		t.Fatalf("save/load hash mismatch: %x vs %x", got, want)
	}
	b := w2.ActorByID(player)
	if got, want := b.CooldownLeft("blink"), s.W.ActorByID(player).CooldownLeft("blink"); got != want || got == 0 {
		t.Errorf("restored cooldown = %d, want %d (nonzero)", got, want)
	}
}

func TestCooldownsDoNotTransfer(t *testing.T) {
	s := sim.New(content.DB(), 67)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "blink")
	castOnce(t, s, player, "blink", space.V(fm.FromInt(5), 0))
	settle(t, s, player)
	a := s.W.ActorByID(player)
	if !a.OnCooldown("blink") {
		t.Fatal("no cooldown to transfer")
	}

	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 68)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if b.OnCooldown("blink") {
		t.Error("cooldown transferred across zones; it is zone-local state")
	}
}
