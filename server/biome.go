// Biomes — regions with identity (ROADMAP v2 Track 2 item 2, re-addressed
// by the delve chart). A biome is host-layer flavor over a delve node:
// which map generator carves its floors, which monster mix scatters onto
// them, and an id the client turns into a palette, a name, and an ambient
// tone. Biomes land on the chart as spatial clumps (delvemap.go's blob
// Voronoi), depth-weighted so the crypt owns the surface and the frost the
// deeps — but a cave clump can reach deep and a frost pocket can start
// early. The sim never sees any of it.
package server

import (
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// biomeDef is one biome. A nil Scatter keeps the scenario's roster — the
// crypt IS the scenario's look, so existing configs and tests read exactly
// as before.
type biomeDef struct {
	ID      string
	Name    string
	MapKind string // space.MapCaves, or "" for rooms-and-corridors
	Scatter []protocol.Scatter
}

// biomes: the chart's regions. Rosters remix the existing monster pool —
// biome-native monsters land whenever content grows (Track 1's side of the
// seam); rosters here are appended-to, never reordered.
var biomes = []biomeDef{
	{ID: "crypt", Name: "the Barrow Crypt"},
	{ID: "caves", Name: "the Sunken Caves", MapKind: space.MapCaves,
		Scatter: []protocol.Scatter{
			{Def: "ghoul", Count: 5},
			{Def: "carrion_husk", Count: 4},
			{Def: "zombie", Count: 3},
		}},
	{ID: "frost", Name: "the Frozen Deep",
		Scatter: []protocol.Scatter{
			{Def: "skeleton_mage", Count: 5},
			{Def: "skeleton_archer", Count: 5},
			{Def: "ghoul", Count: 2},
		}},
}

// biomeByID looks a biome up (nil when unknown — callers treat that as the
// scenario default, same as the crypt's nil scatter).
func biomeByID(id string) *biomeDef {
	for i := range biomes {
		if biomes[i].ID == id {
			return &biomes[i]
		}
	}
	return nil
}
