// The descent — the host-layer run loop (DESIGN.md §14: transfers happen at
// the host layer between ticks; the sim never participates). A run owns a
// seed; the seed owns a delve chart (delvemap.go) — a lattice of nodes,
// each a 3-floor dungeon of one biome ending in a set-piece fight. Floor
// swaps extract every client to character state, build the new world,
// inject, and re-welcome — the same full-reset machinery a reconnect would
// use.
//
// Run rules: a run starts in the hideout (floor 0, a small safe world
// derived from the instance seed); its portal leads to the chart's entry
// node. Inside a node, stairs walk its three floors; the third holds the
// set-piece, and killing it CLEARS the node — the stairs then open the map,
// and you travel to any neighbor of cleared ground (down, sideways to hold
// a depth, or back up), or to anywhere you've already been. The portal is
// the death anchor — it lands on a floor's spawn the first time you step
// through and can be re-planted wherever you stand. Death costs XP (never
// below the current level's floor) and ejects everyone to the portal,
// consuming one portal use; a death with none left ends the run (depth was
// the score; a new run starts back home on a fresh seed and a fresh chart —
// the character survives). Entering the planted portal travels to the
// hideout for one use; stepping back through is free. Numbers (penalty,
// pack scaling, portal budget) are open for tuning.
package server

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/combat"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

var (
	// descendRange / portalRange: how close an actor must stand to use the
	// stairs or the portal, center to center (stairs sit on a tile center).
	descendRange = fm.FromInt(2)
	portalRange  = fm.FromInt(2)
)

// routeWant is one client's harvested deep-start pick for a tick.
type routeWant struct {
	c      *client
	choice int
}

// travelWant is one client's harvested delve-chart travel pick.
type travelWant struct {
	c  *client
	to nodeAddr
}

// runWants bundles one tick's harvested run verbs for runTick.
type runWants struct {
	descends []*client
	routes   []routeWant
	travels  []travelWant
	delves   []*client
	portals  []*client
	plants   []*client
}

// deathXPPenaltyDiv: death costs 1/5 of the current level's XP requirement,
// clamped so a level's progress never goes negative (no de-leveling).
const deathXPPenaltyDiv = 5

// The node set-pieces: every node's third floor ends in one. Regular nodes
// stake a rare Bone Colossus; every bossRows-th row alternates the Barrow
// King and the Ashen Warden; every apexRows-th row stakes the Grave Tyrant.
const (
	guardianDef = "bone_colossus"
	bossDef     = "barrow_king"
	bossDef2    = "ashen_warden" // alternates with the King on boss rows
	bossRows    = 3
	apexDef     = "grave_tyrant"
	apexRows    = 10
	// nodeFloors: every node is this many floors deep; the last holds the
	// set-piece. globalFloor/nodeRowOf/finOf in delvemap.go assume 3.
	nodeFloors = 3
)

// setPieceFor names the set-piece def guarding a node row's last floor.
func setPieceFor(row int) string {
	switch {
	case row%apexRows == 0:
		return apexDef
	case row%bossRows == 0:
		if (row/bossRows)%2 == 1 {
			return bossDef
		}
		return bossDef2
	default:
		return guardianDef
	}
}

// isSetPiece: is this def id one of the node-clearing set-pieces?
func isSetPiece(def string) bool {
	return def == guardianDef || def == bossDef || def == bossDef2 || def == apexDef
}

// deriveSeed mixes a salt into a base seed (splitmix finalizer), so run
// seeds derive from the config seed and floor seeds derive from the run
// seed — whole descents are replayable floor by floor.
func deriveSeed(base, salt uint64) uint64 {
	st := base ^ (salt * 0x9E3779B97F4A7C15)
	return core.SplitMix64(&st)
}

// farthestWalkable is where the stairs stand: the walkable tile farthest
// from the spawn room, so a floor is crossed before it is left. Walkable
// tiles are reachable by construction (mapgen prunes the rest), and the
// scan order is row-major — deterministic for a given grid.
func farthestWalkable(g *space.Grid) space.Vec2 {
	best, bd := g.Spawn, fm.Fixed(-1)
	for _, p := range g.WalkableCenters() {
		if d := space.Dist(p, g.Spawn); d > bd {
			best, bd = p, d
		}
	}
	return best
}

