package core_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// affix looks an affix def up by ID (content keeps them in a slice).
func affix(db *core.ContentDB, id string) *core.AffixDef {
	for _, af := range db.Affixes {
		if af.ID == id {
			return af
		}
	}
	return nil
}

// buildVeteran makes a leveled, geared, dented player in a fresh world:
// a belt equipped (flat life implicit + flat life affix), a sword in the
// bag, half life — enough state to prove a round trip preserves the parts
// that must transfer and rebuilds the parts that must not.
func buildVeteran(t *testing.T, db *core.ContentDB, seed uint64) (*core.World, *core.Actor) {
	t.Helper()
	w := core.NewWorld(db, seed)
	a := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	a.SetLevel(5)
	a.XP = 700

	belt := core.Item{
		ID:       w.AllocID(),
		Base:     db.BaseItems["leather_belt"],
		Rarity:   core.RarityMagic,
		Implicit: fm.FromInt(15),
		Affixes:  []core.RolledAffix{{Def: affix(db, "flat_life"), Value: fm.FromInt(20)}},
	}
	a.Inventory = append(a.Inventory, belt)
	if !items.Equip(w, a, belt.ID, core.EquipAuto) {
		t.Fatal("equipping the belt failed")
	}
	sword := core.Item{ID: w.AllocID(), Base: db.BaseItems["rusty_sword"], Implicit: fm.FromMilli(80)}
	a.Inventory = append(a.Inventory, sword)

	a.Life = fm.Div(a.MaxLife(), fm.FromInt(2))
	return w, a
}

func TestExtractInjectRoundTrip(t *testing.T) {
	db := content.DB()
	_, a := buildVeteran(t, db, 1)
	ch := core.ExtractCharacter(a)

	wB := core.NewWorld(db, 2)
	// Diverge wB's ID counter so re-minted item IDs provably come from the
	// destination world, not the character.
	for i := 0; i < 3; i++ {
		wB.AllocID()
	}
	b, err := core.InjectCharacter(wB, ch, space.V(fm.FromInt(3), 0))
	if err != nil {
		t.Fatal(err)
	}

	if b.Level != a.Level || b.XP != a.XP {
		t.Errorf("level/xp = %d/%d, want %d/%d", b.Level, b.XP, a.Level, a.XP)
	}
	beltA := a.Equipment[core.EquipBelt]
	beltB := b.Equipment[core.EquipBelt]
	if beltB == nil || beltB.Base.ID != "leather_belt" {
		t.Fatalf("belt did not survive the transfer: %+v", beltB)
	}
	if beltB.ID == beltA.ID {
		t.Errorf("belt kept its old-world entity ID %d — IDs must be re-minted", beltA.ID)
	}
	if beltB.Implicit != beltA.Implicit || beltB.Affixes[0].Value != beltA.Affixes[0].Value {
		t.Errorf("belt rolls changed in transit")
	}
	if len(b.Inventory) != 1 || b.Inventory[0].Base.ID != "rusty_sword" {
		t.Errorf("bag = %+v, want the one sword", b.Inventory)
	}
	// The sheet must rebuild identically: same def, level, and gear on both
	// sides means identical evaluated stats.
	for _, st := range []stats.StatID{stats.Life, stats.Mana, stats.Accuracy, stats.Armour} {
		if got, want := b.Sheet.Eval(st, stats.TagSet{}), a.Sheet.Eval(st, stats.TagSet{}); got != want {
			t.Errorf("stat %d = %v, want %v", st, got, want)
		}
	}
	if b.Life != a.Life {
		t.Errorf("life = %v, want carried %v", b.Life, a.Life)
	}
	// Unequipping in the new world must cleanly remove the re-minted item's
	// mods — the proof the sheet is sourced by the new IDs.
	before := b.MaxLife()
	if !items.Unequip(wB, b, beltB.ID) {
		t.Fatal("unequip in destination world failed")
	}
	if b.MaxLife() >= before {
		t.Errorf("max life %v did not drop after unequip (was %v)", b.MaxLife(), before)
	}
}

func TestInjectRefillsDeadCharacter(t *testing.T) {
	db := content.DB()
	_, a := buildVeteran(t, db, 1)
	ch := core.ExtractCharacter(a)
	ch.Life, ch.Mana, ch.ES = 0, 0, 0 // the death-respawn convention

	wB := core.NewWorld(db, 2)
	b, err := core.InjectCharacter(wB, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if b.Life != b.MaxLife() || b.Mana != b.MaxMana() {
		t.Errorf("dead character arrived at %v/%v life, want full %v", b.Life, b.Mana, b.MaxLife())
	}
}

func TestInjectPoolsClampToRebuiltMaxima(t *testing.T) {
	db := content.DB()
	_, a := buildVeteran(t, db, 1)
	ch := core.ExtractCharacter(a)
	ch.Life = fm.FromInt(100000) // corrupt/stale beyond any maximum

	wB := core.NewWorld(db, 2)
	b, err := core.InjectCharacter(wB, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if b.Life != b.MaxLife() {
		t.Errorf("life %v exceeds max %v after inject", b.Life, b.MaxLife())
	}
}

func TestInjectUnknownContentFailsWholesale(t *testing.T) {
	db := content.DB()
	_, a := buildVeteran(t, db, 1)

	ch := core.ExtractCharacter(a)
	ch.Def = "not_a_def"
	wB := core.NewWorld(db, 2)
	if _, err := core.InjectCharacter(wB, ch, space.V(0, 0)); err == nil {
		t.Error("unknown def injected without error")
	}
	if len(wB.Actors) != 0 {
		t.Error("failed inject left an actor behind")
	}

	ch = core.ExtractCharacter(a)
	ch.Equipment[core.EquipBelt].Base = "not_a_base"
	if _, err := core.InjectCharacter(wB, ch, space.V(0, 0)); err == nil {
		t.Error("unknown base injected without error")
	}
	if len(wB.Actors) != 0 {
		t.Error("failed inject left an actor behind")
	}
}

func TestInjectClampsToWalkable(t *testing.T) {
	db := content.DB()
	_, a := buildVeteran(t, db, 1)
	ch := core.ExtractCharacter(a)

	wB := core.NewWorld(db, 7)
	wB.Grid = space.GenerateRooms(space.MapSpec{Width: 16, Height: 16, Rooms: 3}, wB.RNGMap)
	b, err := core.InjectCharacter(wB, ch, space.V(0, 0)) // (0,0) is border wall
	if err != nil {
		t.Fatal(err)
	}
	if !wB.Grid.Fits(b.Pos) {
		t.Errorf("injected actor stands in a wall at %v", b.Pos)
	}
}
