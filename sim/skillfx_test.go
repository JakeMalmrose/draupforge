package sim_test

// Session-35 skill feel mechanics: arc's hitscan chain, fireball's impact
// splash (with distance falloff), and spark's wall bounce. These are the
// behaviors that make the three starter spells read differently.

import (
	"strings"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// TestArcChainsThreeTargets: one cast, no projectile, three victims struck
// at the effect point — the aim-point target plus two chain hops. A fourth
// enemy beyond chain range stays untouched.
func TestArcChainsThreeTargets(t *testing.T) {
	s := sim.New(content.DB(), 90)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "arc")
	d1 := mustSpawn(t, s, "training_dummy", 5000, 0)
	d2 := mustSpawn(t, s, "training_dummy", 5000, 3000)
	d3 := mustSpawn(t, s, "training_dummy", 5000, -3000)
	far := mustSpawn(t, s, "training_dummy", 5000, 12000)

	castOnce(t, s, player, "arc", s.W.ActorByID(d1).Pos)
	if n := len(s.W.Projectiles); n != 0 {
		t.Errorf("arc spawned %d projectiles, want 0 — it is hitscan", n)
	}
	hits := map[core.EntityID]bool{}
	for _, ev := range s.W.LastEvents {
		if ev.Kind == core.EvHit && ev.Actor == player {
			if ev.Note != "arc" {
				t.Errorf("arc hit note = %q, want arc", ev.Note)
			}
			hits[ev.Other] = true
		}
	}
	if !hits[d1] || !hits[d2] || !hits[d3] {
		t.Errorf("arc hits = %v, want d1/d2/d3 (%d/%d/%d) all struck", hits, d1, d2, d3)
	}
	if hits[far] {
		t.Error("arc reached the dummy beyond chain range")
	}
	if len(hits) != 3 {
		t.Errorf("arc struck %d targets, want 3 (base 2 chains)", len(hits))
	}
}

// TestArcOutOfRangeFizzles: nothing within acquisition range means the cast
// completes but strikes no one — aim near something.
func TestArcOutOfRangeFizzles(t *testing.T) {
	s := sim.New(content.DB(), 90)
	player := mustSpawn(t, s, "player", 0, 0)
	grantGems(t, s, player, "arc")
	dummy := mustSpawn(t, s, "training_dummy", 15000, 0)

	castOnce(t, s, player, "arc", s.W.ActorByID(dummy).Pos)
	for i := 0; i < 30; i++ {
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvHit && ev.Actor == player {
				t.Fatal("arc struck a target 15u away; acquisition range is 12u")
			}
		}
		s.Step(nil)
	}
}

// TestFireballSplashes: the impact detonates — a bystander inside the
// explosion radius takes a distance-scaled ":aoe" hit, one outside takes
// nothing, and the direct target is never double-dipped.
func TestFireballSplashes(t *testing.T) {
	s := sim.New(content.DB(), 92)
	player := mustSpawn(t, s, "player", 0, 0)
	direct := mustSpawn(t, s, "training_dummy", 8000, 0)
	near := mustSpawn(t, s, "training_dummy", 8000, 1500)
	far := mustSpawn(t, s, "training_dummy", 8000, 3500)

	castOnce(t, s, player, "fireball", s.W.ActorByID(direct).Pos)
	byNote := map[string][]core.EntityID{}
	for i := 0; i < 60; i++ {
		s.Step(nil)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvHit && ev.Actor == player {
				byNote[ev.Note] = append(byNote[ev.Note], ev.Other)
			}
		}
		if len(byNote) > 0 {
			break // the impact tick carries direct hit and splash together
		}
	}
	if got := byNote["fireball"]; len(got) != 1 || got[0] != direct {
		t.Errorf("direct fireball hits = %v, want exactly [%d]", got, direct)
	}
	splash := byNote["fireball:aoe"]
	if len(splash) != 1 || splash[0] != near {
		t.Errorf("splash hits = %v, want exactly [%d] (near bystander)", splash, near)
	}
	for _, id := range splash {
		if id == far {
			t.Error("splash reached the far bystander outside the 2u radius")
		}
	}
}

// TestSparkBouncesOffWalls: a spark fired down a walled corridor reflects
// off the far wall (velocity X flips while it lives) instead of dying
// there, and still expires by TTL.
func TestSparkBouncesOffWalls(t *testing.T) {
	s := sim.New(content.DB(), 91)
	g := space.NewGrid(14, 5, fm.One, fm.FromMilli(650))
	for y := 1; y < 4; y++ {
		for x := 1; x < 13; x++ {
			g.SetSolid(x, y, false)
		}
	}
	g.Spawn = space.V(fm.FromMilli(2500), fm.FromMilli(2500))
	g.Finalize()
	s.W.Grid = g

	player := mustSpawn(t, s, "player", 2500, 2500)
	grantGems(t, s, player, "spark")
	castOnce(t, s, player, "spark", space.V(fm.FromInt(12), fm.FromMilli(2500)))
	if len(s.W.Projectiles) != 1 {
		t.Fatalf("spark spawned %d projectiles, want 1", len(s.W.Projectiles))
	}

	bounced := false
	for i := 0; i < 80 && len(s.W.Projectiles) > 0; i++ {
		if p := s.W.Projectiles[0]; !p.Dead && p.Vel.X < 0 {
			bounced = true
		}
		s.Step(nil)
	}
	if !bounced {
		t.Error("spark never reflected off the east wall")
	}
	if n := len(s.W.Projectiles); n != 0 {
		t.Errorf("%d sparks still alive after TTL, want 0", n)
	}
}

// TestArcBoltNotCuttable: the mage keeps its projectile bolt, but the gem
// draft pool offers arc instead.
func TestArcBoltNotCuttable(t *testing.T) {
	db := content.DB()
	ids := make([]string, 0, len(db.Cuttable))
	for _, sk := range db.Cuttable {
		ids = append(ids, sk.ID)
	}
	joined := strings.Join(ids, ",")
	if strings.Contains(joined, "arc_bolt") {
		t.Errorf("arc_bolt is still cuttable: %s", joined)
	}
	if !strings.Contains(joined, "arc") {
		t.Errorf("arc missing from the draft pool: %s", joined)
	}
}
