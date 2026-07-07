package server

// Depth-checkpoint unit tests: guardian kills mark the account, the
// hideout portal offers level-gated deep starts, and taking one trades a
// portal use.

import (
	"encoding/json"
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
)

func TestCheckpointStore(t *testing.T) {
	st, _ := NewIdentityStore("")
	tok, err := st.Claim("Delver", false, false)
	if err != nil {
		t.Fatal(err)
	}
	st.AddCheckpoint(tok, 6)
	st.AddCheckpoint(tok, 3)
	st.AddCheckpoint(tok, 6) // dupe: no-op
	st.AddCheckpoint(tok, 1) // floor 1 is the top; never a checkpoint
	st.AddCheckpoint("nobody", 9)
	got := st.Checkpoints(tok)
	if len(got) != 2 || got[0] != 3 || got[1] != 6 {
		t.Fatalf("checkpoints = %v, want [3 6]", got)
	}
	if st.Checkpoints("nobody") != nil {
		t.Error("unknown token grew checkpoints")
	}
}

// TestGuardianDeathMarksCheckpoint: a guardian death event on its floor
// records the checkpoint for every named client present.
func TestGuardianDeathMarksCheckpoint(t *testing.T) {
	in, c, _ := descentInstance(t, 3)
	tok, err := in.ids.Claim("Slayer", false, false)
	if err != nil {
		t.Fatal(err)
	}
	c.token = tok
	in.descend()
	in.descend() // floor 3: the guardian's floor
	if in.floor != 3 {
		t.Fatalf("floor = %d, want 3", in.floor)
	}
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: 999, Note: guardianDef}},
		nil, nil, nil, nil)
	if got := in.ids.Checkpoints(tok); len(got) != 1 || got[0] != 3 {
		t.Fatalf("checkpoints after guardian kill = %v, want [3]", got)
	}
	// A rank-and-file death marks nothing.
	in.runTick([]protocol.EventSnap{{Kind: "death", Actor: 998, Note: "zombie"}},
		nil, nil, nil, nil)
	if got := in.ids.Checkpoints(tok); len(got) != 1 {
		t.Fatalf("checkpoints after zombie kill = %v, want still [3]", got)
	}
}

// TestDeepStartFlow: at the hideout portal with an earned, level-covered
// checkpoint, enter_portal answers with a portal chart; picking the deep
// route starts the run there with one portal traded away; the level gate
// hides checkpoints a fresh alt hasn't grown into.
func TestDeepStartFlow(t *testing.T) {
	in, c, tr := descentInstanceAt(t, 3, 0) // hideout start
	tok, err := in.ids.Claim("Deepstarter", false, false)
	if err != nil {
		t.Fatal(err)
	}
	c.token = tok
	in.ids.AddCheckpoint(tok, 3)
	in.ids.AddCheckpoint(tok, 9)

	a := in.sim.W.ActorByID(c.actor)
	a.Pos = in.sim.W.Grid.Spawn
	a.SetLevel(5) // covers floor 3, not floor 9

	tr.mu.Lock()
	tr.frames = nil
	tr.mu.Unlock()
	in.runTick(nil, nil, []*client{c}, nil, nil)
	if in.floor != 0 {
		t.Fatalf("portal travelled immediately to floor %d; want a chart first", in.floor)
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
	if chart == nil || chart.Kind != "portal" {
		t.Fatalf("chart = %+v, want a portal-kind chart", chart)
	}
	if len(chart.Routes) != 2 {
		t.Fatalf("chart offers %d routes, want top + the level-covered checkpoint", len(chart.Routes))
	}
	if chart.Routes[0].Floor != 1 || chart.Routes[0].Portals != 3 {
		t.Errorf("route 0 = %+v, want floor 1 at full budget", chart.Routes[0])
	}
	if chart.Routes[1].Floor != 3 || chart.Routes[1].Portals != 2 {
		t.Errorf("route 1 = %+v, want floor 3 at one portal traded", chart.Routes[1])
	}

	// Take the deep start.
	in.runTick(nil, nil, nil, nil, []routeWant{{c: c, choice: 1}})
	if in.floor != 3 || in.portalsLeft != 2 || in.portalFloor != 3 || !in.portalPlaced {
		t.Fatalf("after deep start: floor=%d portals=%d anchor=%d placed=%v, want 3/2/3/true",
			in.floor, in.portalsLeft, in.portalFloor, in.portalPlaced)
	}

	// A guest at the portal never sees the chart: straight to floor 1.
	in2, c2, _ := descentInstanceAt(t, 3, 0)
	a2 := in2.sim.W.ActorByID(c2.actor)
	a2.Pos = in2.sim.W.Grid.Spawn
	in2.runTick(nil, nil, []*client{c2}, nil, nil)
	if in2.floor != 1 {
		t.Fatalf("guest portal entry landed on floor %d, want 1", in2.floor)
	}
}
