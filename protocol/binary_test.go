package protocol

import (
	"reflect"
	"testing"
)

func sampleView() Snapshot {
	return Snapshot{
		Tick: 100,
		Actors: []ActorSnap{
			{
				ID: 1, Def: "player", Team: 1,
				Pos: Vec{X: 1500, Y: -2000}, Radius: 400,
				Life: 90000, MaxLife: 100000, Mana: 40000, MaxMana: 50000,
				ES: 1000, Action: "idle", Ail: AilIgnited | AilChilled, InvSize: 20,
				Level: 3, XP: 250, XPNext: 900,
				Passives: []string{"iron_constitution", "executioner"},
				Flasks:   []int64{45, 60},
				Orbs:     []int64{3, 0, 7, 2},
				Gems: []GemSnap{
					{Skill: "fireball", Level: 4, Sockets: 2, Supports: []string{"chain", ""}, ManaCost: 16900},
					{Skill: "spark", Level: 1, Sockets: 1, Supports: []string{""}, ManaCost: 6000},
				},
				Equipment: []EquippedSnap{{
					Slot: "weapon",
					Item: ItemSnap{ID: 7, Base: "rusty_sword", Rarity: "magic",
						Implicit: &AffixSnap{ID: "increased_damage", Value: 80},
						Affixes:  []AffixSnap{{ID: "added_fire", Value: 3000}}},
				}},
				Inventory: []ItemSnap{
					{ID: 9, Base: "leather_cap", Rarity: "normal"},
					{ID: 14, Base: "iron_ring", Rarity: "unique",
						Implicit: &AffixSnap{ID: "mana", Value: 7000},
						Unique: &UniqueItemSnap{Name: "Stormweaver Band",
							Desc: "The storm never asks.", Mods: []string{"+1 projectile"}}},
					{ID: 10, Rarity: "normal", Gem: &GemItemSnap{
						Level: 6, Choices: []string{"spark", "arc_bolt", "frost_nova"}}},
					{ID: 12, Rarity: "normal", Gem: &GemItemSnap{
						Support: true, Choices: []string{"chain", "glaciate", "inspiration"}}},
				},
			},
			{
				ID: 2, Def: "zombie", Team: 2,
				Pos: Vec{X: 9000, Y: 9000}, Radius: 500,
				Life: 30000, MaxLife: 30000, Action: "move",
				Rarity: "rare", Mods: []string{"Fleet", "Brawny"},
				Telegraph: &TelegraphSnap{X: 8500, Y: 9200, Radius: 2200, Left: 12, Total: 24},
			},
		},
		Projectiles: []ProjectileSnap{
			{ID: 3, Skill: "fireball", Pos: Vec{X: 4000, Y: 4000}, Radius: 300},
		},
		Drops: []DropSnap{
			{ID: 4, Pos: Vec{X: 2000, Y: 2000},
				Item: ItemSnap{ID: 8, Base: "rusty_sword", Rarity: "rare",
					Affixes: []AffixSnap{{ID: "added_cold", Value: 1500}, {ID: "inc_life", Value: 12000}}}},
		},
		Events: []EventSnap{
			{Kind: "hit", Actor: 1, Other: 2, Amount: 4200, Note: "fire", Crit: true},
			{Kind: "death", Actor: 2},
		},
	}
}

func TestKeyframeRoundTrip(t *testing.T) {
	view := sampleView()
	frame := EncodeViewFrame(nil, &view)

	bt, err := BaselineTick(frame)
	if err != nil || bt != 0 {
		t.Fatalf("BaselineTick = %d, %v; want 0, nil", bt, err)
	}
	got, err := DecodeViewFrame(frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, view) {
		t.Errorf("keyframe round-trip mismatch:\n got %+v\nwant %+v", got, view)
	}
}

