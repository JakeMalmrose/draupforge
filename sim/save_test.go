package sim_test

import (
	"strings"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// TestSaveRestoreHash: a restored dungeon world hashes identically to the
// one it was saved from — terrain, actors, items, RNG streams, the lot.
func TestSaveRestoreHash(t *testing.T) {
	s, cmds := dungeonScenario(t)
	for tick := uint64(1); tick <= 200; tick++ {
		s.Step(cmds[tick])
	}
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	r, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.W.Hash(), s.W.Hash(); got != want {
		t.Fatalf("restored hash %016x, want %016x", got, want)
	}
}

// TestSaveRestoreContinuation is the property persistence actually exists
// for: a restored world's future is bit-identical to the world that never
// stopped. Mid-dungeon save — in-flight actions, paths, ailments, DoTs,
// drops, and half-consumed RNG streams all have to come back exactly.
func TestSaveRestoreContinuation(t *testing.T) {
	const saveAt, runTo = 200, dungeonTicks
	s, cmds := dungeonScenario(t)
	for tick := uint64(1); tick <= saveAt; tick++ {
		s.Step(cmds[tick])
	}
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	r, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	for tick := uint64(saveAt + 1); tick <= runTo; tick++ {
		s.Step(cmds[tick])
		r.Step(cmds[tick])
		if got, want := r.W.Hash(), s.W.Hash(); got != want {
			t.Fatalf("restored world diverged at tick %d: %016x != %016x", tick, got, want)
		}
	}
}

// TestSaveRestoreOpenPlane covers the nil-grid world plus the item flow:
// the slice scenario ends with the player holding equipped gear whose
// modifiers live on the sheet — those must survive verbatim, or post-restore
// combat math drifts.
func TestSaveRestoreOpenPlane(t *testing.T) {
	s, cmds := sliceScenario(t)
	st := &sliceState{}
	for tick := uint64(1); tick <= sliceTicks; tick++ {
		st.step(s, cmds[tick])
	}
	if !st.equipped {
		t.Fatal("slice did not reach the equipped state the test wants to save")
	}
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	r, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.W.Hash(), s.W.Hash(); got != want {
		t.Fatalf("restored hash %016x, want %016x", got, want)
	}
	for tick := 0; tick < 120; tick++ {
		s.Step(nil)
		r.Step(nil)
		if got, want := r.W.Hash(), s.W.Hash(); got != want {
			t.Fatalf("open-plane restore diverged %d ticks in: %016x != %016x", tick+1, got, want)
		}
	}
}

// TestLoadRejectsUnknownContent: a save naming content the registry no
// longer has must fail the whole load, not half-restore.
func TestLoadRejectsUnknownContent(t *testing.T) {
	s, cmds := dungeonScenario(t)
	for tick := uint64(1); tick <= 200; tick++ {
		s.Step(cmds[tick])
	}
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}

	mangled := strings.Replace(string(data), `"def":"zombie"`, `"def":"ogre"`, 1)
	if mangled == string(data) {
		t.Fatal("test setup: no zombie in the save to mangle")
	}
	if _, err := sim.Load(content.DB(), []byte(mangled)); err == nil {
		t.Error("load accepted a save with an unknown actor def")
	}

	if _, err := sim.Load(content.DB(), []byte(`{"version":99}`)); err == nil {
		t.Error("load accepted a save from the future")
	}
}

// TestSaveRefusedMidTick: Save is a tick-boundary operation; a world with
// unresolved hits must refuse.
func TestSaveRefusedMidTick(t *testing.T) {
	s := sim.New(content.DB(), 1)
	mustSpawn(t, s, "player", 0, 0)
	s.W.QueueHit(core.Hit{Attacker: 1, Defender: 1})
	if _, err := s.W.Save(); err == nil {
		t.Error("save succeeded with pending hits in flight")
	}
}
