package server

// Biome unit tests: clump assignment on the delve chart, biome-built floors
// (map kind + roster), and the biome id on the run snap.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim"
)

// findBiomeNode scans the chart for a node of the wanted biome, shallowest
// first. Fails the test if none shows within maxRow — with depth-weighted
// clumps every biome should appear well before row 40.
func findBiomeNode(t *testing.T, runSeed uint64, biome string, maxRow int) nodeAddr {
	t.Helper()
	for row := 1; row <= maxRow; row++ {
		for _, col := range delveRow(runSeed, row) {
			n := nodeAddr{row, col}
			if b := delveBiome(runSeed, n); b != nil && b.ID == biome {
				return n
			}
		}
	}
	t.Fatalf("no %q node within %d rows — clump weights drifted?", biome, maxRow)
	return nodeAddr{}
}

// TestShallowRowsAreCrypt: the chart's first two rows are always the crypt,
// whatever the blob centers say — the early game reads as it always did.
func TestShallowRowsAreCrypt(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		for row := 1; row <= 2; row++ {
			for _, col := range delveRow(seed, row) {
				if b := delveBiome(seed, nodeAddr{row, col}); b == nil || b.ID != "crypt" {
					t.Fatalf("seed %d node %d:%d biome = %v, want crypt", seed, row, col, b)
				}
			}
		}
	}
}

// TestBiomesFormClumps: biome assignment is deterministic, every biome
// appears somewhere, and the deeps aren't crypt-dominated (the weights
// actually shift with depth).
func TestBiomesFormClumps(t *testing.T) {
	seed := uint64(9)
	counts := map[string]int{}
	for row := 1; row <= 40; row++ {
		for _, col := range delveRow(seed, row) {
			n := nodeAddr{row, col}
			a, b := delveBiome(seed, n), delveBiome(seed, n)
			if a != b {
				t.Fatalf("node %v biome not deterministic", n)
			}
			counts[a.ID]++
		}
	}
	for _, want := range []string{"crypt", "caves", "frost"} {
		if counts[want] == 0 {
			t.Errorf("biome %q never appears in 40 rows: %v", want, counts)
		}
	}
	deepCrypt := 0
	deepTotal := 0
	for row := 25; row <= 40; row++ {
		for _, col := range delveRow(seed, row) {
			deepTotal++
			if delveBiome(seed, nodeAddr{row, col}).ID == "crypt" {
				deepCrypt++
			}
		}
	}
	if deepCrypt*2 > deepTotal {
		t.Errorf("rows 25–40 are %d/%d crypt — depth weighting not biting", deepCrypt, deepTotal)
	}
}

// TestBuildFloorUsesBiomeRoster: a caves node scatters the caves mix (plus
// any death-spawn kin), never the scenario's crypt roster wholesale; a
// crypt node keeps the scenario's.
func TestBuildFloorUsesBiomeRoster(t *testing.T) {
	in, _, _ := descentInstance(t, 3)

	defsOn := func(s *sim.Sim) map[string]int {
		m := map[string]int{}
		for _, a := range s.W.Actors {
			m[a.Def.ID]++
		}
		return m
	}

	caves := findBiomeNode(t, in.runSeed, "caves", 40)
	s1, err := in.buildFloor(caves, 1) // fin 1: no set-piece in the census
	if err != nil {
		t.Fatal(err)
	}
	got := defsOn(s1)
	allowed := map[string]bool{"ghoul": true, "carrion_husk": true, "zombie": true}
	for def := range got {
		if !allowed[def] {
			t.Errorf("caves node %v spawned %q, not in the caves roster", caves, def)
		}
	}
	if got["ghoul"] == 0 || got["carrion_husk"] == 0 {
		t.Errorf("caves node roster = %v, want the caves mix present", got)
	}

	// The scenario roster (descentInstance uses dummy scatter) rides crypt
	// nodes unchanged.
	crypt := findBiomeNode(t, in.runSeed, "crypt", 5)
	s2, err := in.buildFloor(crypt, 1)
	if err != nil {
		t.Fatal(err)
	}
	if defsOn(s2)["training_dummy"] == 0 {
		t.Errorf("crypt node roster = %v, want the scenario's dummies", defsOn(s2))
	}
}

// TestCavesFloorsCarveCaves: a caves node's terrain rebuilds byte-identical
// (the replayable-floors invariant survives the biome switch) and differs
// from a crypt floor's rooms-and-corridors.
func TestCavesFloorsCarveCaves(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	caves := findBiomeNode(t, in.runSeed, "caves", 40)
	sa, err := in.buildFloor(caves, 1)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := in.buildFloor(caves, 1)
	if err != nil {
		t.Fatal(err)
	}
	ga, gb := sa.W.Grid, sb.W.Grid
	for y := 0; y < ga.Height; y++ {
		for x := 0; x < ga.Width; x++ {
			if ga.Solid(x, y) != gb.Solid(x, y) {
				t.Fatalf("caves node terrain not replayable at (%d,%d)", x, y)
			}
		}
	}
	crypt := findBiomeNode(t, in.runSeed, "crypt", 5)
	s2, err := in.buildFloor(crypt, 1)
	if err != nil {
		t.Fatal(err)
	}
	same := true
	g2 := s2.W.Grid
	for y := 0; y < ga.Height && same; y++ {
		for x := 0; x < ga.Width; x++ {
			if ga.Solid(x, y) != g2.Solid(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatal("caves floor terrain identical to a rooms floor")
	}
}

func TestRunSnapCarriesBiome(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	node := in.node
	in.node, in.fin, in.floor = nodeAddr{}, 0, 0
	if b := in.runSnap().Biome; b != "" {
		t.Errorf("hideout biome = %q, want empty", b)
	}
	in.node, in.fin, in.floor = node, 1, 1
	if b := in.runSnap().Biome; b != "crypt" {
		t.Errorf("entry node biome = %q, want crypt", b)
	}
	caves := findBiomeNode(t, in.runSeed, "caves", 40)
	in.node, in.fin, in.floor = caves, 1, globalFloor(caves.Row, 1)
	if b := in.runSnap().Biome; b != "caves" {
		t.Errorf("caves node biome on the snap = %q, want caves", b)
	}
}
