// Run-save wrapper — the descent's host-layer state around World.Save.
// The sim's save knows nothing about runs (DESIGN §14: runs live at the
// host layer), so the server wraps the world bytes with its own envelope.
// Legacy bare-world files still load, resuming as floor 1 of a fresh run.
package server

import (
	"encoding/json"

	"github.com/JakeMalmrose/draupforge/sim/space"
)

// runSaveVersion gates the envelope like core.SaveVersion gates the world:
// shape changes bump it and old envelopes fail loudly. v2: PortalPlaced
// (runs start in the hideout with the portal anchor pending).
const runSaveVersion = 2

type runSave struct {
	RunVersion   int        `json:"run_version"`
	Run          int        `json:"run"`
	RunSeed      uint64     `json:"run_seed"`
	Floor        int        `json:"floor"`
	PortalsLeft  int        `json:"portals_left"`
	PortalFloor  int        `json:"portal_floor"`
	PortalPos    space.Vec2 `json:"portal_pos"`
	PortalPlaced bool       `json:"portal_placed"`
	Best         int        `json:"best"`
	// Route addresses (the descent chart) — additive to v2: old envelopes
	// read as the trunk path, which is what they were.
	Route         int             `json:"route,omitempty"`
	Chamber       int             `json:"chamber,omitempty"`
	PortalRoute   int             `json:"portal_route,omitempty"`
	PortalChamber int             `json:"portal_chamber,omitempty"`
	World         json.RawMessage `json:"world"`
}

// encodeRunSave wraps serialized world bytes with the instance's run state.
// Call on the tick goroutine — it reads live run fields.
func (in *Instance) encodeRunSave(world []byte) ([]byte, error) {
	return json.Marshal(runSave{
		RunVersion:    runSaveVersion,
		Run:           in.run,
		RunSeed:       in.runSeed,
		Floor:         in.floor,
		PortalsLeft:   in.portalsLeft,
		PortalFloor:   in.portalFloor,
		PortalPos:     in.portalPos,
		PortalPlaced:  in.portalPlaced,
		Best:          in.best,
		Route:         in.route,
		Chamber:       in.chamber,
		PortalRoute:   in.portalRoute,
		PortalChamber: in.portalChmbr,
		World:         world,
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
