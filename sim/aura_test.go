package sim_test

// Auras: toggled reservation gems. While on, the AuraMods package sits on
// the caster's sheet and every owned minion's (no radius), max mana is
// reserved, and the toggle state is durable — it survives saves and
// character transfers, and the gem verbs (replace, level) keep the applied
// mods honest.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// fireFlat reads the flat-fire portion of the actor's damage layers — the
// observable Anger grants.
func fireFlat(a *core.Actor) fm.Fixed {
	return a.Sheet.Layers(stats.Damage, stats.T(stats.TagFire)).Flat
}

// settle steps until the actor is idle again — casts gate on the
// one-action-at-a-time rule, so back-to-back casts must ride out recovery.
func settle(t *testing.T, s *sim.Sim, id core.EntityID) {
	t.Helper()
	a := s.W.ActorByID(id)
	for i := 0; a.Action.Kind != core.ActionIdle; i++ {
		if i > 300 {
			t.Fatal("actor never settled to idle")
		}
		s.Step(nil)
	}
}

func TestAuraToggleReservesAndBuffs(t *testing.T) {
	s := sim.New(content.DB(), 41)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger")
	a := s.W.ActorByID(player)
	baseMax := a.MaxMana()

	castOnce(t, s, player, "anger", space.Vec2{})
	if g := a.GemForSkill("anger"); !g.AuraOn {
		t.Fatal("cast did not toggle the aura on")
	}
	if got, want := fireFlat(a), fm.FromInt(5); got != want {
		t.Errorf("aura flat fire = %d, want %d", got, want)
	}
	// 35% reserved: max drops to 65%, current mana clamps under it.
	if got, want := a.MaxMana(), fm.Mul(baseMax, fm.FromMilli(650)); got != want {
		t.Errorf("reserved max mana = %d, want %d", got, want)
	}
	if a.Mana > a.MaxMana() {
		t.Errorf("current mana %d above reserved max %d", a.Mana, a.MaxMana())
	}

	// Second cast toggles off: mods gone, max restored.
	settle(t, s, player)
	castOnce(t, s, player, "anger", space.Vec2{})
	if g := a.GemForSkill("anger"); g.AuraOn {
		t.Fatal("second cast did not toggle the aura off")
	}
	if got := fireFlat(a); got != 0 {
		t.Errorf("flat fire after toggle off = %d, want 0", got)
	}
	if got := a.MaxMana(); got != baseMax {
		t.Errorf("max mana after toggle off = %d, want %d", got, baseMax)
	}
}

func TestAuraCoversMinionsAndLateSummons(t *testing.T) {
	s := sim.New(content.DB(), 42)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger", "summon_skeleton")
	a := s.W.ActorByID(player)

	minions := func() []*core.Actor {
		var ms []*core.Actor
		for _, m := range s.W.Actors {
			if !m.Dead && m.Owner == a.ID {
				ms = append(ms, m)
			}
		}
		return ms
	}

	castOnce(t, s, player, "summon_skeleton", space.Vec2{})
	settle(t, s, player)
	first := minions()
	if len(first) == 0 {
		t.Fatal("no minion after summon")
	}

	castOnce(t, s, player, "anger", space.Vec2{})
	settle(t, s, player)
	for _, m := range minions() {
		if got, want := fireFlat(m), fm.FromInt(5); got != want {
			t.Errorf("existing minion flat fire = %d, want %d — toggle must cover standing minions", got, want)
		}
	}

	// A minion summoned while the aura runs inherits it at materialization.
	castOnce(t, s, player, "summon_skeleton", space.Vec2{})
	settle(t, s, player)
	after := minions()
	if len(after) <= len(first) {
		t.Fatal("second summon added no minion")
	}
	for _, m := range after {
		if got, want := fireFlat(m), fm.FromInt(5); got != want {
			t.Errorf("late minion flat fire = %d, want %d — DrainSpawns must apply owner auras", got, want)
		}
	}

	settle(t, s, player)
	castOnce(t, s, player, "anger", space.Vec2{})
	settle(t, s, player)
	for _, m := range minions() {
		if got := fireFlat(m); got != 0 {
			t.Errorf("minion flat fire after toggle off = %d, want 0", got)
		}
	}
}