// floorSalt mixes a node address and floor-within-node into one world-seed
// salt (its own namespace next to delvemap.go's).
func floorSalt(n nodeAddr, fin int) uint64 {
	return 0xF10012_0000_0000 ^ uint64(uint32(n.Row))<<16 ^ uint64(uint32(n.Col))<<8 ^ uint64(uint32(fin))
}

// buildFloor constructs floor fin (1..nodeFloors) of a delve node: terrain
// from the address seed carved by the node's biome, the scenario's fixed
// spawns, the biome's scatter packs leveled and thickened by global depth,
// the node's modifiers applied to every monster, and — on the last floor —
// the row's set-piece parked on the stairs. Pure in (runSeed, node, fin) —
// a death-eject rebuild is byte-identical.
func (in *Instance) buildFloor(n nodeAddr, fin int) (*sim.Sim, error) {
	floor := globalFloor(n.Row, fin)
	s := sim.New(in.db, deriveSeed(in.runSeed, floorSalt(n, fin)))
	b := delveBiome(in.runSeed, n)
	kind := ""
	if b != nil {
		kind = b.MapKind
	}
	s.GenerateMap(space.MapSpec{
		Width: in.cfg.Map.Width, Height: in.cfg.Map.Height, Rooms: in.cfg.Map.Rooms,
		Kind: kind,
	})
	for _, sp := range in.cfg.Spawns {
		if _, err := s.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y))); err != nil {
			return nil, fmt.Errorf("server: node %v floor %d spawn: %w", n, fin, err)
		}
	}
	mods := delveNodeMods(in.runSeed, n)
	// Rarity pressure grows with depth (numbers open for tuning): magic
	// 10% +2%/floor capped at 30%, rare 2% +1%/floor capped at 12% — plus
	// whatever juice the node mods promise.
	magicPm := min(uint64(100+20*(floor-1)), 300)
	rarePm := min(uint64(20+10*(floor-1)), 120)
	packsPct := 0
	var monMods []string
	for _, m := range mods {
		magicPm += m.RarityPm
		rarePm += m.RarityPm / 2
		packsPct += m.PacksPct
		if m.MonMod != "" {
			monMods = append(monMods, m.MonMod)
		}
	}
	scatter := in.cfg.Scatter
	if b != nil && b.Scatter != nil {
		scatter = b.Scatter
	}
	for _, sc := range scatter {
		count := sc.Count + floor - 1
		count += count * packsPct / 100
		if err := s.ScatterSpawnPack(sc.Def, count, floor, magicPm, rarePm); err != nil {
			return nil, fmt.Errorf("server: node %v floor %d scatter: %w", n, fin, err)
		}
	}
	// The set-piece guards the node's last floor: kill it to clear the node
	// and open the map. It spawns hot and leashes tight — sneaking to the
	// stairs only buys the retreat routes.
	if fin == nodeFloors {
		def := setPieceFor(n.Row)
		lvl := floor + 2
		if def == apexDef {
			lvl = floor + 3
		}
		if _, err := s.SpawnRareLeveled(def, farthestWalkable(s.W.Grid), lvl); err != nil {
			return nil, fmt.Errorf("server: node %v set-piece: %w", n, err)
		}
	}
	if err := s.ApplyFloorMods(monMods); err != nil {
		return nil, fmt.Errorf("server: node %v mods: %w", n, err)
	}
	return s, nil
}

// hideoutSalt mixes the hideout world off the instance seed. Not a pinned
// constant: a fresh character's starting gem draft rolls from the hideout's
// loot stream at spawn, so a fixed hideout seed would deal every new exile
// the same three choices. The one-room layout barely varies anyway.
const hideoutSalt uint64 = 0xCA5A

