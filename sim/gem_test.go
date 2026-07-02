package sim_test

// The gem system's contract: cutting drafts, leveling, sockets, and each
// support behavior (more/less multipliers, extra projectiles, chain,
// conversion, speed, mana) — plus durability across saves and transfers.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// giveUncut hands an actor a hand-built uncut gem item and returns its ID.
func giveUncut(s *sim.Sim, a *core.Actor, support bool, level int, choices ...string) core.EntityID {
	item := core.Item{ID: s.W.AllocID(), Gem: &core.UncutGem{
		Support: support, Level: level, Choices: choices,
	}}
	a.Inventory = append(a.Inventory, item)
	return item.ID
}

// castOnce fires one skill and steps until the effect point has passed.
func castOnce(t *testing.T, s *sim.Sim, id core.EntityID, skill string, aim space.Vec2) {
	t.Helper()
	s.Step([]core.Command{{Actor: id, Kind: core.CmdUseSkill, Skill: skill, Point: aim}})
	a := s.W.ActorByID(id)
	if a.Action.Kind != core.ActionSkill {
		t.Fatalf("cast of %s was rejected", skill)
	}
	for a.Action.Kind == core.ActionSkill && a.Action.Phase == core.PhaseWindup {
		s.Step(nil)
	}
}

// hitAmount runs one fireball against a full-life dummy and returns the hit
// event amount. mutate pokes the player's gem state before casting.
func hitAmount(t *testing.T, seed uint64, mutate func(*core.Actor, *core.ContentDB)) fm.Fixed {
	t.Helper()
	s := sim.New(content.DB(), seed)
	player := mustSpawn(t, s, "player", 0, 0)
	dummy := mustSpawn(t, s, "training_dummy", 8000, 0)
	a := s.W.ActorByID(player)
	if mutate != nil {
		mutate(a, s.W.Content)
	}
	castOnce(t, s, player, "fireball", s.W.ActorByID(dummy).Pos)
	for i := 0; i < 60; i++ {
		s.Step(nil)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvHit && ev.Actor == player {
				return ev.Amount
			}
		}
	}
	t.Fatal("fireball never landed")
	return 0
}

// TestGemLevelScalesDamage: a level-20 gem deals exactly ×2.9 the level-1
// roll (linear +10%/level on the skill's own base, same RNG consumption).
func TestGemLevelScalesDamage(t *testing.T) {
	base := hitAmount(t, 77, nil)
	scaled := hitAmount(t, 77, func(a *core.Actor, _ *core.ContentDB) {
		a.Gems[0].Level = 20
	})
	if want := fm.Mul(base, fm.FromMilli(2900)); scaled != want {
		t.Errorf("level-20 hit = %d, want %d (×2.9 of level-1 %d)", scaled, want, base)
	}
}

// TestBruteForceMoreDamage: the 25% more multiplier folds in exactly, and
// only for the supported skill's cast.
func TestBruteForceMoreDamage(t *testing.T) {
	base := hitAmount(t, 78, nil)
	boosted := hitAmount(t, 78, func(a *core.Actor, db *core.ContentDB) {
		a.Gems[0].Supports[0] = db.Support("brute_force")
	})
	if want := fm.Mul(base, fm.FromMilli(1250)); boosted != want {
		t.Errorf("supported hit = %d, want %d (×1.25 of %d)", boosted, want, base)
	}
}

// TestSupportManaAndSpeed: inspiration cuts the cost, faster casting cuts
// the windup — both applied at use time.
func TestSupportManaAndSpeed(t *testing.T) {
	cast := func(support string) (manaAfter fm.Fixed, ticksLeft uint32) {
		s := sim.New(content.DB(), 79)
		player := mustSpawn(t, s, "player", 0, 0)
		a := s.W.ActorByID(player)
		if support != "" {
			a.Gems[0].Supports[0] = s.W.Content.Support(support)
		}
		s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "fireball", Point: space.V(fm.FromInt(8), 0)}})
		if a.Action.Kind != core.ActionSkill {
			t.Fatal("cast rejected")
		}
		return a.Mana, a.Action.TicksLeft
	}

	baseMana, baseTicks := cast("")
	if want := fm.FromInt(40); baseMana != want {
		t.Errorf("plain fireball left %d mana, want %d", baseMana, want)
	}
	inspMana, _ := cast("inspiration")
	if want := fm.FromInt(43); inspMana != want { // 10 × 0.7 = 7 spent
		t.Errorf("inspired fireball left %d mana, want %d", inspMana, want)
	}
	_, fastTicks := cast("faster_casting")
	if fastTicks >= baseTicks {
		t.Errorf("faster casting windup %d ≥ base %d", fastTicks, baseTicks)
	}
}

