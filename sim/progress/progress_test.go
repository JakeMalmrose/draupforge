package progress_test

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// kill emits a death event for dier with killer credited, then awards XP —
// the same shape the sim produces during a tick.
func kill(w *core.World, dier, killer *core.Actor) {
	dier.Dead = true
	w.Emit(core.Event{Kind: core.EvDeath, Actor: dier.ID, Other: killer.ID})
	progress.AwardXP(w)
}

func TestAwardXPOnKill(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	zombie := w.SpawnActor(db.Actors["zombie"], space.V(5000, 0))

	kill(w, zombie, player)
	if player.XP != db.Actors["zombie"].XPValue {
		t.Fatalf("player XP = %d, want %d", player.XP, db.Actors["zombie"].XPValue)
	}
	if player.Level != 1 {
		t.Fatalf("player level = %d, want 1 (one zombie is not a level)", player.Level)
	}

	// Monsters killing players get nothing: players have no XPValue.
	w2 := core.NewWorld(db, 1)
	p2 := w2.SpawnActor(db.Actors["player"], space.V(0, 0))
	z2 := w2.SpawnActor(db.Actors["zombie"], space.V(5000, 0))
	kill(w2, p2, z2)
	if z2.XP != 0 {
		t.Fatalf("zombie XP = %d, want 0", z2.XP)
	}
}

func TestLevelUpAppliesGrowthAndHeals(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	dummy := w.SpawnActor(db.Actors["training_dummy"], space.V(5000, 0))

	baseMax := player.MaxLife()
	player.Life = fm.FromInt(1) // hurt, to observe the ding heal
	player.XP = progress.XPToNext(1) - 1
	kill(w, dummy, player) // +10 XP crosses the threshold

	if player.Level != 2 {
		t.Fatalf("player level = %d, want 2", player.Level)
	}
	wantXP := progress.XPToNext(1) - 1 + db.Actors["training_dummy"].XPValue - progress.XPToNext(1)
	if player.XP != wantXP {
		t.Errorf("XP after level = %d, want remainder %d", player.XP, wantXP)
	}
	// One level of growth: +12 life from the player's PerLevel package.
	if got, want := player.MaxLife(), baseMax+fm.FromInt(12); got != want {
		t.Errorf("max life at level 2 = %d, want %d", got, want)
	}
	if player.Life != player.MaxLife() {
		t.Errorf("life after ding = %d, want full %d", player.Life, player.MaxLife())
	}

	var leveled bool
	for _, ev := range w.Events() {
		if ev.Kind == core.EvLevelUp && ev.Actor == player.ID && ev.Amount == fm.FromInt(2) {
			leveled = true
		}
	}
	if !leveled {
		t.Error("no level_up event emitted")
	}
}

func TestMultiLevelSingleAward(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	dummy := w.SpawnActor(db.Actors["training_dummy"], space.V(5000, 0))

	// Enough banked XP to clear levels 1 and 2 in one kill.
	player.XP = progress.XPToNext(1) + progress.XPToNext(2) - 5
	kill(w, dummy, player)
	if player.Level != 3 {
		t.Fatalf("player level = %d, want 3", player.Level)
	}
	if got, want := player.MaxLife(), w.Content.Actors["player"].BaseStats[0]+fm.FromInt(24); got != want {
		t.Errorf("max life at level 3 = %d, want %d (2 levels of +12)", got, want)
	}
}

func TestMaxLevelCap(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	dummy := w.SpawnActor(db.Actors["training_dummy"], space.V(5000, 0))

	player.SetLevel(progress.MaxLevel)
	kill(w, dummy, player)
	if player.Level != progress.MaxLevel {
		t.Fatalf("level past cap: %d", player.Level)
	}
	if player.XP != 0 {
		t.Fatalf("XP accumulating at cap: %d", player.XP)
	}
}

func TestLevelSurvivesSaveLoad(t *testing.T) {
	db := content.DB()
	w := core.NewWorld(db, 1)
	player := w.SpawnActor(db.Actors["player"], space.V(0, 0))
	player.SetLevel(4)
	player.XP = 250
	player.Life = player.MaxLife()

	data, err := w.Save()
	if err != nil {
		t.Fatal(err)
	}
	w2, err := core.LoadWorld(db, data)
	if err != nil {
		t.Fatal(err)
	}
	p2 := w2.ActorByID(player.ID)
	if p2.Level != 4 || p2.XP != 250 {
		t.Fatalf("restored level/xp = %d/%d, want 4/250", p2.Level, p2.XP)
	}
	if p2.MaxLife() != player.MaxLife() {
		t.Fatalf("restored max life %d != original %d", p2.MaxLife(), player.MaxLife())
	}
	if w.Hash() != w2.Hash() {
		t.Fatal("hash mismatch after save/load round-trip")
	}
}
