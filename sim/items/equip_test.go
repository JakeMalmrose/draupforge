package items_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func affixByID(t *testing.T, db *core.ContentDB, id string) *core.AffixDef {
	t.Helper()
	for _, af := range db.Affixes {
		if af.ID == id {
			return af
		}
	}
	t.Fatalf("affix %q not in content", id)
	return nil
}

func makeRing(w *core.World, db *core.ContentDB, affixes ...core.RolledAffix) core.Item {
	return core.Item{
		ID:      w.AllocID(),
		Base:    db.BaseItems["iron_ring"],
		Rarity:  core.RarityMagic,
		Affixes: affixes,
	}
}

func TestEquipAppliesModifiersAndFillsSlots(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	lifeAffix := affixByID(t, db, "flat_life")

	baseMax := player.MaxLife()

	ring1 := makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(20)})
	d1 := w.SpawnDrop(player.Pos, ring1)
	if !items.Equip(w, player, d1.ID) {
		t.Fatal("equip of in-range drop failed")
	}
	if got := player.MaxLife(); got != baseMax+fm.FromInt(20) {
		t.Errorf("max life after +20 ring = %d, want %d", got, baseMax+fm.FromInt(20))
	}
	if player.Equipment[core.EquipRing1] == nil {
		t.Fatal("first ring did not land in ring1")
	}

	// Second ring goes to the empty ring2.
	ring2 := makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(10)})
	d2 := w.SpawnDrop(player.Pos, ring2)
	items.Equip(w, player, d2.ID)
	if player.Equipment[core.EquipRing2] == nil {
		t.Fatal("second ring did not land in ring2")
	}
	if got := player.MaxLife(); got != baseMax+fm.FromInt(30) {
		t.Errorf("max life with both rings = %d, want %d", got, baseMax+fm.FromInt(30))
	}
}

func TestEquipReplaceDropsOldItemAndRemovesItsMods(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	lifeAffix := affixByID(t, db, "flat_life")
	baseMax := player.MaxLife()

	// Fill both ring slots.
	for i := 0; i < 2; i++ {
		d := w.SpawnDrop(player.Pos, makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(20)}))
		items.Equip(w, player, d.ID)
	}
	// Third ring must displace ring1; its +20 leaves, +5 arrives.
	d := w.SpawnDrop(player.Pos, makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(5)}))
	items.Equip(w, player, d.ID)

	if got := player.MaxLife(); got != baseMax+fm.FromInt(25) {
		t.Errorf("max life after replace = %d, want %d (20 removed, 5 added)", got, baseMax+fm.FromInt(25))
	}
	// The picked-up drop is consumed; exactly the displaced +20 ring remains.
	var ground []*core.Drop
	for _, dr := range w.Drops {
		if !dr.Taken {
			ground = append(ground, dr)
		}
	}
	if len(ground) != 1 {
		t.Fatalf("live drops = %d, want 1 (the displaced ring at the player's feet)", len(ground))
	}
	if v := ground[0].Item.Affixes[0].Value; v != fm.FromInt(20) {
		t.Errorf("ground item affix = %d, want the displaced ring's 20000", v)
	}
}

func TestEquipClampsPoolsWhenMaxShrinks(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	lifeAffix := affixByID(t, db, "flat_life")
	armourAffix := affixByID(t, db, "flat_armour")

	// Equip +25 life, top off, then replace it (ring2 also full) with an
	// armour ring: max drops by 25 and current life must follow it down.
	d1 := w.SpawnDrop(player.Pos, makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(25)}))
	items.Equip(w, player, d1.ID)
	d2 := w.SpawnDrop(player.Pos, makeRing(w, db))
	items.Equip(w, player, d2.ID)
	player.Life = player.MaxLife()

	d3 := w.SpawnDrop(player.Pos, makeRing(w, db, core.RolledAffix{Def: armourAffix, Value: fm.FromInt(15)}))
	items.Equip(w, player, d3.ID)

	if player.Life > player.MaxLife() {
		t.Errorf("life %d exceeds max %d after unequip — pools must clamp", player.Life, player.MaxLife())
	}
}

func TestEquipRejectsOutOfRangeAndTakenDrops(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))

	far := w.SpawnDrop(space.V(fm.FromInt(10), 0), makeRing(w, db))
	if items.Equip(w, player, far.ID) {
		t.Error("equipped a drop 10 units away (pickup range is 2)")
	}

	near := w.SpawnDrop(player.Pos, makeRing(w, db))
	if !items.Equip(w, player, near.ID) {
		t.Fatal("first equip of near drop failed")
	}
	if items.Equip(w, player, near.ID) {
		t.Error("equipped the same (taken) drop twice")
	}
}

func liveDrops(w *core.World) int {
	n := 0
	for _, d := range w.Drops {
		if !d.Taken {
			n++
		}
	}
	return n
}
