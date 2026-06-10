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

func runSlice(t *testing.T) []uint64 {
	s, cmds := sliceScenario(t)
	hashes := make([]uint64, 0, sliceTicks)
	for tick := uint64(1); tick <= sliceTicks; tick++ {
		s.Step(cmds[tick])
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
// actually happen: the dummy dies to fireballs, loot drops, the zombie
// reaches the player and deals damage.
func TestSliceOutcomes(t *testing.T) {
	s, cmds := sliceScenario(t)

	var sawDummyDeath, sawDrop, sawZombieHit bool
	for tick := uint64(1); tick <= sliceTicks; tick++ {
		s.Step(cmds[tick])
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

	player := s.W.ActorByID(1)
	if player == nil {
		t.Fatal("player died — zombie damage badly overtuned for the slice")
	}
	if player.Life >= player.MaxLife() {
		t.Error("player took no damage in 10 seconds adjacent to a zombie")
	}
	if len(s.W.Drops) == 0 {
		t.Error("no drops persisted in world state")
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
