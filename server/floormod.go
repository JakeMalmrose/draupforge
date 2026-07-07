// Floor modifiers (ROADMAP v2 Track 2 item 3, re-addressed by the delve
// chart). Nodes on the chart roll named, depth-scaled modifiers shared by
// all three of their floors, and you read them on the map BEFORE you
// commit. Rolls are pure functions of (run seed, node address) — see
// delvemap.go — so a death-eject rebuild re-rolls identically and the
// chart never lies about the node it sells.
package server

import (
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// floorModDef is one rollable floor modifier: the monster-facing package
// (a content mod id applied to every monster at build time; "" = none) and
// the floor-level knobs (extra packs, bonus rarity). Reward is the chart's
// juice pips — how loudly this mod pays.
type floorModDef struct {
	ID       string
	Name     string
	MonMod   string // content MonsterModDef/FloorModDef id, floor-wide
	RarityPm uint64 // added to both magic and rare permille
	PacksPct int    // extra scatter monsters, percent
	Reward   int    // 1–3 pips on the chart
}

// floorModTable is ordered — rollModsFrom indexes into it, and node
// rebuilds must re-roll identically, so append-only like every table.
var floorModTable = []floorModDef{
	{ID: "teeming", Name: "Teeming", PacksPct: 40, RarityPm: 25, Reward: 2},
	{ID: "fleet", Name: "Fleet-footed", MonMod: "fleet", RarityPm: 25, Reward: 1},
	{ID: "savage", Name: "Savage", MonMod: "deadly", RarityPm: 40, Reward: 2},
	{ID: "ironboned", Name: "Iron-boned", MonMod: "brawny", RarityPm: 25, Reward: 1},
	{ID: "warded", Name: "Warded", MonMod: "stalwart", RarityPm: 25, Reward: 1},
	{ID: "vampiric", Name: "Vampiric", MonMod: "floor_vampiric", RarityPm: 40, Reward: 2},
	{ID: "gilded", Name: "Gilded", RarityPm: 90, Reward: 3},
}

// rollModsFrom draws count distinct mods off an already-derived splitmix
// state — the shared tail of every mod roll (delveNodeMods owns the salt).
func rollModsFrom(st *uint64, count int) []floorModDef {
	if count <= 0 {
		return nil
	}
	if count > 4 {
		count = 4
	}
	if count > len(floorModTable) {
		count = len(floorModTable)
	}
	idx := make([]int, len(floorModTable))
	for i := range idx {
		idx[i] = i
	}
	picks := make([]floorModDef, 0, count)
	for k := 0; k < count; k++ {
		j := k + int(core.SplitMix64(st)%uint64(len(idx)-k))
		idx[k], idx[j] = idx[j], idx[k]
		picks = append(picks, floorModTable[idx[k]])
	}
	return picks
}

// routeOffer is one entry on the hideout portal's deep-start chart: enter
// the delve at a node (row 1's trunk by default, or an earned checkpoint
// row), with the portal budget the depth costs.
type routeOffer struct {
	choice  int
	node    nodeAddr
	portals int
	mods    []floorModDef
}

// chartSnapKind builds a chart frame body (the hideout's deep-start chart;
// stairs travel goes through the delve map now).
func (in *Instance) chartSnapKind(kind string, offers []routeOffer) *protocol.ChartSnap {
	cs := &protocol.ChartSnap{Kind: kind}
	for _, o := range offers {
		rs := protocol.RouteSnap{
			Choice: o.choice, Floor: globalFloor(o.node.Row, 1),
			Portals: o.portals,
		}
		if b := delveBiome(in.runSeed, o.node); b != nil {
			rs.Biome = b.ID
		}
		for _, m := range o.mods {
			rs.Mods = append(rs.Mods, protocol.FloorModSnap{Name: m.Name, Reward: m.Reward})
		}
		cs.Routes = append(cs.Routes, rs)
	}
	return cs
}