// TestMultipleProjectilesFan: LMP fires three projectiles; the fan spreads
// but all fly (distinct velocities, same speed).
func TestMultipleProjectilesFan(t *testing.T) {
	s := sim.New(content.DB(), 80)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	a.Gems[0].Supports[0] = s.W.Content.Support("lesser_projectiles")

	castOnce(t, s, player, "fireball", space.V(fm.FromInt(20), 0))
	if n := len(s.W.Projectiles); n != 3 {
		t.Fatalf("LMP fireball spawned %d projectiles, want 3", n)
	}
	vels := map[space.Vec2]bool{}
	for _, p := range s.W.Projectiles {
		vels[p.Vel] = true
		if p.Gem.Level != 1 || len(p.Gem.Supports) != 1 {
			t.Error("projectile lost its gem context")
		}
	}
	if len(vels) != 3 {
		t.Errorf("fan has %d distinct velocities, want 3", len(vels))
	}
}

// TestChainBounces: a chained spark strikes a second target after the
// first — one projectile, two hits.
func TestChainBounces(t *testing.T) {
	s := sim.New(content.DB(), 81)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "spark")
	a := s.W.ActorByID(player)
	g := a.GemForSkill("spark")
	g.Supports[0] = s.W.Content.Support("chain")

	first := mustSpawn(t, s, "training_dummy", 8000, 0)
	second := mustSpawn(t, s, "training_dummy", 8000, 3000)

	castOnce(t, s, player, "spark", s.W.ActorByID(first).Pos)
	hits := map[core.EntityID]bool{}
	for i := 0; i < 120; i++ {
		s.Step(nil)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvHit && ev.Actor == player {
				hits[ev.Other] = true
			}
		}
	}
	if !hits[first] || !hits[second] {
		t.Errorf("chain hits: first=%v second=%v, want both", hits[first], hits[second])
	}
}

// TestGlaciateConverts: converted cold damage chills — a plain fireball
// never does.
func TestGlaciateConverts(t *testing.T) {
	chilled := func(support bool) bool {
		s := sim.New(content.DB(), 82)
		player := mustSpawn(t, s, "player", 0, 0)
		dummy := mustSpawn(t, s, "training_dummy", 8000, 0)
		a := s.W.ActorByID(player)
		if support {
			a.Gems[0].Supports[0] = s.W.Content.Support("glaciate")
		}
		castOnce(t, s, player, "fireball", s.W.ActorByID(dummy).Pos)
		for i := 0; i < 60; i++ {
			s.Step(nil)
			for _, st := range s.W.ActorByID(dummy).Statuses {
				if st.Kind == core.StatusChill {
					return true
				}
			}
		}
		return false
	}
	if chilled(false) {
		t.Error("plain fireball chilled — conversion leaking without the support")
	}
	if !chilled(true) {
		t.Error("glaciated fireball never chilled — no cold arrived")
	}
}

// TestCutSkillCommand: the full uncut-to-cut loop through the command gate,
// including the duplicate, cap, and replace rules.
func TestCutSkillCommand(t *testing.T) {
	s := sim.New(content.DB(), 83)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)

	// Choice 0 duplicates the starter fireball — rejected outright.
	dup := giveUncut(s, a, false, 5, "fireball", "spark", "frost_nova")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSkill, TargetID: dup, Choice: 0}})
	if len(a.Gems) != 1 {
		t.Fatal("cutting a duplicate skill went through")
	}
	// Choice 1 (spark) cuts at the drop's level.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSkill, TargetID: dup, Choice: 1}})
	if len(a.Gems) != 2 || a.Gems[1].Skill.ID != "spark" || a.Gems[1].Level != 5 {
		t.Fatalf("cut failed: %+v", a.Gems)
	}
	if len(a.Inventory) != 0 {
		t.Fatal("uncut gem survived its cutting")
	}

	// Fill to the cap, then require replace.
	grantGems(t, s, player, "frost_nova", "adrenaline")
	full := giveUncut(s, a, false, 2, "arc_bolt", "bone_arrow", "spark")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSkill, TargetID: full, Choice: 0}})
	if len(a.Gems) != core.MaxSkillGems || a.GemForSkill("arc_bolt") != nil {
		t.Fatal("cut past the skill-gem cap without replace")
	}
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSkill, TargetID: full, Choice: 0, Replace: true, GemIndex: 3}})
	if a.GemForSkill("arc_bolt") == nil || a.GemForSkill("adrenaline") != nil {
		t.Fatalf("replace-cut failed: %+v", a.Gems)
	}
}