// buildHideout constructs the hideout: one small safe room, no monsters.
func (in *Instance) buildHideout() *sim.Sim {
	s := sim.New(in.db, deriveSeed(in.cfg.Seed, hideoutSalt))
	s.GenerateMap(space.MapSpec{Width: 16, Height: 12, Rooms: 1})
	return s
}

// resetChart clears the run's chart bookkeeping (visited/cleared nodes).
func (in *Instance) resetChart() {
	in.visited = make(map[nodeAddr]bool)
	in.cleared = make(map[nodeAddr]bool)
	in.maxRow = 1
}

// markVisited records entering a node (and stretches the chart's revealed
// depth).
func (in *Instance) markVisited(n nodeAddr) {
	in.visited[n] = true
	if n.Row > in.maxRow {
		in.maxRow = n.Row
	}
}

// startRunWorld builds the current run's first world per cfg.StartFloor:
// the hideout by default (the chart's entry node waits behind its portal),
// or directly on a global floor for tests and dev servers (mapped onto the
// chart's trunk node at that depth). Boot-time only — swaps use
// startNextRun, which carries the clients along.
func (in *Instance) startRunWorld() error {
	in.portalsLeft = in.cfg.Portals
	in.resetChart()
	if in.cfg.StartFloor <= 0 {
		in.sim = in.buildHideout()
		in.node, in.fin, in.floor = nodeAddr{}, 0, 0
		in.portalNode, in.portalFin = trunkNodeAt(in.runSeed, 1), 1
		in.portalPlaced = false
	} else {
		n := trunkNodeAt(in.runSeed, nodeRowOf(in.cfg.StartFloor))
		fin := finOf(in.cfg.StartFloor)
		s, err := in.buildFloor(n, fin)
		if err != nil {
			return err
		}
		in.sim = s
		in.node, in.fin, in.floor = n, fin, in.cfg.StartFloor
		in.markVisited(n)
		in.stairs = farthestWalkable(s.W.Grid)
		in.portalNode, in.portalFin = n, fin
		in.portalPos, in.portalPlaced = s.W.Grid.Spawn, true
		if in.floor > in.best {
			in.best = in.floor
		}
	}
	in.mapSnap = in.sim.EncodeMap()
	return nil
}

// beginRun initializes run bookkeeping over the current world, which must
// be a floor with terrain installed — the legacy-save resume path (the
// saved world stands in for the entry node's first floor).
func (in *Instance) beginRun() {
	g := in.sim.W.Grid
	in.resetChart()
	in.node, in.fin, in.floor = trunkNodeAt(in.runSeed, 1), 1, 1
	in.markVisited(in.node)
	if in.best < 1 {
		in.best = 1
	}
	in.stairs = farthestWalkable(g)
	in.portalNode, in.portalFin = in.node, 1
	in.portalPos, in.portalPlaced = g.Spawn, true
	in.portalsLeft = in.cfg.Portals
}

// startNextRun begins run in.run+1 on a fresh derived seed (a fresh chart)
// at the configured start floor — back home by default — carrying every
// client through the swap. Hideout arrivals need no grace (nothing lives
// there); floor arrivals get it, same as a death eject.
func (in *Instance) startNextRun() {
	in.run++
	in.runSeed = deriveSeed(in.cfg.Seed, uint64(in.run))
	in.portalsLeft = in.cfg.Portals
	in.resetChart()
	if in.cfg.StartFloor <= 0 {
		s := in.buildHideout()
		in.portalNode, in.portalFin = trunkNodeAt(in.runSeed, 1), 1
		in.portalPlaced = false
		in.node, in.fin = nodeAddr{}, 0
		in.swapWorld(s, 0, s.W.Grid.Spawn)
		return
	}
	n := trunkNodeAt(in.runSeed, nodeRowOf(in.cfg.StartFloor))
	fin := finOf(in.cfg.StartFloor)
	s, err := in.buildFloor(n, fin)
	if err != nil {
		log.Printf("server: new run: %v", err)
		return
	}
	in.node, in.fin = n, fin
	in.markVisited(n)
	in.portalNode, in.portalFin = n, fin
	in.portalPos, in.portalPlaced = s.W.Grid.Spawn, true
	in.swapWorld(s, in.cfg.StartFloor, s.W.Grid.Spawn)
	in.grantGrace()
}

