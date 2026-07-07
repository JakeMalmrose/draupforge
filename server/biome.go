// Biomes — depth bands with identity (ROADMAP v2 Track 2 item 2). A biome
// is host-layer flavor over the descent: which map generator carves the
// floor, which monster mix scatters onto it, and an id the client turns
// into a palette, a name, and an ambient tone. Bands are content the sim
// never sees: floor construction picks a biome, the run snapshot carries
// its id, and everything else is presentation.
package server

import (
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// biomeDef is one depth band. MinFloor is where the band starts (bands are
// ordered shallow → deep and pick by "deepest band at or above the floor").
// A nil Scatter keeps the scenario's roster — the crypt IS the scenario's
// look, so existing configs and tests read exactly as before.
type biomeDef struct {
	ID       string
	Name     string
	MinFloor int
	MapKind  string // space.MapCaves, or "" for rooms-and-corridors
	Scatter  []protocol.Scatter
}

// biomes: the descent's depth bands. Rosters remix the existing monster
// pool — biome-native monsters land whenever content grows (Track 1's
// side of the seam); rosters here are appended-to, never reordered.
var biomes = []biomeDef{
	{ID: "crypt", Name: "the Barrow Crypt", MinFloor: 1},
	{ID: "caves", Name: "the Sunken Caves", MinFloor: 10, MapKind: space.MapCaves,
		Scatter: []protocol.Scatter{
			{Def: "ghoul", Count: 5},
			{Def: "carrion_husk", Count: 4},
			{Def: "zombie", Count: 3},
		}},
	{ID: "frost", Name: "the Frozen Deep", MinFloor: 20,
		Scatter: []protocol.Scatter{
			{Def: "skeleton_mage", Count: 5},
			{Def: "skeleton_archer", Count: 5},
			{Def: "ghoul", Count: 2},
		}},
}

// biomeForFloor picks the band a floor belongs to (nil for the hideout).
func biomeForFloor(floor int) *biomeDef {
	if floor <= 0 {
		return nil
	}
	b := &biomes[0]
	for i := range biomes {
		if floor >= biomes[i].MinFloor {
			b = &biomes[i]
		}
	}
	return b
}