func TestAuraScalesWithGemLevel(t *testing.T) {
	s := sim.New(content.DB(), 43)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger")
	a := s.W.ActorByID(player)
	a.GemForSkill("anger").Level = 11 // +5% per level past 1 → ×1.5

	castOnce(t, s, player, "anger", space.Vec2{})
	if got, want := fireFlat(a), fm.Mul(fm.FromInt(5), fm.FromMilli(1500)); got != want {
		t.Errorf("level-11 aura flat fire = %d, want %d", got, want)
	}
}

func TestLevelGemRescalesRunningAura(t *testing.T) {
	s := sim.New(content.DB(), 44)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger")
	a := s.W.ActorByID(player)

	castOnce(t, s, player, "anger", space.Vec2{})
	settle(t, s, player)
	up := giveUncut(s, a, false, 5, "anger", "fireball", "spark")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdLevelGem, TargetID: up, GemIndex: 0}})

	if g := a.GemForSkill("anger"); g.Level != 5 || !g.AuraOn {
		t.Fatalf("level-up broke the gem: level %d, on %v", a.GemForSkill("anger").Level, a.GemForSkill("anger").AuraOn)
	}
	if got, want := fireFlat(a), fm.Mul(fm.FromInt(5), fm.FromMilli(1200)); got != want {
		t.Errorf("re-leveled aura flat fire = %d, want %d", got, want)
	}
}

func TestCutReplaceStripsRunningAura(t *testing.T) {
	s := sim.New(content.DB(), 45)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger")
	a := s.W.ActorByID(player)
	baseMax := a.MaxMana()

	castOnce(t, s, player, "anger", space.Vec2{})
	settle(t, s, player)
	cut := giveUncut(s, a, false, 1, "spark", "fireball", "sweep")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSkill, TargetID: cut, Choice: 0, Replace: true, GemIndex: 0}})

	if a.GemForSkill("anger") != nil {
		t.Fatal("replace did not remove the aura gem")
	}
	if got := fireFlat(a); got != 0 {
		t.Errorf("flat fire after replacing the aura gem = %d, want 0 — mods leaked", got)
	}
	if got := a.MaxMana(); got != baseMax {
		t.Errorf("max mana after replacing the aura gem = %d, want %d — reservation leaked", got, baseMax)
	}
}

func TestAuraSurvivesTransfer(t *testing.T) {
	s := sim.New(content.DB(), 46)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger")
	a := s.W.ActorByID(player)
	castOnce(t, s, player, "anger", space.Vec2{})

	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 47)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if g := b.GemForSkill("anger"); g == nil || !g.AuraOn {
		t.Fatal("aura state did not transfer")
	}
	if got, want := fireFlat(b), fm.FromInt(5); got != want {
		t.Errorf("transferred aura flat fire = %d, want %d", got, want)
	}
	if b.Mana > b.MaxMana() {
		t.Errorf("arrived with mana %d above reserved max %d", b.Mana, b.MaxMana())
	}
}

func TestAuraSurvivesSaveLoad(t *testing.T) {
	s := sim.New(content.DB(), 48)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "anger")
	castOnce(t, s, player, "anger", space.Vec2{})
	s.Step(nil) // settle to a clean tick boundary

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
	if g := b.GemForSkill("anger"); g == nil || !g.AuraOn {
		t.Fatal("aura state did not survive save/load")
	}
	if got, want := fireFlat(b), fm.FromInt(5); got != want {
		t.Errorf("restored aura flat fire = %d, want %d (mods ride the saved modifier list)", got, want)
	}
}

// TestAuraTogglesConsumeNoRNG pins the streams: an aura toggle is pure state,
// so a world where the player toggles twice ends with the same combat-stream
// position as one where they idle.
func TestAuraTogglesConsumeNoRNG(t *testing.T) {
	run := func(toggle bool) [4]uint64 {
		s := sim.New(content.DB(), 49)
		player := mustSpawn(t, s, "player", 0, 0)
		grantGems(t, s, player, "anger")
		if toggle {
			castOnce(t, s, player, "anger", space.Vec2{})
			castOnce(t, s, player, "anger", space.Vec2{})
		}
		for i := 0; i < 30; i++ {
			s.Step(nil)
		}
		return s.W.RNGCombat.State()
	}
	if run(true) != run(false) {
		t.Error("aura toggles consumed combat RNG")
	}
}