// runTick drives the descent between steps: deaths eject through the portal
// (or end the run), stairs walk a node's floors or open the chart, travel
// picks and portal travel swap the world, plants move the portal anchor. At
// most one world swap happens per tick; whichever fires first wins and the
// rest of this tick's requests are dropped (their validation context is
// gone with the old world).
func (in *Instance) runTick(fresh []protocol.EventSnap, w runWants) {
	if in.run == 0 {
		return
	}
	// Recap evidence: bank hits against client actors before anything can
	// swap the world out from under the attacker's name.
	for _, ev := range fresh {
		if ev.Kind != "hit" {
			continue
		}
		for _, c := range in.clients {
			if uint64(c.actor) != ev.Other {
				continue
			}
			from := "something"
			if a := in.sim.W.ActorByID(core.EntityID(ev.Actor)); a != nil {
				from = a.Def.Name
			}
			c.recentHits = append(c.recentHits, protocol.RecapHit{From: from, Note: ev.Note, Amount: ev.Amount})
			if len(c.recentHits) > recapHitCap {
				c.recentHits = c.recentHits[1:]
			}
		}
	}
	var dead []*client
	for _, ev := range fresh {
		if ev.Kind != "death" {
			continue
		}
		for _, c := range in.clients {
			if uint64(c.actor) == ev.Actor {
				dead = append(dead, c)
			}
		}
		// A fallen set-piece clears the node: the map opens at the stairs,
		// the floor marks an account checkpoint for every named player
		// present (deep starts for the whole roster), and the kill proves
		// whatever feats it implies.
		if isSetPiece(ev.Note) && in.fin == nodeFloors && !in.cleared[in.node] {
			in.cleared[in.node] = true
			if in.floor > 1 {
				for _, c := range in.clients {
					if c.token != "" {
						in.ids.AddCheckpoint(c.token, in.floor)
					}
				}
				in.syntheticEvent("checkpoint", int64(in.floor)*1000, "")
			}
			in.syntheticEvent("cleared", int64(in.node.Row)*1000, "")
			in.killFeats(ev.Note)
			in.broadcastRun()
			in.broadcastDelve()
		} else if isSetPiece(ev.Note) {
			in.killFeats(ev.Note)
		}
	}
	swapped := false
	if len(dead) > 0 {
		in.handleDeaths(dead)
		swapped = true
	}
	if !swapped && in.floor > 0 {
		for _, c := range w.descends {
			a := in.sim.W.ActorByID(c.actor)
			if a == nil || a.Dead || space.Dist(a.Pos, in.stairs) > descendRange {
				continue
			}
			if in.fin < nodeFloors {
				// Mid-node stairs: straight down to the node's next floor.
				in.descendWithin()
				swapped = true
				break
			}
			// The node's last floor: the stairs open the chart in travel
			// mode. An uncleared node's frontier isn't travelable (CanGo
			// says so per node) — the set-piece bars new ground, but the
			// retreat routes to visited nodes work.
			frame, _ := json.Marshal(protocol.ServerMsg{Type: "delve", Delve: in.delveSnap("travel")})
			if !c.send(frame, false) {
				c.tr.Close()
			}
		}
	}
	if !swapped && in.floor > 0 {
		for _, t := range w.travels {
			a := in.sim.W.ActorByID(t.c.actor)
			if a == nil || a.Dead || in.fin != nodeFloors ||
				space.Dist(a.Pos, in.stairs) > descendRange {
				continue
			}
			if !in.canTravelTo(t.to) {
				continue
			}
			in.travelTo(t.to)
			swapped = true
			break
		}
	}
	if !swapped && in.floor == 0 {
		// Route picks at the hideout portal: the deep-start chart's answer.
		for _, wr := range w.routes {
			a := in.sim.W.ActorByID(wr.c.actor)
			if a == nil || a.Dead || in.portalPlaced || wr.c.token == "" ||
				space.Dist(a.Pos, in.sim.W.Grid.Spawn) > portalRange {
				continue
			}
			offers := in.portalStartOffers(wr.c, a)
			if wr.choice < 0 || wr.choice >= len(offers) {
				continue
			}
			in.startRunAt(offers[wr.choice])
			swapped = true
			break
		}
	}
	if !swapped {
		for _, c := range w.portals {
			if in.portalTravel(c) {
				swapped = true
				break
			}
		}
	}
	if !swapped && in.floor > 0 {
		for _, c := range w.plants {
			a := in.sim.W.ActorByID(c.actor)
			if a == nil || a.Dead {
				continue
			}
			in.portalNode, in.portalFin = in.node, in.fin
			in.portalPos, in.portalPlaced = a.Pos, true
			in.syntheticEvent("portal", int64(in.floor)*1000, "planted")
			in.broadcastRun()
		}
	}
	// The map panel's refresh requests — reads, answered any time.
	for _, c := range w.delves {
		if in.floor <= 0 {
			continue
		}
		frame, _ := json.Marshal(protocol.ServerMsg{Type: "delve", Delve: in.delveSnap("")})
		if !c.send(frame, false) {
			c.tr.Close()
		}
	}
	// Keep the freshest character copy per client: death compacts the actor
	// away inside Step, so eject/respawn works from this (at most one tick
	// stale — the death tick itself).
	for _, c := range in.clients {
		if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
			c.lastChar, c.hasChar = core.ExtractCharacter(a), true
		}
	}
}