// TestLevelGemCommand: leveling raises to the drop level, never lowers.
func TestLevelGemCommand(t *testing.T) {
	s := sim.New(content.DB(), 84)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)

	up := giveUncut(s, a, false, 9, "spark", "frost_nova", "arc_bolt")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdLevelGem, TargetID: up, GemIndex: 0}})
	if a.Gems[0].Level != 9 || len(a.Inventory) != 0 {
		t.Fatalf("level-up failed: level %d, bag %d", a.Gems[0].Level, len(a.Inventory))
	}
	down := giveUncut(s, a, false, 4, "spark", "frost_nova", "arc_bolt")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdLevelGem, TargetID: down, GemIndex: 0}})
	if a.Gems[0].Level != 9 {
		t.Fatal("a lower-level uncut gem downgraded the gem")
	}
	if len(a.Inventory) != 1 {
		t.Fatal("rejected level-up consumed the item")
	}
}

// TestCutSupportCommand: socketing is tag-gated and duplicate-checked;
// socketing over an occupied socket swaps the support out.
func TestCutSupportCommand(t *testing.T) {
	s := sim.New(content.DB(), 85)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	grantGems(t, s, player, "frost_nova") // gem 1: no projectile tag

	chain := giveUncut(s, a, true, 0, "chain", "brute_force", "faster_casting")
	// Chain into frost nova: requires projectile — rejected.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSupport, TargetID: chain, Choice: 0, GemIndex: 1, Socket: 0}})
	if a.Gems[1].Supports[0] != nil {
		t.Fatal("chain socketed into a non-projectile skill")
	}
	// Chain into fireball: fine.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSupport, TargetID: chain, Choice: 0, GemIndex: 0, Socket: 0}})
	if sup := a.Gems[0].Supports[0]; sup == nil || sup.ID != "chain" {
		t.Fatalf("chain didn't socket: %+v", a.Gems[0].Supports)
	}
	// Socketing over it swaps in the new support (old one is destroyed).
	swap := giveUncut(s, a, true, 0, "brute_force", "chain", "glaciate")
	s.Step([]core.Command{{Actor: player, Kind: core.CmdCutSupport, TargetID: swap, Choice: 0, GemIndex: 0, Socket: 0}})
	if sup := a.Gems[0].Supports[0]; sup == nil || sup.ID != "brute_force" {
		t.Fatalf("socket swap failed: %+v", a.Gems[0].Supports)
	}
}

// TestAddSocketCommand: jeweller orbs grow sockets to the cap and no
// further; without orbs nothing happens.
func TestAddSocketCommand(t *testing.T) {
	s := sim.New(content.DB(), 86)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)

	s.Step([]core.Command{{Actor: player, Kind: core.CmdAddSocket, GemIndex: 0}})
	if a.Gems[0].Sockets != core.GemStartSockets {
		t.Fatal("socket added without a jeweller orb")
	}
	a.Orbs[core.OrbJeweller] = 5
	for i := 0; i < 5; i++ {
		s.Step([]core.Command{{Actor: player, Kind: core.CmdAddSocket, GemIndex: 0}})
	}
	if a.Gems[0].Sockets != core.MaxGemSockets {
		t.Errorf("sockets = %d, want capped at %d", a.Gems[0].Sockets, core.MaxGemSockets)
	}
	if want := int32(5 - (core.MaxGemSockets - core.GemStartSockets)); a.Orbs[core.OrbJeweller] != want {
		t.Errorf("jewellers left = %d, want %d (cap must not eat orbs)", a.Orbs[core.OrbJeweller], want)
	}
	if len(a.Gems[0].Supports) != core.MaxGemSockets {
		t.Errorf("supports slice length %d out of sync with sockets", len(a.Gems[0].Supports))
	}
}

