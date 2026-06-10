package sim_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

const (
	sliceSeed  = 1
	sliceTicks = 300
)

// sliceScenario is the canonical vertical-slice fight: a player fireballing
// a training dummy while a zombie closes in and swings. Mirrors
// scripts/slice.json.
func sliceScenario(t *testing.T) (*sim.Sim, map[uint64][]core.Command) {
	t.Helper()
	s := sim.New(content.DB(), sliceSeed)
	mustSpawn(t, s, "player", 0, 0)
	mustSpawn(t, s, "training_dummy", 8000, 0)
	mustSpawn(t, s, "zombie", 0, 12000)

	cmds := map[uint64][]core.Command{}
	for _, tick := range []uint64{1, 23, 45, 67} {
		cmds[tick] = []core.Command{{
			Actor: 1,
			Kind:  core.CmdUseSkill,
			Skill: "fireball",
			Point: space.V(fm.FromInt(8), 0),
		}}
	}
	return s, cmds
}

func mustSpawn(t *testing.T, s *sim.Sim, def string, x, y int64) core.EntityID {
	t.Helper()
	id, err := s.Spawn(def, space.V(fm.FromMilli(x), fm.FromMilli(y)))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sliceState drives the reactive loot behavior tick by tick: once the
// dummy's drop appears, the player walks to it, picks it up into the
// inventory, then equips it from the bag — the full item flow. Reacting to
// deterministic events keeps the trace stable without hardcoding entity IDs
// or arrival ticks.
type sliceState struct {
	dropID   core.EntityID
	dropPos  space.Vec2
	itemID   core.EntityID // known after pickup
	equipped bool
}

func (st *sliceState) step(s *sim.Sim, scheduled []core.Command) {
	cmds := scheduled
	switch {
	case st.itemID != 0 && !st.equipped:
		cmds = append(append([]core.Command{}, cmds...),
			core.Command{Actor: 1, Kind: core.CmdEquip, TargetID: st.itemID})
	case st.dropID != 0:
		cmds = append(append([]core.Command{}, cmds...),
			core.Command{Actor: 1, Kind: core.CmdMove, Point: st.dropPos},
			core.Command{Actor: 1, Kind: core.CmdPickup, TargetID: st.dropID},
		)
	}
	s.Step(cmds)
	for _, ev := range s.W.LastEvents {
		switch {
		case ev.Kind == core.EvDrop && st.dropID == 0 && st.itemID == 0:
			st.dropID = ev.Other
			if d := s.W.DropByID(ev.Other); d != nil {
				st.dropPos = d.Pos
			}
		case ev.Kind == core.EvPickup && ev.Actor == 1:
			st.itemID = ev.Other
			st.dropID = 0
		case ev.Kind == core.EvEquip && ev.Actor == 1:
			st.equipped = true
		}
	}
}

func runSlice(t *testing.T) []uint64 {
	s, cmds := sliceScenario(t)
	st := &sliceState{}
	hashes := make([]uint64, 0, sliceTicks)
	for tick := uint64(1); tick <= sliceTicks; tick++ {
		st.step(s, cmds[tick])
		hashes = append(hashes, s.W.Hash())
	}
	return hashes
}

// TestDeterminism: same seed, same commands → byte-identical state every
// single tick. This is the foundational guarantee of the whole engine.
func TestDeterminism(t *testing.T) {
	a := runSlice(t)
	b := runSlice(t)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("worlds diverged at tick %d: %016x != %016x", i+1, a[i], b[i])
		}
	}
}