func TestDeltaRoundTrip(t *testing.T) {
	base := sampleView()

	// Next view: player moved and lost life, zombie died (removed), a new
	// zombie entered view, projectile moved, drop was picked up, new drop
	// appeared, player equipment changed.
	view := sampleView()
	view.Tick = 103
	view.Actors[0].Pos = Vec{X: 1800, Y: -1500}
	view.Actors[0].Life = 85000
	view.Actors[0].Ail = AilShocked
	view.Actors[0].Level = 4
	view.Actors[0].XP = 30
	view.Actors[0].XPNext = 1600
	view.Actors[0].Gems[0].Level = 9 // gem leveled between views
	view.Actors[0].Gems[0].ManaCost = 19600
	view.Actors[0].Equipment = nil
	view.Actors[0].Inventory = append(view.Actors[0].Inventory,
		ItemSnap{ID: 7, Base: "rusty_sword", Rarity: "magic",
			Affixes: []AffixSnap{{ID: "added_fire", Value: 3000}}})
	view.Actors = view.Actors[:1] // zombie 2 gone
	view.Actors = append(view.Actors, ActorSnap{
		ID: 5, Def: "zombie", Team: 2, Pos: Vec{X: 7000, Y: 0}, Radius: 500,
		Life: 30000, MaxLife: 30000, Action: "idle",
		Telegraph: &TelegraphSnap{X: 7000, Y: 0, Radius: 3500, Left: 30},
	})
	view.Projectiles[0].Pos = Vec{X: 5000, Y: 5000}
	view.Drops = []DropSnap{{ID: 6, Pos: Vec{X: 7000, Y: 0},
		Item: ItemSnap{ID: 11, Base: "leather_cap", Rarity: "normal"}}}
	view.Events = []EventSnap{{Kind: "pickup", Actor: 1, Note: "rusty_sword"}}

	frame := EncodeViewFrame(&base, &view)
	bt, err := BaselineTick(frame)
	if err != nil || bt != base.Tick {
		t.Fatalf("BaselineTick = %d, %v; want %d, nil", bt, err, base.Tick)
	}
	got, err := DecodeViewFrame(frame, &base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, view) {
		t.Errorf("delta round-trip mismatch:\n got %+v\nwant %+v", got, view)
	}

	// The delta must be meaningfully smaller than a keyframe of the same view.
	key := EncodeViewFrame(nil, &view)
	if len(frame) >= len(key) {
		t.Errorf("delta (%d bytes) not smaller than keyframe (%d bytes)", len(frame), len(key))
	}
}

func TestDeltaOfIdenticalViewsIsTiny(t *testing.T) {
	base := sampleView()
	view := sampleView()
	view.Tick = 103
	view.Events = nil

	frame := EncodeViewFrame(&base, &view)
	// type + tick + baseline + four empty entity lists + empty events ≈ a
	// dozen bytes; anything bigger means phantom diffs.
	if len(frame) > 16 {
		t.Errorf("no-change delta is %d bytes, want <= 16", len(frame))
	}
	got, err := DecodeViewFrame(frame, &base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, view) {
		t.Errorf("no-change delta round-trip mismatch")
	}
}

func TestDecodeRejectsWrongBaseline(t *testing.T) {
	base := sampleView()
	view := sampleView()
	view.Tick = 103
	frame := EncodeViewFrame(&base, &view)

	if _, err := DecodeViewFrame(frame, nil); err == nil {
		t.Error("decoding a delta without its baseline should fail")
	}
	wrong := sampleView()
	wrong.Tick = 99
	if _, err := DecodeViewFrame(frame, &wrong); err == nil {
		t.Error("decoding a delta against the wrong baseline tick should fail")
	}
}

func TestDecodeRejectsTruncatedFrame(t *testing.T) {
	view := sampleView()
	frame := EncodeViewFrame(nil, &view)
	for _, n := range []int{0, 1, len(frame) / 2, len(frame) - 1} {
		if _, err := DecodeViewFrame(frame[:n], nil); err == nil {
			t.Errorf("decoding %d/%d bytes should fail", n, len(frame))
		}
	}
}
