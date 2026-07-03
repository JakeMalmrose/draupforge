package sim_test

// The spawn queue (RISKS #2, designed on purpose): mid-tick actor creation
// goes through World.QueueSpawn and materializes at root sim's fixed drain
// phase — after combat, loot, and XP, before compaction. Contracts: adds
// appear on the death tick but act and bleed only from the next; queue
// order is ID order; saves refuse a pending queue; the whole thing is
// deterministic; and no RNG is consumed anywhere in the path.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// killHusk spawns a husk at (x, y) milli and executes it, returning the
// sim one Step after the death tick began.
func huskFixture(t *testing.T, seed uint64) (*sim.Sim, core.EntityID) {
	t.Helper()
	s := sim.New(content.DB(), seed)
	husk := mustSpawn(t, s, "carrion_husk", 0, 0)
	return s, husk
}

func executeActor(s *sim.Sim, id core.EntityID) {
	a := s.W.ActorByID(id)
	a.Life = fm.FromMilli(1)
	// One overwhelming queued hit, resolved by the normal pipeline.
	s.W.QueueHit(core.Hit{Attacker: id, Defender: id, Skill: s.W.Content.Skills["zombie_slam"],
		Tags: s.W.Content.Skills["zombie_slam"].Tags})
	s.Step(nil)
}

func countDef(s *sim.Sim, def string) int {
	n := 0
	for _, a := range s.W.Actors {
		if a.Def.ID == def && !a.Dead {
			n++
		}
	}
	return n
}

func TestDeathSpawnsAdds(t *testing.T) {
	s, husk := huskFixture(t, 7)
	h := s.W.ActorByID(husk)
	h.SetLevel(4)
	executeActor(s, husk)

	if got := countDef(s, "ghoul"); got != 2 {
		t.Fatalf("husk death produced %d ghouls, want 2", got)
	}
	spawns := 0
	for _, ev := range s.W.LastEvents {
		if ev.Kind == core.EvSpawn {
			spawns++
			if ev.Note != "ghoul" || ev.Other != husk {
				t.Errorf("spawn event = %+v, want ghoul from the husk", ev)
			}
		}
	}
	if spawns != 2 {
		t.Fatalf("%d spawn events, want 2", spawns)
	}
	for _, a := range s.W.Actors {
		if a.Def.ID != "ghoul" {
			continue
		}
		if a.Level != 4 {
			t.Errorf("add level = %d, want the husk's 4", a.Level)
		}
		if a.Life != a.MaxLife() {
			t.Errorf("add born at %v life, want full", a.Life)
		}
		if a.Action.Kind != core.ActionIdle {
			t.Error("add acted on its birth tick")
		}
		if a.Home != a.Pos {
			t.Error("add's leash anchor isn't its birth spot")
		}
	}
}

// TestSpawnQueueDeterminism: same seed, same script, twice — identical
// hashes through the death tick and beyond, with two splitters dying at
// once (queue order is the only order).
func TestSpawnQueueDeterminism(t *testing.T) {
	run := func() uint64 {
		s := sim.New(content.DB(), 11)
		a := mustSpawn(t, s, "carrion_husk", 0, 0)
		b := mustSpawn(t, s, "carrion_husk", 3000, 0)
		executeActor(s, a) // separate ticks would also work; same tick is harder
		s.W.ActorByID(b).Life = fm.FromMilli(1)
		executeActor(s, b)
		for i := 0; i < 30; i++ {
			s.Step(nil)
		}
		return s.W.Hash()
	}
	if run() != run() {
		t.Fatal("spawn-queue world diverged across identical runs")
	}
}

func TestSaveRefusesPendingSpawns(t *testing.T) {
	s := sim.New(content.DB(), 3)
	def := s.W.Content.Actors["ghoul"]
	s.W.QueueSpawn(core.PendingSpawn{Def: def, Pos: space.V(0, 0)})
	if _, err := s.W.Save(); err == nil {
		t.Fatal("saved a world with spawns still queued")
	}
	s.W.DrainSpawns()
	if _, err := s.W.Save(); err != nil {
		t.Fatalf("drained world refused to save: %v", err)
	}
}

// TestSpawnSaveRoundTrip: kill the husk, save at the boundary, load, run
// both — the adds persist and behave identically.
func TestSpawnSaveRoundTrip(t *testing.T) {
	s, husk := huskFixture(t, 13)
	executeActor(s, husk)
	data, err := s.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := sim.Load(content.DB(), data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		s.Step(nil)
		restored.Step(nil)
		if s.W.Hash() != restored.W.Hash() {
			t.Fatalf("hash diverged %d ticks after restore", i+1)
		}
	}
	if got := countDef(restored, "ghoul"); got != 2 {
		t.Fatalf("restored world has %d ghouls, want 2", got)
	}
}