// canTravelTo: is a node a legal travel target right now? Anywhere the run
// has been, or any neighbor of cleared ground — the frontier.
func (in *Instance) canTravelTo(n nodeAddr) bool {
	if !nodeExists(in.runSeed, n) || (n == in.node) {
		return false
	}
	if in.visited[n] {
		return true
	}
	for _, nb := range delveNeighbors(in.runSeed, n) {
		if in.cleared[nb] {
			return true
		}
	}
	return false
}

// handleDeaths applies the run's death rules for client actors that died
// this tick: XP penalty, then eject everyone to the portal — or, with no
// portal uses left, end the run and start the next one.
func (in *Instance) handleDeaths(dead []*client) {
	for _, c := range dead {
		if !c.hasChar {
			continue
		}
		// The recap first, while the floor that killed them still stands.
		frame, _ := json.Marshal(protocol.ServerMsg{Type: "recap", Recap: in.recapFor(c)})
		if !c.send(frame, false) {
			c.tr.Close()
		}
		// A hardcore death is the character's last: memorial row on the
		// account, slot and name gone (store first, so the dying session's
		// flushes land nowhere), a farewell frame, and a short doom fuse so
		// both frames flush before the socket closes. The client lands on
		// the character select — one row shorter.
		if c.hardcore && c.token != "" {
			if fallen, ok := in.ids.FellInBattle(c.token, c.lastChar.Level, in.floor); ok {
				bye, _ := json.Marshal(protocol.ServerMsg{
					Type:  "error",
					Error: fmt.Sprintf("%s has fallen on floor %d. Hardcore is forever.", fallen, in.floor),
				})
				c.send(bye, false)
				c.doom = 10 // ~1/3s: enough for the writer to drain
				in.syntheticEvent("memorial", int64(in.floor)*1000, fallen)
			}
			continue
		}
		pen := progress.XPToNext(c.lastChar.Level) / deathXPPenaltyDiv
		if pen > c.lastChar.XP {
			pen = c.lastChar.XP
		}
		c.lastChar.XP -= pen
		// The zone projection died; the character respawns refilled.
		c.lastChar.Life, c.lastChar.Mana, c.lastChar.ES = 0, 0, 0
	}
	if in.portalsLeft > 0 {
		in.portalsLeft--
		s, err := in.buildFloor(in.portalNode, in.portalFin)
		if err != nil {
			log.Printf("server: death eject: %v", err)
			return
		}
		if !in.portalPlaced { // defensive: deaths shouldn't precede placement
			in.portalPos, in.portalPlaced = s.W.Grid.Spawn, true
		}
		in.node, in.fin = in.portalNode, in.portalFin
		in.markVisited(in.node)
		in.swapWorld(s, globalFloor(in.portalNode.Row, in.portalFin), in.portalPos)
		in.grantGrace()
		in.syntheticEvent("death_eject", int64(in.portalsLeft)*1000, "")
		return
	}
	depth := in.floor
	in.startNextRun()
	in.syntheticEvent("run_over", int64(depth)*1000, "")
}

