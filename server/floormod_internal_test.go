package server

// Floor-modifier + descent-chart unit tests: deterministic rolls, mods
// actually landing on monsters, the chart's offer shape, and the
// stairs → chart → route flow.

import (
	"encoding/json"
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

func TestModCountBands(t *testing.T) {
	cases := []struct{ floor, want int }{{1, 0}, {3, 0}, {4, 1}, {9, 1}, {10, 2}, {19, 2}, {20, 3}, {50, 3}}
	for _, c := range cases {
		if got := modCountForFloor(c.floor); got != c.want {
			t.Errorf("modCountForFloor(%d) = %d, want %d", c.floor, got, c.want)
		}
	}
	if modCountAt(4, 1, 0) != 2 {
		t.Error("route 1 should be one mod greedier")
	}
	if modCountAt(4, 0, 2) != 3 {
		t.Error("chambers should stack one mod each")
	}
}

func TestRollFloorModsDeterministicAndDistinct(t *testing.T) {
	a := rollFloorMods(99, 12, 1, 0, 3)
	b := rollFloorMods(99, 12, 1, 0, 3)
	if len(a) != 3 || len(b) != 3 {
		t.Fatalf("rolled %d/%d mods, want 3", len(a), len(b))
	}
	seen := map[string]bool{}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("same address rolled different mods: %v vs %v", a, b)
		}
		if seen[a[i].ID] {
			t.Fatalf("duplicate mod %q in one roll", a[i].ID)
		}
		seen[a[i].ID] = true
	}
	c := rollFloorMods(99, 12, 0, 0, 3)
	same := true
	for i := range c {
		if c[i].ID != a[i].ID {
			same = false
		}
	}
	if same {
		t.Error("different routes rolled identical mods — suspicious salt")
	}
}

// TestFloorModsLandOnMonsters: build a modded floor whose roll includes a
// monster-facing package and assert every monster carries it; and the
// same address rebuilds to the same world hash while a different route
// diverges.
func TestFloorModsLandOnMonsters(t *testing.T) {
	in, _, _ := descentInstance(t, 3)
	// Find a route address at floor 12 whose mods include a MonMod package
	// (12 rolls two mods from a table of seven; scan chambers until one
	// includes a monster-facing mod — deterministic for the fixed seed).
	var route, chamber = -1, -1
	var wantMod string
scan:
	for r := 0; r < 2; r++ {
		for ch := 0; ch < 4; ch++ {
			for _, m := range rollFloorMods(in.runSeed, 12, r, ch, modCountAt(12, r, ch)) {
				if m.MonMod != "" {
					route, chamber, wantMod = r, ch, m.MonMod
					break scan
				}
			}
		}
	}
	if route < 0 {
		t.Fatal("no address at floor 12 rolled a monster-facing mod — table drifted?")
	}
	s, err := in.buildFloor(12, route, chamber)
	if err != nil {
		t.Fatal(err)
	}
	monsters, carrying := 0, 0
	for _, a := range s.W.Actors {
		if a.Team != core.TeamMonsters {
			continue
		}
		monsters++
		for _, md := range a.Mods {
			if md.ID == wantMod {
				carrying++
				break
			}
		}
	}
	if monsters == 0 || carrying != monsters {
		t.Fatalf("%d/%d monsters carry floor mod %q, want all", carrying, monsters, wantMod)
	}

	// Replayable: same address → same hash; different route → different.
	s2, err := in.buildFloor(12, route, chamber)
	if err != nil {
		t.Fatal(err)
	}
	if s.W.Hash() != s2.W.Hash() {
		t.Error("same route address built different worlds")
	}
	s3, err := in.buildFloor(12, 1-route, chamber)
	if err != nil {
		t.Fatal(err)
	}
	if s.W.Hash() == s3.W.Hash() {
		t.Error("different routes built identical worlds")
	}
}

// TestChartFlowOverRunTick: standing at the stairs, "descend" answers with
// a chart frame (no swap); a "route" pick executes it. The side chamber
// holds the depth.
func TestChartFlowOverRunTick(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	// Jump to floor 4 (first modded depth) with the client carried along.
	in.descend()
	in.descend()
	in.descend()
	if in.floor != 4 {
		t.Fatalf("floor = %d, want 4", in.floor)
	}
	// Stand at the stairs and ask to descend: a chart, not a swap.
	a := in.sim.W.ActorByID(c.actor)
	a.Pos = in.stairs
	tr.mu.Lock()
	tr.frames = nil
	tr.mu.Unlock()
	in.runTick(nil, []*client{c}, nil, nil, nil)
	if in.floor != 4 {
		t.Fatalf("descend swapped immediately to floor %d; want a chart first", in.floor)
	}
	var chart *protocol.ChartSnap
	tr.mu.Lock()
	for _, f := range tr.frames {
		var msg protocol.ServerMsg
		if json.Unmarshal(f, &msg) == nil && msg.Type == "chart" {
			chart = msg.Chart
		}
	}
	tr.mu.Unlock()
	if chart == nil {
		t.Fatal("no chart frame arrived")
	}
	if len(chart.Routes) != 3 {
		t.Fatalf("chart offers %d routes, want 2 down + 1 side chamber", len(chart.Routes))
	}
	if !chart.Routes[2].Side || chart.Routes[2].Floor != 4 {
		t.Fatalf("route 2 = %+v, want a side chamber at floor 4", chart.Routes[2])
	}
	if len(chart.Routes[1].Mods) != len(chart.Routes[0].Mods)+1 {
		t.Errorf("route 1 should be one mod greedier: %d vs %d",
			len(chart.Routes[1].Mods), len(chart.Routes[0].Mods))
	}

	// Take the greedy route down.
	a.Pos = in.stairs
	in.runTick(nil, nil, nil, nil, []routeWant{{c: c, choice: 1}})
	if in.floor != 5 || in.route != 1 || in.chamber != 0 {
		t.Fatalf("after route 1: floor=%d route=%d chamber=%d, want 5/1/0", in.floor, in.route, in.chamber)
	}

	// And a side chamber from there: depth held, chamber counted.
	a = in.sim.W.ActorByID(c.actor)
	a.Pos = in.stairs
	in.runTick(nil, nil, nil, nil, []routeWant{{c: c, choice: 2}})
	if in.floor != 5 || in.chamber != 1 {
		t.Fatalf("after side chamber: floor=%d chamber=%d, want 5/1", in.floor, in.chamber)
	}
	// The run snap reads the chamber's stacked mods.
	if rs := in.runSnap(); len(rs.Mods) != modCountAt(5, in.route, 1) {
		t.Errorf("run snap mods = %v, want %d entries", rs.Mods, modCountAt(5, in.route, 1))
	}
}