// TestGoldenReplay locks the slice's behavior against the committed hash
// trace. An intentional behavior change re-records with:
//
//	DRAUPFORGE_UPDATE_GOLDEN=1 go test ./sim/
func TestGoldenReplay(t *testing.T) {
	hashes := runSlice(t)
	var lines []string
	for i := 0; i < len(hashes); i += 30 {
		lines = append(lines, fmt.Sprintf("tick %3d %016x", i+1, hashes[i]))
	}
	got := strings.Join(lines, "\n") + "\n"

	goldenPath := filepath.Join("testdata", "golden_slice.txt")
	if os.Getenv("DRAUPFORGE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file (run with DRAUPFORGE_UPDATE_GOLDEN=1 to record): %v", err)
	}
	if got != string(want) {
		t.Errorf("replay diverged from golden trace.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestSliceOutcomes asserts the gameplay beats of the vertical slice
// actually happen: the dummy dies to fireballs, loot drops, the player walks
// over and equips it, and the zombie lands hits along the way.
func TestSliceOutcomes(t *testing.T) {
	s, cmds := sliceScenario(t)
	st := &sliceState{}

	var sawDummyDeath, sawDrop, sawZombieHit bool
	for tick := uint64(1); tick <= sliceTicks; tick++ {
		st.step(s, cmds[tick])
		for _, ev := range s.W.LastEvents {
			switch {
			case ev.Kind == core.EvDeath && ev.Actor == 2:
				sawDummyDeath = true
			case ev.Kind == core.EvDrop && ev.Actor == 2:
				sawDrop = true
			case ev.Kind == core.EvHit && ev.Actor == 3 && ev.Other == 1:
				sawZombieHit = true
			}
		}
	}

	if !sawDummyDeath {
		t.Error("training dummy never died — 4 fireballs (min 80 damage) should kill 80 life")
	}
	if !sawDrop {
		t.Error("no loot dropped from the dummy death (table chance is 100%)")
	}
	if !sawZombieHit {
		t.Error("zombie never landed a hit on the player")
	}
	if !st.equipped {
		t.Error("player never equipped the drop — the full loot loop must close")
	}

	player := s.W.ActorByID(1)
	if player == nil {
		t.Fatal("player died — zombie damage badly overtuned for the slice")
	}
	if player.Life >= player.MaxLife() {
		t.Error("player took no damage in 10 seconds adjacent to a zombie")
	}
	var hasEquipment bool
	for _, item := range player.Equipment {
		if item != nil {
			hasEquipment = true
		}
	}
	if st.equipped && !hasEquipment {
		t.Error("equip event fired but no item is in an equipment slot")
	}
}

// TestFrostNovaHitsAllInRange: the nova shape hits every hostile inside the
// radius with an independent roll, and nothing outside it.
func TestFrostNovaHitsAllInRange(t *testing.T) {
	s := sim.New(content.DB(), 5)
	player := mustSpawn(t, s, "player", 0, 0)
	near1 := mustSpawn(t, s, "training_dummy", 2000, 0)  // inside 4u radius
	near2 := mustSpawn(t, s, "training_dummy", 0, -3000) // inside
	far := mustSpawn(t, s, "training_dummy", 10000, 0)   // outside

	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "frost_nova"}})
	for s.W.ActorByID(player).Action.Kind == core.ActionSkill {
		s.Step(nil)
	}

	full := s.W.ActorByID(far).MaxLife()
	if got := s.W.ActorByID(near1).Life; got >= full {
		t.Error("dummy at 2u took no nova damage")
	}
	if got := s.W.ActorByID(near2).Life; got >= full {
		t.Error("dummy at 3u took no nova damage")
	}
	if got := s.W.ActorByID(far).Life; got != full {
		t.Errorf("dummy at 10u took damage (%d/%d) — outside the 4u nova", got, full)
	}
	// Independent rolls per target: identical damage on both would be a
	// one-roll-shared bug (12–18 range makes a collision unlikely).
	if s.W.ActorByID(near1).Life == s.W.ActorByID(near2).Life {
		t.Log("warning: both nova targets took identical damage — possible shared roll (or 1-in-~6000 coincidence)")
	}
}

// TestCommandValidation: the sim is the authority — invalid commands are
// dropped, mana is enforced.
func TestCommandValidation(t *testing.T) {
	s := sim.New(content.DB(), 99)
	player := mustSpawn(t, s, "player", 0, 0)

	// Unknown skill for this actor.
	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "zombie_slam"}})
	if a := s.W.ActorByID(player); a.Action.Kind != core.ActionIdle {
		t.Error("actor used a skill not in its def")
	}

	// Burn mana down: 5 casts × 10 cost vs 50 pool. Wait out each cast.
	for i := 0; i < 5; i++ {
		s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "fireball", Point: space.V(fm.FromInt(10), 0)}})
		for s.W.ActorByID(player).Action.Kind == core.ActionSkill {
			s.Step(nil)
		}
	}
	a := s.W.ActorByID(player)
	if a.Mana >= fm.FromInt(10) {
		t.Skip("regen kept mana above one cast; validation path not exercised")
	}
	s.Step([]core.Command{{Actor: player, Kind: core.CmdUseSkill, Skill: "fireball", Point: space.V(fm.FromInt(10), 0)}})
	if s.W.ActorByID(player).Action.Kind == core.ActionSkill {
		t.Error("cast went through without enough mana")
	}
}
