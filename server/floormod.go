// Floor modifiers + the descent chart (ROADMAP v2 Track 2 item 3).
// Floors roll named, depth-scaled modifiers, and you read them BEFORE you
// commit: the stairs answer with a chart of routes down — each exit with
// visible mods — plus a side chamber that holds your depth with stacked
// mods and juiced rewards. Farming a comfortable depth is a strategy.
//
// Everything here is a pure function of (run seed, floor, route, chamber),
// rolled host-side off a splitmix — a death-eject rebuild of the same
// floor re-rolls identically, and the chart can be offered without
// touching any world RNG stream.
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

// floorModTable is ordered — rollFloorMods indexes into it, and floor
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

// modCountForFloor: how many modifiers a floor rolls — the depth scaling.
// The first floors stay clean so the early game reads as it always did.
func modCountForFloor(floor int) int {
	switch {
	case floor < 4:
		return 0
	case floor < 10:
		return 1
	case floor < 20:
		return 2
	default:
		return 3
	}
}

// modCountAt is the one formula both the chart preview and the floor
// build use: depth scaling, +1 per side chamber taken at this depth (the
// stack), +1 on route 1 (the greedy exit). Diverging these would let the
// chart lie about the floor it sells.
func modCountAt(floor, route, chamber int) int {
	n := modCountForFloor(floor) + chamber
	if route == 1 {
		n++
	}
	return n
}

// floorModSalt mixes a route address into one derivation salt.
func floorModSalt(floor, route, chamber int) uint64 {
	return 0xF100D_0000_0000 ^ uint64(floor)<<20 ^ uint64(route)<<10 ^ uint64(chamber)
}

// rollFloorMods rolls count distinct mods for a floor address. Pure and
// host-side: same address, same mods, every time — eject rebuilds and
// chart previews agree by construction.
func rollFloorMods(runSeed uint64, floor, route, chamber, count int) []floorModDef {
	if count <= 0 {
		return nil
	}
	if count > 4 {
		count = 4
	}
	if count > len(floorModTable) {
		count = len(floorModTable)
	}
	st := deriveSeed(runSeed, floorModSalt(floor, route, chamber))
	idx := make([]int, len(floorModTable))
	for i := range idx {
		idx[i] = i
	}
	picks := make([]floorModDef, 0, count)
	for k := 0; k < count; k++ {
		j := k + int(core.SplitMix64(&st)%uint64(len(idx)-k))
		idx[k], idx[j] = idx[j], idx[k]
		picks = append(picks, floorModTable[idx[k]])
	}
	return picks
}

// routeOffer is one exit on the descent chart (stairs routes and hideout
// deep starts share the shape; portals is the deep-start budget, 0 on
// stairs offers where the budget doesn't change).
type routeOffer struct {
	choice  int
	floor   int // destination
	route   int // roll address of the destination
	chamber int
	side    bool
	portals int
	mods    []floorModDef
}

// chartOffers builds the current stairs' chart: two ways down (the second
// greedier by one mod), plus — once the current depth rolls mods at all —
// a side chamber that holds the depth with stacked mods. Pure; safe to
// call for previews.
func (in *Instance) chartOffers() []routeOffer {
	next := in.floor + 1
	offers := []routeOffer{
		{choice: 0, floor: next, route: 0,
			mods: rollFloorMods(in.runSeed, next, 0, 0, modCountAt(next, 0, 0))},
		{choice: 1, floor: next, route: 1,
			mods: rollFloorMods(in.runSeed, next, 1, 0, modCountAt(next, 1, 0))},
	}
	if modCountForFloor(in.floor) > 0 {
		ch := in.chamber + 1
		offers = append(offers, routeOffer{
			choice: 2, floor: in.floor, route: in.route, chamber: ch, side: true,
			mods: rollFloorMods(in.runSeed, in.floor, in.route, ch, modCountAt(in.floor, in.route, ch)),
		})
	}
	return offers
}

// chartSnap is the wire form of the stairs chart.
func chartSnap(offers []routeOffer) *protocol.ChartSnap {
	return chartSnapKind("", offers)
}

// chartSnapKind builds a chart frame body of a given kind ("" = stairs,
// "portal" = the hideout's deep-start chart).
func chartSnapKind(kind string, offers []routeOffer) *protocol.ChartSnap {
	cs := &protocol.ChartSnap{Kind: kind}
	for _, o := range offers {
		rs := protocol.RouteSnap{Choice: o.choice, Floor: o.floor, Side: o.side, Portals: o.portals}
		if b := biomeForFloor(o.floor); b != nil {
			rs.Biome = b.ID
		}
		for _, m := range o.mods {
			rs.Mods = append(rs.Mods, protocol.FloorModSnap{Name: m.Name, Reward: m.Reward})
		}
		cs.Routes = append(cs.Routes, rs)
	}
	return cs
}
