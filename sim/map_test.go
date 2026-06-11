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

// simWithGrid builds a sim on a hand-drawn map ('#' solid, '.' floor).
func simWithGrid(rows []string, seed uint64) *sim.Sim {
	s := sim.New(content.DB(), seed)
	g := space.NewGrid(len(rows[0]), len(rows), fm.One, fm.FromMilli(650))
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			g.SetSolid(x, y, row[x] == '#')
		}
	}
	g.Finalize()
	s.W.Grid = g
	return s
}

// TestMoveAroundWall: a click on the far side of a wall routes through the
// gap and arrives exactly — the full CmdMove → A* → waypoint-following path.
func TestMoveAroundWall(t *testing.T) {
	s := simWithGrid([]string{
		"############",
		"#....#.....#",
		"#....#.....#",
		"#....#.....#",
		"#....#.....#",
		"#....#.....#",
		"#....#.....#",
		"#..........#",
		"#..........#",
		"#..........#",
		"############",
	}, 11)
	player := mustSpawn(t, s, "player", 2500, 2500)
	target := s.W.Grid.TileCenter(8, 2)

	s.Step([]core.Command{{Actor: player, Kind: core.CmdMove, Point: target}})
	for i := 0; i < 600 && s.W.ActorByID(player).Action.Kind == core.ActionMove; i++ {
		s.Step(nil)
	}

	a := s.W.ActorByID(player)
	if a.Action.Kind == core.ActionMove {
		t.Fatal("player still walking after 600 ticks — path runaway")
	}
	if a.Pos != target {
		t.Errorf("player stopped at %v, want exact arrival at %v", a.Pos, target)
	}
}

// TestProjectileBlockedByWall: a fireball at an actor behind a solid wall
// dies at the wall — no hit, no damage.
func TestProjectileBlockedByWall(t *testing.T) {
	s := simWithGrid([]string{
		"############",
		"#....#.....#",
		"#....#.....#",
		"#....#.....#",
		"#....#.....#",
		"############",
	}, 12)
	player := mustSpawn(t, s, "player", 2500, 2500)
	dummy := mustSpawn(t, s, "training_dummy", 8500, 2500)

	s.Step([]core.Command{{
		Actor: player, Kind: core.CmdUseSkill, Skill: "fireball",
		Point: s.W.ActorByID(dummy).Pos,
	}})
	for i := 0; i < 120; i++ {
		s.Step(nil)
		for _, ev := range s.W.LastEvents {
			if ev.Kind == core.EvHit {
				t.Fatalf("hit landed through a wall at tick %d", s.W.Tick)
			}
		}
	}
	d := s.W.ActorByID(dummy)
	if d.Life != d.MaxLife() {
		t.Error("dummy took damage through a solid wall")
	}
	if len(s.W.Projectiles) != 0 {
		t.Error("projectile survived its wall impact")
	}
}

// TestRangedKiterShoots: with line of sight inside preferred range, the
// archer draws and fires at the player.
func TestRangedKiterShoots(t *testing.T) {
	s := sim.New(content.DB(), 13)
	mustSpawn(t, s, "player", 8000, 0)
	archer := mustSpawn(t, s, "skeleton_archer", 0, 0)

	sawArrow := false
	for i := 0; i < 60 && !sawArrow; i++ {
		s.Step(nil)
		for _, p := range s.W.Projectiles {
			if p.Source == archer && p.Skill.ID == "bone_arrow" {
				sawArrow = true
			}
		}
	}
	if !sawArrow {
		t.Error("archer with clear line of sight never fired in 2 seconds")
	}
}

// TestRangedKiterRetreats: an enemy inside a third of preferred range makes
// the archer back off before it resumes firing.
func TestRangedKiterRetreats(t *testing.T) {
	s := sim.New(content.DB(), 14)
	player := mustSpawn(t, s, "player", 2000, 0)
	archer := mustSpawn(t, s, "skeleton_archer", 0, 0)

	start := space.Dist(s.W.ActorByID(archer).Pos, s.W.ActorByID(player).Pos)
	for i := 0; i < 60; i++ {
		s.Step(nil)
	}
	end := space.Dist(s.W.ActorByID(archer).Pos, s.W.ActorByID(player).Pos)
	if end <= start {
		t.Errorf("archer didn't kite: distance %v -> %v", start, end)
	}
}