// grantGrace shields every client actor with the portal-grace buff — death
// arrivals only (eject, run over), never voluntary travel. Runs between
// ticks right after a swap: ApplyBuff consumes no RNG and queues nothing,
// and the swallowed EvBuff event doesn't matter — the synthetic run event
// narrates the arrival and the buff ring shows on the wire regardless.
func (in *Instance) grantGrace() {
	def := in.db.Buffs["portal_grace"]
	if def == nil {
		return
	}
	for _, c := range in.clients {
		if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
			combat.ApplyBuff(in.sim.W, a, def, a.ID)
		}
	}
	in.surgery = true // replay: buffs granted outside Step
}

// descendWithin takes the mid-node stairs one floor down inside the current
// node.
func (in *Instance) descendWithin() {
	fin := in.fin + 1
	s, err := in.buildFloor(in.node, fin)
	if err != nil {
		log.Printf("server: descend: %v", err)
		return
	}
	in.fin = fin
	in.swapWorld(s, globalFloor(in.node.Row, fin), s.W.Grid.Spawn)
	in.syntheticEvent("descend", int64(in.floor-1)*1000, "")
}

// descend walks one global floor down the trunk: the mid-node stairs, or —
// from a node's last floor — entry into the next row's trunk node, skipping
// the clear gate. The shorthand tests and tools reach for; live travel
// goes through runTick's validated travel picks.
func (in *Instance) descend() {
	if in.fin < nodeFloors {
		in.descendWithin()
		return
	}
	in.travelTo(trunkNodeAt(in.runSeed, in.node.Row+1))
}

// travelTo swaps the instance to a chart node's first floor — the pick made
// on the open map at a cleared node's stairs.
func (in *Instance) travelTo(n nodeAddr) {
	s, err := in.buildFloor(n, 1)
	if err != nil {
		log.Printf("server: travel: %v", err)
		return
	}
	in.node, in.fin = n, 1
	in.markVisited(n)
	in.swapWorld(s, globalFloor(n.Row, 1), s.W.Grid.Spawn)
	b := delveBiome(in.runSeed, n)
	note := ""
	if b != nil {
		note = b.ID
	}
	in.syntheticEvent("travel", int64(n.Row)*1000, note)
}

// portalStartOffers is the deep-start chart for a fresh run: from the top
// with the full portal budget, or any earned checkpoint the character's
// level covers — one portal lighter, so "from the top" stays a choice.
// Checkpoints are stored as global floors (a cleared node's last floor);
// each maps onto the fresh chart's trunk node at that row.
func (in *Instance) portalStartOffers(c *client, a *core.Actor) []routeOffer {
	entry := trunkNodeAt(in.runSeed, 1)
	offers := []routeOffer{{
		choice: 0, node: entry, portals: in.cfg.Portals,
		mods: delveNodeMods(in.runSeed, entry),
	}}
	deep := max(1, in.cfg.Portals-1)
	for _, cp := range in.ids.Checkpoints(c.token) {
		row := nodeRowOf(cp)
		if row <= 1 || a.Level < cp {
			continue
		}
		n := trunkNodeAt(in.runSeed, row)
		offers = append(offers, routeOffer{
			choice: len(offers), node: n, portals: deep,
			mods: delveNodeMods(in.runSeed, n),
		})
	}
	return offers
}

