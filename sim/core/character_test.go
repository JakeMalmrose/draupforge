package core_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func findAffix(db *core.ContentDB, id string) *core.AffixDef {
	for _, af := range db.Affixes {
		if af.ID == id {
			return af
		}
	}
	return nil
}

// TestCharacterRoundTrip is the descent's core invariant: a leveled, geared
// player extracted from one world and injected into a fresh one keeps its
// identity, progression, and items — with item IDs re-minted and the stat
// sheet rebuilt from def + level + equipment, not copied.
func TestCharacterRoundTrip(t *testing.T) {
	db := content.DB()
	w1 := core.NewWorld(db, 1)
	p := w1.SpawnActor(db.Actors["player"], space.Vec2{})
	p.SetLevel(3)
	p.XP = 50

	belt := db.BaseItems["leather_belt"]
	lifeAffix := findAffix(db, "flat_life")
	if belt == nil || lifeAffix == nil {
		t.Fatal("content missing leather_belt / flat_life")
	}
	equipped := core.Item{
		ID: w1.AllocID(), Base: belt, Rarity: core.RarityMagic,
		Implicit: fm.FromInt(15),
		Affixes:  []core.RolledAffix{{Def: lifeAffix, Value: fm.FromInt(20)}},
	}
	p.Inventory = append(p.Inventory, equipped)
	if !items.Equip(w1, p, equipped.ID, core.EquipAuto) {
		t.Fatal("equip failed")
	}
	// A second item just rides along in the bag.
	bagItem := core.Item{ID: w1.AllocID(), Base: db.BaseItems["iron_ring"], Implicit: fm.FromInt(7)}
	p.Inventory = append(p.Inventory, bagItem)

	wantLife := p.MaxLife()
	origEquipID := p.Equipment[core.EquipBelt].ID

	ch := core.ExtractCharacter(p)

	// Inject into a brand-new world whose ID counter is already nonzero, so a
	// failure to re-mint would surface as a collision.
	w2 := core.NewWorld(db, 999)
	w2.SpawnActor(db.Actors["training_dummy"], space.V(fm.FromInt(5), fm.FromInt(5)))
	got, err := w2.InjectCharacter(ch, space.Vec2{})
	if err != nil {
		t.Fatal(err)
	}

	if got.Def.ID != "player" {
		t.Errorf("def = %q, want player", got.Def.ID)
	}
	if got.Level != 3 || got.XP != 50 {
		t.Errorf("progression = level %d xp %d, want level 3 xp 50", got.Level, got.XP)
	}
	if got.MaxLife() != wantLife {
		t.Errorf("MaxLife = %d, want %d — sheet not rebuilt from gear+level",
			got.MaxLife().Milli(), wantLife.Milli())
	}
	if got.Life != got.MaxLife() {
		t.Errorf("did not enter the floor at full life: %d/%d", got.Life.Milli(), got.MaxLife().Milli())
	}

	be := got.Equipment[core.EquipBelt]
	if be == nil {
		t.Fatal("belt not equipped after inject")
	}
	if be.Base.ID != "leather_belt" || be.Implicit != fm.FromInt(15) {
		t.Errorf("belt = %q implicit %d, want leather_belt 15", be.Base.ID, be.Implicit.Milli())
	}
	if len(be.Affixes) != 1 || be.Affixes[0].Value != fm.FromInt(20) {
		t.Errorf("belt affixes = %+v, want one flat_life of 20", be.Affixes)
	}
	if len(got.Inventory) != 1 || got.Inventory[0].Base.ID != "iron_ring" {
		t.Errorf("bag = %+v, want one iron_ring", got.Inventory)
	}

	// IDs re-minted: different from the source world, unique within w2, and
	// distinct from the actor's own ID.
	if be.ID == origEquipID {
		t.Errorf("equipped item ID not re-minted (still %d)", be.ID)
	}
	ids := map[core.EntityID]bool{got.ID: true}
	for _, it := range append([]core.Item{*be}, got.Inventory...) {
		if ids[it.ID] {
			t.Errorf("duplicate entity ID %d after inject", it.ID)
		}
		ids[it.ID] = true
	}

	// Unequipping in the new world cleanly removes the belt's life — proof the
	// mods were sourced under the re-minted ID, not the dead old one.
	if !items.Unequip(w2, got, be.ID) {
		t.Fatal("unequip failed in the new world")
	}
	if got.MaxLife() == wantLife {
		t.Error("unequip did not remove the belt's life — wrong mod source")
	}
}

func TestFloorSeed(t *testing.T) {
	if core.FloorSeed(42, 1) != 42 || core.FloorSeed(42, 0) != 42 {
		t.Error("floor <= 1 must equal the run seed (single-floor worlds unchanged)")
	}
	s2 := core.FloorSeed(42, 2)
	if s2 == 42 {
		t.Error("floor 2 seed must differ from the run seed")
	}
	if core.FloorSeed(42, 2) != s2 {
		t.Error("floor seed must be deterministic")
	}
	if core.FloorSeed(42, 3) == s2 {
		t.Error("different floors must derive different seeds")
	}
	if core.FloorSeed(43, 2) == s2 {
		t.Error("different runs must derive different seeds")
	}
}