// TestRangedKiterAdvancesWithoutLoS: a wall between archer and target means
// no shot — the archer pathfinds toward the enemy instead.
func TestRangedKiterAdvancesWithoutLoS(t *testing.T) {
	s := simWithGrid([]string{
		"##############",
		"#.....#......#",
		"#.....#......#",
		"#.....#......#",
		"#............#",
		"#............#",
		"#............#",
		"##############",
	}, 15)
	mustSpawn(t, s, "player", 2500, 2500)
	archer := mustSpawn(t, s, "skeleton_archer", 10500, 2500)

	s.Step(nil)
	a := s.W.ActorByID(archer)
	if a.Action.Kind != core.ActionMove {
		t.Fatalf("wall-blocked archer's action = %v, want move (advance for line of sight)", a.Action.Kind)
	}
	if len(s.W.Projectiles) != 0 {
		t.Error("archer fired without line of sight")
	}
}

// --- the dungeon slice: generated map, scattered monsters, a player
// walking the rooms. This is the grid-world counterpart of the open-plane
// golden replay: mapgen, pathing, kiting, and wall collisions all consume
// their pinned RNG streams here.

const (
	dungeonSeed  = 7
	dungeonTicks = 450
)

func dungeonScenario(t *testing.T) (*sim.Sim, map[uint64][]core.Command) {
	t.Helper()
	s := sim.New(content.DB(), dungeonSeed)
	s.GenerateMap(space.MapSpec{Width: 40, Height: 40, Rooms: 7})
	g := s.W.Grid

	id, err := s.Spawn("player", g.Spawn)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ScatterSpawn("zombie", 4); err != nil {
		t.Fatal(err)
	}
	if err := s.ScatterSpawn("skeleton_archer", 3); err != nil {
		t.Fatal(err)
	}

	// Tour the map: walk toward spread-out walkable tiles, casting along the
	// way. Targets derive from the generated grid, so the script is a pure
	// function of the seed.
	centers := g.WalkableCenters()
	pick := func(i int) space.Vec2 { return centers[i*len(centers)/5] }
	cmds := map[uint64][]core.Command{
		1:   {{Actor: id, Kind: core.CmdMove, Point: pick(1)}},
		120: {{Actor: id, Kind: core.CmdMove, Point: pick(3)}},
		150: {{Actor: id, Kind: core.CmdUseSkill, Skill: "spark", Point: pick(2)}},
		260: {{Actor: id, Kind: core.CmdMove, Point: pick(4)}},
		290: {{Actor: id, Kind: core.CmdUseSkill, Skill: "frost_nova"}},
		380: {{Actor: id, Kind: core.CmdUseSkill, Skill: "fireball", Point: pick(0)}},
	}
	return s, cmds
}

func runDungeon(t *testing.T) []uint64 {
	s, cmds := dungeonScenario(t)
	hashes := make([]uint64, 0, dungeonTicks)
	for tick := uint64(1); tick <= dungeonTicks; tick++ {
		s.Step(cmds[tick])
		hashes = append(hashes, s.W.Hash())
	}
	return hashes
}

func TestDungeonDeterminism(t *testing.T) {
	a := runDungeon(t)
	b := runDungeon(t)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("dungeon worlds diverged at tick %d: %016x != %016x", i+1, a[i], b[i])
		}
	}
}

// TestGoldenDungeon locks grid-world behavior (mapgen, pathing, ranged AI,
// projectile-wall collision) against a committed trace. Re-record intended
// changes with DRAUPFORGE_UPDATE_GOLDEN=1, same as the slice golden.
func TestGoldenDungeon(t *testing.T) {
	hashes := runDungeon(t)
	var lines []string
	for i := 0; i < len(hashes); i += 30 {
		lines = append(lines, fmt.Sprintf("tick %3d %016x", i+1, hashes[i]))
	}
	got := strings.Join(lines, "\n") + "\n"

	goldenPath := filepath.Join("testdata", "golden_dungeon.txt")
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
		t.Errorf("dungeon replay diverged from golden trace.\ngot:\n%s\nwant:\n%s", got, want)
	}
}
