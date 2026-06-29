package server

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

func monsterCount(s *sim.Sim) int {
	n := 0
	for _, a := range s.W.Actors {
		if a.Team == core.TeamMonsters {
			n++
		}
	}
	return n
}

func newHideoutInstance(t *testing.T) *Instance {
	t.Helper()
	in, err := New(content.DB(), Config{
		Seed:    9,
		Hideout: true,
		Map:     &protocol.MapSpec{Width: 30, Height: 30, Rooms: 6},
		Scatter: []protocol.Scatter{{Def: "zombie", Count: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// TestHideoutRunLoop walks the run/portal state machine: spawn safe in the
// hideout, enter a run, descend (packs scale), die down through the lives
// resuming each time, and on the last death roll a fresh run.
func TestHideoutRunLoop(t *testing.T) {
	in := newHideoutInstance(t)

	// Hideout: floor 0, no run, no monsters.
	if in.floor != 0 || in.lives != 0 {
		t.Fatalf("start floor/lives = %d/%d, want 0/0 (hideout)", in.floor, in.lives)
	}
	if n := monsterCount(in.sim); n != 0 {
		t.Errorf("hideout has %d monsters, want a safe zone", n)
	}

	// Enter a run from the hideout: floor 1, full lives, base-level packs.
	if !in.enterRun() {
		t.Fatal("enterRun failed")
	}
	if in.floor != 1 || in.runFloor != 1 || in.lives != livesPerRun {
		t.Fatalf("after enter: floor=%d runFloor=%d lives=%d, want 1/1/%d", in.floor, in.runFloor, in.lives, livesPerRun)
	}
	if lv := zombieLevels(in.sim); len(lv) != 3 || lv[0] != 1 {
		t.Errorf("floor 1 zombies = %v, want three at level 1", lv)
	}

	// Descend: floor 2, deeper packs.
	if !in.descendFloor() {
		t.Fatal("descendFloor failed")
	}
	if in.floor != 2 || in.runFloor != 2 {
		t.Errorf("after descend: floor=%d runFloor=%d, want 2/2", in.floor, in.runFloor)
	}
	if lv := zombieLevels(in.sim); len(lv) == 0 || lv[0] != 1+levelBonus(2) {
		t.Errorf("floor 2 zombie level = %v, want %d", lv, 1+levelBonus(2))
	}

	// Die with lives to spare: a death spends a life; rise lands in the hideout
	// with the run paused at its deepest floor.
	deadReq := []*client{{}} // zero actor → treated as dead
	in.lives--               // a death (would be noteDeaths over the wire)
	if !in.rise(deadReq) {
		t.Fatal("rise failed with a dead requester")
	}
	if in.floor != 0 || in.runFloor != 2 || in.lives != livesPerRun-1 {
		t.Fatalf("after death-rise: floor=%d runFloor=%d lives=%d, want 0/2/%d", in.floor, in.runFloor, in.lives, livesPerRun-1)
	}

	// Re-enter from the hideout: resume at the deepest floor, lives unchanged.
	if !in.enterRun() {
		t.Fatal("re-enter failed")
	}
	if in.floor != 2 || in.lives != livesPerRun-1 {
		t.Errorf("after re-enter: floor=%d lives=%d, want 2/%d (resume, no reset)", in.floor, in.lives, livesPerRun-1)
	}

	// Burn the rest of the lives. The final death ends the run.
	runBefore := in.runNumber
	for in.lives > 0 {
		in.lives--
		in.rise(deadReq)
	}
	if in.runFloor != 0 {
		t.Errorf("run not wound up: runFloor=%d, want 0 (portal starts fresh)", in.runFloor)
	}

	// The portal now rolls a brand-new run: floor 1, lives reset, new seed.
	if !in.enterRun() {
		t.Fatal("new run failed")
	}
	if in.floor != 1 || in.runFloor != 1 || in.lives != livesPerRun {
		t.Errorf("new run: floor=%d runFloor=%d lives=%d, want 1/1/%d", in.floor, in.runFloor, in.lives, livesPerRun)
	}
	if in.runNumber <= runBefore {
		t.Errorf("runNumber did not advance (%d -> %d) — fresh run should reseed", runBefore, in.runNumber)
	}
}
