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
	// The displaced ring lands in the bag, not on the ground.
	if got := liveDrops(w); got != 0 {
		t.Errorf("live drops = %d, want 0 (displaced item goes to inventory)", got)
	}
	if len(player.Inventory) != 1 || player.Inventory[0].Affixes[0].Value != fm.FromInt(20) {
		t.Fatalf("inventory = %+v, want exactly the displaced +20 ring", player.Inventory)
	}
}

func TestEquipDisplacedFallsToGroundWhenBagFull(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	// One-slot bag, pre-filled.
	def := *db.Actors["player"]
	def.InventorySize = 1
	player := w.SpawnActor(&def, space.V(0, 0))
	player.Inventory = append(player.Inventory, makeRing(w, db))

	for i := 0; i < 3; i++ { // third equip displaces ring1 with a full bag
		d := w.SpawnDrop(player.Pos, makeRing(w, db))
		items.Equip(w, player, d.ID)
	}
	if got := liveDrops(w); got != 1 {
		t.Errorf("live drops = %d, want 1 (full bag overflows displaced item to ground)", got)
	}
}

func TestPickupAndEquipFromInventory(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	lifeAffix := affixByID(t, db, "flat_life")

	ring := makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(20)})
	d := w.SpawnDrop(player.Pos, ring)
	if !items.Pickup(w, player, d.ID) {
		t.Fatal("pickup of in-range drop failed")
	}
	if len(player.Inventory) != 1 {
		t.Fatalf("inventory = %d items, want 1", len(player.Inventory))
	}
	baseMax := player.MaxLife() // picked up but not worn: no stats yet

	if !items.Equip(w, player, ring.ID) {
		t.Fatal("equip from inventory failed")
	}
	if len(player.Inventory) != 0 {
		t.Error("item stayed in inventory after equipping")
	}
	if got := player.MaxLife(); got != baseMax+fm.FromInt(20) {
		t.Errorf("max life = %d, want %d (affixes apply only when worn)", got, baseMax+fm.FromInt(20))
	}
}

func TestPickupRejectsWhenBagFull(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	def := *db.Actors["player"]
	def.InventorySize = 1
	player := w.SpawnActor(&def, space.V(0, 0))

	d1 := w.SpawnDrop(player.Pos, makeRing(w, db))
	d2 := w.SpawnDrop(player.Pos, makeRing(w, db))
	if !items.Pickup(w, player, d1.ID) {
		t.Fatal("first pickup failed")
	}
	if items.Pickup(w, player, d2.ID) {
		t.Error("pickup succeeded into a full bag")
	}
	if got := liveDrops(w); got != 1 {
		t.Errorf("live drops = %d, want 1 (rejected drop stays on the ground)", got)
	}
}

func TestUnequipToInventory(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	lifeAffix := affixByID(t, db, "flat_life")
	baseMax := player.MaxLife()

	ring := makeRing(w, db, core.RolledAffix{Def: lifeAffix, Value: fm.FromInt(20)})
	d := w.SpawnDrop(player.Pos, ring)
	items.Equip(w, player, d.ID)

	if !items.Unequip(w, player, ring.ID) {
		t.Fatal("unequip failed")
	}
	if player.Equipment[core.EquipRing1] != nil {
		t.Error("slot still occupied after unequip")
	}
	if len(player.Inventory) != 1 {
		t.Errorf("inventory = %d items, want 1", len(player.Inventory))
	}
	if got := player.MaxLife(); got != baseMax {
		t.Errorf("max life = %d, want base %d (mods must leave with the item)", got, baseMax)
	}
}

func TestUnequipRejectedWhenBagFull(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	def := *db.Actors["player"]
	def.InventorySize = 1
	player := w.SpawnActor(&def, space.V(0, 0))

	ring := makeRing(w, db)
	d := w.SpawnDrop(player.Pos, ring)
	items.Equip(w, player, d.ID)
	player.Inventory = append(player.Inventory, makeRing(w, db)) // fill the bag

	if items.Unequip(w, player, ring.ID) {
		t.Error("unequip succeeded into a full bag")
	}
	if player.Equipment[core.EquipRing1] == nil {
		t.Error("item left the slot despite rejected unequip")
	}
}

func TestDropItem(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))

	ring := makeRing(w, db)
	d := w.SpawnDrop(player.Pos, ring)
	items.Pickup(w, player, d.ID)

	if !items.DropItem(w, player, ring.ID) {
		t.Fatal("drop from inventory failed")
	}
	if len(player.Inventory) != 0 {
		t.Error("item stayed in inventory after dropping")
	}
	if got := liveDrops(w); got != 1 {
		t.Errorf("live drops = %d, want 1 (the dropped ring)", got)
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