// TestUncutDraftDistinct: every rolled draft offers three distinct, valid
// choices.
func TestUncutDraftDistinct(t *testing.T) {
	s := sim.New(content.DB(), 87)
	for i := 0; i < 50; i++ {
		for _, support := range []bool{false, true} {
			item := items.RollUncutGem(s.W, support, 7)
			seen := map[string]bool{}
			for _, c := range item.Gem.Choices {
				if seen[c] {
					t.Fatalf("draft repeats %q: %v", c, item.Gem.Choices)
				}
				seen[c] = true
				if support {
					if s.W.Content.Support(c) == nil {
						t.Fatalf("draft offers unknown support %q", c)
					}
				} else if sk := s.W.Content.Skills[c]; sk == nil || !sk.Cuttable {
					t.Fatalf("draft offers uncuttable skill %q", c)
				}
			}
			if len(seen) != core.GemDraftSize {
				t.Fatalf("draft size %d, want %d", len(seen), core.GemDraftSize)
			}
		}
	}
}

// TestGemSaveRoundTrip: gems, uncut bag items, and an in-flight supported
// projectile all survive save/load bit-exactly — including the future.
func TestGemSaveRoundTrip(t *testing.T) {
	s := sim.New(content.DB(), 88)
	player := mustSpawn(t, s, "player", 0, 0)
	mustSpawn(t, s, "training_dummy", 12000, 0)
	a := s.W.ActorByID(player)
	a.Gems[0].Level = 6
	a.Orbs[core.OrbJeweller] = 2
	s.Step([]core.Command{{Actor: player, Kind: core.CmdAddSocket, GemIndex: 0}})
	a.Gems[0].Supports[1] = s.W.Content.Support("chain")
	giveUncut(s, a, false, 11, "spark", "arc_bolt", "bone_arrow")
	giveUncut(s, a, true, 0, "glaciate", "chain", "inspiration")

	castOnce(t, s, player, "fireball", space.V(fm.FromInt(12), 0))
	if len(s.W.Projectiles) == 0 {
		t.Fatal("no projectile in flight at save time")
	}

	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	w2, err := core.LoadWorld(s.W.Content, data)
	if err != nil {
		t.Fatal(err)
	}
	if s.W.Hash() != w2.Hash() {
		t.Fatal("hash changed across save/load of a gem world")
	}
	r := &sim.Sim{W: w2}
	for i := 0; i < 60; i++ {
		s.Step(nil)
		r.Step(nil)
		if s.W.Hash() != r.W.Hash() {
			t.Fatalf("restored world diverged %d ticks after load", i+1)
		}
	}
}

// TestCharacterCarriesGems: extract/inject moves cut gems (level, sockets,
// supports) and uncut bag items; a gem-less legacy character is re-granted
// its def's starters.
func TestCharacterCarriesGems(t *testing.T) {
	s := sim.New(content.DB(), 89)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	grantGems(t, s, player, "spark")
	a.Gems[1].Level = 8
	a.Gems[1].Supports[0] = s.W.Content.Support("brute_force")
	giveUncut(s, a, true, 0, "chain", "glaciate", "faster_casting")

	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 90)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Gems) != 2 || b.Gems[1].Skill.ID != "spark" || b.Gems[1].Level != 8 {
		t.Fatalf("gems didn't transfer: %+v", b.Gems)
	}
	if sup := b.Gems[1].Supports[0]; sup == nil || sup.ID != "brute_force" {
		t.Fatal("socketed support didn't transfer")
	}
	if len(b.Inventory) != 1 || b.Inventory[0].Gem == nil || !b.Inventory[0].Gem.Support {
		t.Fatal("uncut gem item didn't transfer")
	}

	// Legacy character: no gems recorded → starters granted at injection.
	legacy := core.Character{Def: "player", Level: 3}
	s3 := sim.New(content.DB(), 91)
	c, err := core.InjectCharacter(s3.W, legacy, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Gems) != 1 || c.Gems[0].Skill.ID != "fireball" {
		t.Fatalf("legacy character got gems %+v, want starter fireball", c.Gems)
	}
}