// startRunAt begins the fresh run's descent at a deep-start offer: the
// portal anchors on the chosen node's first floor and the portal budget is
// the offer's — the depth was bought with it.
func (in *Instance) startRunAt(o routeOffer) {
	s, err := in.buildFloor(o.node, 1)
	if err != nil {
		log.Printf("server: deep start: %v", err)
		return
	}
	in.portalsLeft = o.portals
	in.node, in.fin = o.node, 1
	in.markVisited(o.node)
	in.portalNode, in.portalFin = o.node, 1
	in.portalPos, in.portalPlaced = s.W.Grid.Spawn, true
	in.swapWorld(s, globalFloor(o.node.Row, 1), in.portalPos)
	in.syntheticEvent("portal", int64(in.floor)*1000, "checkpoint")
}

// portalTravel handles one client's enter_portal request. From a dungeon
// floor, standing at the planted portal travels to the hideout and consumes
// a portal use; from the hideout, standing at its portal returns to the
// anchor floor for free. Reports whether a world swap happened.
func (in *Instance) portalTravel(c *client) bool {
	a := in.sim.W.ActorByID(c.actor)
	if a == nil || a.Dead {
		return false
	}
	if in.floor == 0 {
		if space.Dist(a.Pos, in.sim.W.Grid.Spawn) > portalRange {
			return false
		}
		// A fresh run's first descent offers the account's earned deep
		// starts (level-gated); the pick comes back as a "route" verb.
		if !in.portalPlaced && c.token != "" {
			if offers := in.portalStartOffers(c, a); len(offers) > 1 {
				frame, _ := json.Marshal(protocol.ServerMsg{
					Type: "chart", Chart: in.chartSnapKind("portal", offers),
				})
				if !c.send(frame, false) {
					c.tr.Close()
				}
				return false
			}
		}
		s, err := in.buildFloor(in.portalNode, in.portalFin)
		if err != nil {
			log.Printf("server: portal return: %v", err)
			return false
		}
		// First trip of the run: the anchor lands on the floor's spawn.
		if !in.portalPlaced {
			in.portalPos, in.portalPlaced = s.W.Grid.Spawn, true
		}
		in.node, in.fin = in.portalNode, in.portalFin
		in.markVisited(in.node)
		in.swapWorld(s, globalFloor(in.portalNode.Row, in.portalFin), in.portalPos)
		in.syntheticEvent("portal", int64(in.floor)*1000, "return")
		return true
	}
	if in.node != in.portalNode || in.fin != in.portalFin ||
		space.Dist(a.Pos, in.portalPos) > portalRange {
		return false
	}
	if in.portalsLeft == 0 {
		in.syntheticEvent("portal", 0, "exhausted")
		return false
	}
	in.portalsLeft--
	s := in.buildHideout()
	in.node, in.fin = nodeAddr{}, 0
	in.swapWorld(s, 0, s.W.Grid.Spawn)
	in.syntheticEvent("portal", int64(in.portalsLeft)*1000, "hideout")
	return true
}

