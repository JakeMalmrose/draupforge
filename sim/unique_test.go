package sim_test

// Uniques — the chase items. Contracts: the drop path's RNG consumption is
// pinned (zero permille consumes nothing new), a dropped unique carries its
// def with no affixes, equipping one changes what the skill system DOES
// (extra projectiles, extra chains — stats nothing else rolls), orbs refuse
// them, and they survive saves and character transfers.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// rollUnique draws items from a max-permille table until a unique appears.
func rollUnique(t *testing.T, s *sim.Sim, uniqueID string) core.Item {
	t.Helper()
	table := &core.LootTableDef{
		ID: "test_uniques", DropChance: fm.One,
		Bases:          []string{"rusty_sword"},
		RarityWeights:  [3]uint32{1, 0, 0},
		UniquePermille: 1000,
	}
	for i := 0; i < 64; i++ {
		item := items.RollItem(s.W, table, 20)
		if item.Unique != nil && item.Unique.ID == uniqueID {
			return item
		}
	}
	t.Fatalf("64 guaranteed-unique rolls never produced %s", uniqueID)
	return core.Item{}
}

// equipUnique mints the unique into the actor's bag and equips it.
func equipUnique(t *testing.T, s *sim.Sim, actor core.EntityID, uniqueID string) {
	t.Helper()
	a := s.W.ActorByID(actor)
	item := rollUnique(t, s, uniqueID)
	a.Inventory = append(a.Inventory, item)
	if !items.Equip(s.W, a, item.ID, core.EquipAuto) {
		t.Fatalf("equipping %s failed", uniqueID)
	}
}

func TestUniqueDropShape(t *testing.T) {
	s := sim.New(content.DB(), 3)
	item := rollUnique(t, s, "stormweaver_band")
	if item.Rarity != core.RarityUnique {
		t.Errorf("rarity = %v, want unique", item.Rarity)
	}
	if item.Base.ID != "iron_ring" {
		t.Errorf("base = %s, want the unique's fixed iron_ring", item.Base.ID)
	}
	if len(item.Affixes) != 0 {
		t.Errorf("unique rolled %d affixes, want none", len(item.Affixes))
	}
	if item.Implicit == 0 {
		t.Error("unique skipped its base implicit roll")
	}
}

// TestUniqueRNGConsumption pins the conditional draw: a zero-permille table
// consumes exactly the pre-unique stream, so existing tables that never set
// it keep their replays.
func TestUniqueRNGConsumption(t *testing.T) {
	table := func(pm uint32) *core.LootTableDef {
		return &core.LootTableDef{
			ID: "t", DropChance: fm.One, Bases: []string{"leather_cap"},
			RarityWeights: [3]uint32{1, 0, 0}, UniquePermille: pm,
		}
	}
	a := sim.New(content.DB(), 5)
	b := sim.New(content.DB(), 5)
	items.RollItem(a.W, table(0), 20)
	items.RollItem(b.W, table(0), 20)
	if a.W.RNGLoot.State() != b.W.RNGLoot.State() {
		t.Fatal("identical zero-permille rolls diverged")
	}
	items.RollItem(b.W, table(1), 20) // one extra draw for the missed unique check
	if a.W.RNGLoot.State() == b.W.RNGLoot.State() {
		t.Fatal("a live permille consumed nothing — the check draw vanished")
	}
}

// TestStormweaverFansFireball: +1 projectile from a unique ring — the sheet
// stat nothing else rolls — turns every cast into a two-bolt fan.
func TestStormweaverFansFireball(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "fireball")
	equipUnique(t, s, player, "stormweaver_band")

	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "fireball", Point: space.V(fm.FromInt(10), 0)}})
	a := s.W.ActorByID(player)
	for a.Action.Kind == core.ActionSkill && a.Action.Phase == core.PhaseWindup {
		s.Step(nil)
	}
	live := 0
	for _, p := range s.W.Projectiles {
		if !p.Dead {
			live++
		}
	}
	if live != 2 {
		t.Fatalf("fireball with Stormweaver fired %d projectiles, want 2", live)
	}
}

// TestHydraChainsArc: +2 chain targets from the amulet — arc's three
// victims become five.
func TestHydraChainsArc(t *testing.T) {
	s := sim.New(content.DB(), 7)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "arc")
	equipUnique(t, s, player, "coil_of_the_hydra")
	// Six dummies in a tight line: base arc (3 targets) leaves three alive.
	for i := 0; i < 6; i++ {
		mustSpawn(t, s, "training_dummy", 6000+int64(i)*1500, 0)
	}
	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "arc", Point: space.V(fm.FromInt(6), 0)}})
	a := s.W.ActorByID(player)
	hits := 0
	for i := 0; i < 60 && a.Action.Kind == core.ActionSkill; i++ {
		s.Step(nil)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvHit && ev.Actor == player {
				hits++
			}
		}
	}
	if hits != 5 {
		t.Fatalf("arc with the Hydra coil struck %d targets, want 5 (3 base + 2)", hits)
	}
}

func TestOrbsRefuseUniques(t *testing.T) {
	s := sim.New(content.DB(), 3)
	player := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(player)
	item := rollUnique(t, s, "juggernauts_wall")
	a.Inventory = append(a.Inventory, item)
	a.Orbs[core.OrbTransmutation] = 1
	a.Orbs[core.OrbAlchemy] = 1
	a.Orbs[core.OrbChaos] = 1
	for _, orb := range []core.OrbKind{core.OrbTransmutation, core.OrbAlchemy, core.OrbChaos} {
		if items.ApplyOrb(s.W, a, orb, item.ID) {
			t.Errorf("%v crafted a unique", orb)
		}
	}
}

// TestUniqueSurvivesSaveAndTransfer: the def rides by ID through world
// saves and character extraction, mods intact on arrival.
func TestUniqueSurvivesSaveAndTransfer(t *testing.T) {
	s := sim.New(content.DB(), 9)
	player := mustSpawn(t, s, "player", 0, 0)
	equipUnique(t, s, player, "windrunner_treads")

	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	if restored.W.Hash() != s.W.Hash() {
		t.Fatal("unique-equipped world hashes differently after restore")
	}

	ch := core.ExtractCharacter(s.W.ActorByID(player))
	dest := sim.New(content.DB(), 11)
	arrived, err := core.InjectCharacter(dest.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	boots := arrived.Equipment[core.EquipBoots]
	if boots == nil || boots.Unique == nil || boots.Unique.ID != "windrunner_treads" {
		t.Fatalf("transferred boots = %+v, want the unique", boots)
	}
	// The mods came along: move speed well above the bare-def 5.
	speed := arrived.Sheet.Eval(stats.MoveSpeed, stats.TagSet{})
	if speed <= fm.FromInt(5) {
		t.Fatalf("move speed %v after transfer, want the unique's boost applied", speed)
	}
}
