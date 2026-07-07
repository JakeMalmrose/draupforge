// Run-save wrapper — the descent's host-layer state around World.Save.
// The sim's save knows nothing about runs (DESIGN §14: runs live at the
// host layer), so the server wraps the world bytes with its own envelope.
// Legacy bare-world files still load, resuming as the entry node's first
// floor of a fresh run.
package server

import (
	"encoding/json"

	"github.com/JakeMalmrose/draupforge/sim/space"
)

// runSaveVersion gates the envelope like core.SaveVersion gates the world:
// shape changes bump it and old envelopes fail loudly. v3: the delve chart
// (node addresses replace linear floors; visited/cleared travel along).
const runSaveVersion = 3

type runSave struct {
	RunVersion   int        `json:"run_version"`
	Run          int        `json:"run"`
	RunSeed      uint64     `json:"run_seed"`
	Floor        int        `json:"floor"`
	PortalsLeft  int        `json:"portals_left"`
	Node         nodeAddr   `json:"node"`
	Fin          int        `json:"fin"`
	PortalNode   nodeAddr   `json:"portal_node"`
	PortalFin    int        `json:"portal_fin"`
	PortalPos    space.Vec2 `json:"portal_pos"`
	PortalPlaced bool       `json:"portal_placed"`
	Best         int        `json:"best"`
	// Visited/Cleared list the chart bookkeeping in canonical order (rows
	// ascending, columns ascending — encodeRunSave scans the lattice, so
	// the bytes are deterministic despite the in-memory maps).
	Visited []nodeAddr      `json:"visited,omitempty"`
	Cleared []nodeAddr      `json:"cleared,omitempty"`
	World   json.RawMessage `json:"world"`
}

// chartList collects the members of a node set in canonical lattice order.
func (in *Instance) chartList(set map[nodeAddr]bool) []nodeAddr {
	var out []nodeAddr
	for row := 1; row <= in.maxRow; row++ {
		for _, col := range delveRow(in.runSeed, row) {
			if set[nodeAddr{row, col}] {
				out = append(out, nodeAddr{row, col})
			}
		}
	}
	return out
}

// encodeRunSave wraps serialized world bytes with the instance's run state.
// Call on the tick goroutine — it reads live run fields.
func (in *Instance) encodeRunSave(world []byte) ([]byte, error) {
	return json.Marshal(runSave{
		RunVersion:   runSaveVersion,
		Run:          in.run,
		RunSeed:      in.runSeed,
		Floor:        in.floor,
		PortalsLeft:  in.portalsLeft,
		Node:         in.node,
		Fin:          in.fin,
		PortalNode:   in.portalNode,
		PortalFin:    in.portalFin,
		PortalPos:    in.portalPos,
		PortalPlaced: in.portalPlaced,
		Best:         in.best,
		Visited:      in.chartList(in.visited),
		Cleared:      in.chartList(in.cleared),
		World:        world,
	})
}

// decodeRunSave recognizes a run envelope; ok is false for legacy
// bare-world saves (which have no "world" key).
func decodeRunSave(data []byte) (*runSave, bool) {
	var rs runSave
	if err := json.Unmarshal(data, &rs); err != nil || rs.World == nil {
		return nil, false
	}
	return &rs, true
}