// swapWorld is the transfer itself: reduce every client to character state,
// install the new world (floor 0 = hideout), inject everyone at pos (fanned
// out; injection clamps to walkable ground), and re-welcome each client —
// which resets their delta encoder and bumps their welcome generation.
func (in *Instance) swapWorld(s *sim.Sim, floor int, at space.Vec2) {
	for _, c := range in.clients {
		if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
			c.lastChar, c.hasChar = core.ExtractCharacter(a), true
		}
	}
	in.sim = s
	in.floor = floor
	in.mapSnap = s.EncodeMap()
	in.surgery = true // replay: the world changed outside Step
	if floor > in.best {
		in.best = floor
	}
	if floor > 0 {
		in.stairs = farthestWalkable(s.W.Grid)
		// Ladder bookkeeping: reaching a floor records it (and the build
		// that reached it) as the character's best on the account store.
		for _, c := range in.clients {
			if c.token != "" && c.hasChar {
				in.ids.RecordBest(c.token, floor, buildSnapOf(in.db, &c.lastChar))
			}
		}
		in.depthFeats(floor)
	}
	for _, c := range in.clients {
		c.recentHits = nil // a new world's dangers start their own story
	}
	// Old-world events still buffered reference entities that no longer
	// exist; the synthetic run events narrate the transition instead.
	in.eventBuf = nil
	ids := make([]core.EntityID, len(in.clients))
	for i, c := range in.clients {
		pos := at.Add(space.V(fm.FromInt(int64(i%4)), 0))
		if c.hasChar {
			if a, err := core.InjectCharacter(s.W, c.lastChar, pos); err == nil {
				ids[i] = a.ID
			} else {
				log.Printf("server: inject character: %v", err)
			}
		}
		if ids[i] == 0 {
			ids[i], _ = s.Spawn(in.cfg.PlayerDef, pos) // no character yet: fresh spawn
		}
	}
	// Cut every client over: actor IDs, welcome generations, ack state, and
	// the pending queue flip before any new command lands. Commands readLoop
	// tagged with old-world IDs are either in the queue we drop here or
	// arrive after and get the new IDs — nothing ever drives whichever
	// actor wears an old ID now.
	for i, c := range in.clients {
		c.mu.Lock()
		c.actor = ids[i]
		c.gen++
		c.ack, c.ackDirty = 0, false
		c.mu.Unlock()
	}
	in.mu.Lock()
	in.pending = nil
	in.mu.Unlock()
	for _, c := range in.clients {
		c.baseline, c.sent, c.sentTicks = nil, nil, nil
		if !c.send(in.welcomeFrame(c), false) {
			c.tr.Close()
		}
	}
}

// runSnap is the wire form of the run state. Floor 0 is the hideout; the
// portal position is included when the portal stands on the current world.
func (in *Instance) runSnap() *protocol.RunSnap {
	rs := &protocol.RunSnap{Floor: in.floor, Portals: in.portalsLeft, Run: in.run, Best: in.best}
	if in.floor > 0 {
		rs.Row, rs.Col, rs.Fin = in.node.Row, in.node.Col, in.fin
		rs.Cleared = in.cleared[in.node]
		if b := delveBiome(in.runSeed, in.node); b != nil {
			rs.Biome = b.ID
		}
		for _, m := range delveNodeMods(in.runSeed, in.node) {
			rs.Mods = append(rs.Mods, m.Name)
		}
	}
	var pp *space.Vec2
	if in.floor == 0 {
		p := in.sim.W.Grid.Spawn
		pp = &p
	} else if in.node == in.portalNode && in.fin == in.portalFin {
		pp = &in.portalPos
	}
	if pp != nil {
		rs.Portal = &protocol.Vec{X: pp.X.Milli(), Y: pp.Y.Milli()}
	}
	return rs
}

// broadcastRun announces a run-state change that comes without a new world
// (portal planted, node cleared). Swaps don't need it — their welcomes
// carry the run.
func (in *Instance) broadcastRun() {
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "run", Run: in.runSnap()})
	for _, c := range in.clients {
		if !c.send(frame, false) {
			c.tr.Close()
		}
	}
}

// broadcastDelve pushes a fresh chart to everyone — after a clear, so open
// map panels see the frontier grow without re-asking. Info kind: the map
// never pops open mid-fight; the stairs answer travel mode.
func (in *Instance) broadcastDelve() {
	if in.floor <= 0 {
		return
	}
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "delve", Delve: in.delveSnap("")})
	for _, c := range in.clients {
		if !c.send(frame, false) {
			c.tr.Close()
		}
	}
}

// syntheticEvent queues a host-layer wire event. Participant-less, so
// interest filtering treats it as global — run flow (descents, portal
// travel, deaths) is narrated this way, since the sim doesn't know runs
// exist.
func (in *Instance) syntheticEvent(kind string, amount int64, note string) {
	in.eventBuf = append(in.eventBuf, protocol.EventSnap{Kind: kind, Amount: amount, Note: note})
}
