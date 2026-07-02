// Package server hosts a sim instance over two wires. TCP/NDJSON is the
// debug wire: protocol.Command lines in, protocol.ServerMsg lines out (a
// welcome, then full-world JSON snapshots). WebSocket is the real-client
// wire: a JSON welcome, then interest-culled binary delta view frames
// (protocol/binary.go) acked by the client; ?format=json downgrades a WS
// client to JSON views for debugging. The sim ticks at core.TicksPerSecond
// regardless of the send rate — views go out every SendEvery ticks with the
// skipped ticks' events accumulated.
//
// Concurrency model: the tick goroutine owns ALL world mutation — joins,
// leaves, commands, and Step happen there, at tick boundaries. Connection
// goroutines only decode lines and append to a mutex-guarded queue. This
// preserves the sim's single-goroutine invariant; the server scales by
// running more instances, not by threading one.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

const (
	// maxLineBytes bounds one client command line; snapshots go the other
	// way, so commands are small.
	maxLineBytes = 64 * 1024
	// writeTimeout is how long one client write may stall the tick loop.
	// Shortcut: no per-client send queues yet; a slow reader gets dropped.
	writeTimeout = time.Second
)

type Config struct {
	// Addr is the TCP/NDJSON listen address.
	Addr string
	// HTTPAddr, if set, serves the WebSocket endpoint at /ws and (with
	// StaticDir) the web client at /.
	HTTPAddr  string
	StaticDir string
	// AdminAddr, if set, serves the admin dashboard and JSON API (admin.go)
	// on its own port. No auth — bind it somewhere trusted (localhost or a
	// tailnet), never the open internet.
	AdminAddr string
	Seed      uint64
	// TickInterval defaults to one real tick (1s / core.TicksPerSecond).
	// Tests shrink it; the sim itself never reads the clock.
	TickInterval time.Duration
	// SendEvery is how many sim ticks pass between view sends (1 = every
	// tick; default 3 → 10Hz at the 30Hz sim rate). Events from the skipped
	// ticks accumulate into the next send.
	SendEvery int
	// InterestRadius culls each WS client's view to entities within this
	// range (milli-units) of its actor. 0 disables culling. The TCP wire is
	// debug-omniscient and ignores it.
	InterestRadius int64
	// Map, if set, generates rooms-and-corridors terrain before any spawns.
	Map *protocol.MapSpec
	// Spawns places the map's starting actors (monsters, dummies). On grid
	// worlds positions are clamped to the nearest walkable tile.
	Spawns []protocol.ScriptSpawn
	// Scatter places monsters on random walkable tiles (needs Map).
	Scatter []protocol.Scatter
	// PlayerDef is the actor def spawned per connecting client.
	PlayerDef string
	// Portals is the descent run's death budget: each death-ejection to the
	// portal consumes one; a death with none left ends the run. Applies only
	// to worlds with terrain (descent needs stairs).
	Portals int
	// Load, if set, restores the world from a World.Save file instead of
	// building one — Seed, Map, Spawns, and Scatter are ignored. Player-def
	// actors in the save are removed at load (live sessions don't survive a
	// restart; identities bank characters separately) with their gear
	// dropped at their feet, so a restart never deletes items.
	Load []byte
	// IdentityPath persists named players (identity.go). "" keeps
	// identities in memory only — they still work, but a restart forgets
	// everyone.
	IdentityPath string
}

type Instance struct {
	cfg Config
	db  *core.ContentDB
	sim *sim.Sim
	ids *IdentityStore
	// mapSnap is the terrain encoded once per world; rides every welcome.
	mapSnap *protocol.MapSnap

	// tickCount drives periodic host-layer work (character banking); it is
	// process time, not world time — world swaps don't reset it.
	tickCount uint64

	// Descent run state (descent.go), tick-goroutine-only. run == 0 means
	// no descent (open-plane worlds); everything else is meaningful only
	// when run > 0.
	run         int        // 1-based run counter; a run ends when the portals run out
	runSeed     uint64     // this run's seed; floor worlds derive from it
	floor       int        // current depth, 1-based
	best        int        // deepest floor reached this process — the score
	stairs      space.Vec2 // this floor's descent stairs (farthest walkable from spawn)
	portalFloor int        // where death ejects to
	portalPos   space.Vec2
	portalsLeft int

	mu       sync.Mutex
	pending  []core.Command
	joins    []*client
	leaves   []*client
	adminOps []adminOp

	clients   []*client
	joinCount int

	// eventBuf accumulates wire events between sends so a lower send rate
	// drops no events; sinceSend counts ticks toward the next send. Both are
	// tick-goroutine-only.
	eventBuf  []protocol.EventSnap
	sinceSend int

	// Pause: pauseDesired is what admin asked for (written only by adminOps,
	// which run on the tick goroutine); paused is the announced state. While
	// paused the world doesn't Step and player commands are discarded, but
	// the loop keeps ticking: joins/leaves land, views keep flowing (cheap
	// no-change deltas), and admin ops still drain.
	pauseDesired bool
	paused       bool

	// Admin telemetry, tick-goroutine-only (admin reads it via adminOps):
	// recent tick wall-times for the actual-rate gauge, recent wire events.
	tickTimes    []time.Time
	recentEvents []adminEvent

	listenerAddr chan net.Addr
}

// adminOp runs a closure on the tick goroutine, where touching the world and
// client list is safe, and reports back to the waiting admin HTTP handler.
type adminOp struct {
	fn    func() (any, error)
	reply chan adminReply
}

type adminReply struct {
	v   any
	err error
}

// mode is how a client's views travel.
type mode uint8

const (
	// modeBinary: delta-encoded binary view frames, interest-culled. The
	// real-client wire (WS).
	modeBinary mode = iota
	// modeJSONView: the same interest-culled view, full JSON every send —
	// debug mode for inspecting exactly what a client sees (/ws?format=json).
	modeJSONView
	// modeJSONWorld: full-world JSON — the omniscient TCP/nc debug wire.
	modeJSONWorld
)

type client struct {
	tr   transport
	mode mode

	// name/token identify a named player (identity.go); both empty for
	// guests. Set before the join is queued, then tick-goroutine-only;
	// removeClient clears token so a double leave can't double-disconnect.
	name  string
	token string

	actor core.EntityID
	// early buffers commands that arrive before the tick loop has spawned
	// this client's actor (a fast client races its own welcome); they flush
	// into the pending queue at spawn. Guarded by the instance mutex.
	early []core.Command
	wmu   sync.Mutex

	// gen is the welcome generation: 1 at join, +1 per re-welcome (floor
	// swap). readLoop drops acks whose gen doesn't match — they deltaed
	// against a world this client no longer sees. Written by the tick
	// goroutine under the instance mutex; readLoop reads it under the same.
	gen int
	// wantDescend/wantPlant/wantPortal buffer the transport-level run verbs
	// for the tick goroutine, like ack. Guarded by the instance mutex.
	wantDescend bool
	wantPlant   bool
	wantPortal  bool

	// lastChar is the freshest character extraction for this client's
	// actor, taken after every step — death compacts the actor away before
	// the host can see it, so eject/respawn works from this copy (at most
	// one tick stale). Tick-goroutine-only.
	lastChar core.Character
	hasChar  bool

	// ack is the latest view tick the client confirmed, recorded by readLoop
	// and consumed by the tick goroutine. Guarded by the instance mutex.
	ack      uint64
	ackDirty bool

	// Delta-encoder state, tick-goroutine-only. baseline is the last acked
	// view (nil → next send is a keyframe); sent holds unacked views the
	// client may still ack, oldest first in sentTicks.
	baseline  *protocol.Snapshot
	sent      map[uint64]*protocol.Snapshot
	sentTicks []uint64

	// bytesSent feeds the admin dashboard's bandwidth column. Sends happen
	// only on the tick goroutine, which is also where admin ops read it.
	bytesSent uint64
}

// maxUnackedViews bounds the per-client baseline candidates (~3s at the
// default 10Hz send rate). A client acking something older just gets a
// keyframe.
const maxUnackedViews = 32

func New(db *core.ContentDB, cfg Config) (*Instance, error) {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = time.Second / core.TicksPerSecond
	}
	if cfg.SendEvery <= 0 {
		cfg.SendEvery = 3
	}
	if cfg.PlayerDef == "" {
		cfg.PlayerDef = "player"
	}
	ids, err := NewIdentityStore(cfg.IdentityPath)
	if err != nil {
		return nil, err
	}
	in := &Instance{
		cfg:          cfg,
		db:           db,
		ids:          ids,
		listenerAddr: make(chan net.Addr, 1),
	}
	if cfg.Load != nil {
		world := cfg.Load
		rs, wrapped := decodeRunSave(cfg.Load)
		if wrapped {
			if rs.RunVersion != runSaveVersion {
				return nil, fmt.Errorf("server: run save version %d, this build reads %d", rs.RunVersion, runSaveVersion)
			}
			world = rs.World
		}
		s, err := sim.Load(db, world)
		if err != nil {
			return nil, fmt.Errorf("server: loading world: %w", err)
		}
		in.sim = s
		reclaimOrphanPlayers(s.W, cfg.PlayerDef)
		in.mapSnap = in.sim.EncodeMap()
		switch {
		case wrapped:
			// Resume the run exactly where the save left it — including a
			// hideout visit (floor 0: no stairs, portal anchored elsewhere).
			in.run, in.runSeed = rs.Run, rs.RunSeed
			in.floor, in.portalsLeft = rs.Floor, rs.PortalsLeft
			in.portalFloor, in.portalPos = rs.PortalFloor, rs.PortalPos
			in.best = rs.Best
			if in.floor > 0 && s.W.Grid != nil {
				in.stairs = farthestWalkable(s.W.Grid)
			}
		case s.W.Grid != nil:
			// Legacy bare-world save: resume as floor 1 of a fresh run.
			in.run = 1
			in.runSeed = deriveSeed(cfg.Seed, uint64(in.run))
			in.beginRun()
		}
		return in, nil
	}
	if cfg.Map != nil {
		// Descent world: the configured seed seeds the run; each floor's
		// world derives from (run seed, floor index) (DESIGN §14).
		in.run = 1
		in.runSeed = deriveSeed(cfg.Seed, uint64(in.run))
		s, err := in.buildFloor(1)
		if err != nil {
			return nil, err
		}
		in.sim = s
		in.mapSnap = s.EncodeMap()
		in.beginRun()
		return in, nil
	}
	in.sim = sim.New(db, cfg.Seed)
	for _, sp := range cfg.Spawns {
		if _, err := in.sim.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y))); err != nil {
			return nil, fmt.Errorf("server: scenario spawn: %w", err)
		}
	}
	for _, sc := range cfg.Scatter {
		if err := in.sim.ScatterSpawn(sc.Def, sc.Count); err != nil {
			return nil, fmt.Errorf("server: scatter spawn: %w", err)
		}
	}
	return in, nil
}

// reclaimOrphanPlayers removes saved player-def actors at load time. A fresh
// process has no session to hand them to (reconnect/session identity doesn't
// exist yet — RISKS.md), so they'd stand frozen forever; instead their gear
// drops where they stood and the actor goes away. Runs before any client or
// tick exists, so mutating the world directly is safe.
func reclaimOrphanPlayers(w *core.World, playerDef string) {
	for _, a := range w.Actors {
		if a.Def.ID != playerDef {
			continue
		}
		for slot, item := range a.Equipment {
			if item != nil {
				w.SpawnDrop(a.Pos, *item)
				a.Equipment[slot] = nil
			}
		}
		for _, item := range a.Inventory {
			w.SpawnDrop(a.Pos, item)
		}
		a.Inventory = nil
		a.Dead = true
	}
	w.EndTick() // compact the tombstones
}

// Addr returns the bound listen address once ListenAndServe is up — useful
// with ":0" in tests.
func (in *Instance) Addr() net.Addr { return <-in.listenerAddr }

// ListenAndServe accepts clients and runs the tick loop until ctx ends.
func (in *Instance) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", in.cfg.Addr)
	if err != nil {
		close(in.listenerAddr) // unblock Addr() waiters with nil
		return err
	}
	in.listenerAddr <- ln.Addr()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go in.acceptLoop(ctx, ln)
	if in.cfg.HTTPAddr != "" {
		go in.serveHTTP(ctx)
	}
	if in.cfg.AdminAddr != "" {
		go in.serveAdmin(ctx)
	}

	ticker := time.NewTicker(in.cfg.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, c := range in.clients {
				c.tr.Close()
			}
			return ctx.Err()
		case <-ticker.C:
			in.tick()
		}
	}
}

func (in *Instance) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		c := &client{tr: newTCPTransport(conn), mode: modeJSONWorld}
		in.mu.Lock()
		in.joins = append(in.joins, c)
		in.mu.Unlock()
		go in.readLoop(ctx, c)
	}
}

// readLoop decodes one client's command lines. The actor field is always
// overwritten with the client's assigned actor — clients command only
// themselves, whatever they claim.
func (in *Instance) readLoop(ctx context.Context, c *client) {
	defer func() {
		c.tr.Close()
		in.mu.Lock()
		in.leaves = append(in.leaves, c)
		in.mu.Unlock()
	}()
	for {
		frame, err := c.tr.ReadFrame()
		if err != nil || ctx.Err() != nil {
			return
		}
		var wc protocol.Command
		if err := json.Unmarshal(frame, &wc); err != nil {
			continue // garbage frame; the sim never sees it
		}
		switch wc.Kind {
		case "ack":
			// Transport-level: moves this client's delta baseline. ack 0 (or
			// any tick we no longer hold) resets to keyframes. An ack from a
			// previous welcome generation references a world this client no
			// longer sees — dropped.
			in.mu.Lock()
			if wc.Gen == c.gen {
				c.ack, c.ackDirty = wc.Tick, true
			}
			in.mu.Unlock()
			continue
		case "descend", "plant_portal", "enter_portal":
			// Run verbs are host-layer, like ack — the sim never sees them.
			in.mu.Lock()
			switch wc.Kind {
			case "descend":
				c.wantDescend = true
			case "plant_portal":
				c.wantPlant = true
			case "enter_portal":
				c.wantPortal = true
			}
			in.mu.Unlock()
			continue
		}
		cmd, err := sim.DecodeCommand(wc)
		if err != nil {
			continue
		}
		in.mu.Lock()
		if c.actor != 0 {
			cmd.Actor = c.actor
			in.pending = append(in.pending, cmd)
		} else {
			c.early = append(c.early, cmd)
		}
		in.mu.Unlock()
	}
}

// tick is the heart of the server: drain the queues, mutate the world,
// step, broadcast. Everything here runs on the one tick goroutine.
func (in *Instance) tick() {
	in.mu.Lock()
	joins := in.joins
	leaves := in.leaves
	cmds := in.pending
	ops := in.adminOps
	in.joins, in.leaves, in.pending, in.adminOps = nil, nil, nil, nil
	var descends, portals, plants []*client
	for _, c := range in.clients {
		if c.wantDescend {
			c.wantDescend = false
			descends = append(descends, c)
		}
		if c.wantPortal {
			c.wantPortal = false
			portals = append(portals, c)
		}
		if c.wantPlant {
			c.wantPlant = false
			plants = append(plants, c)
		}
	}
	in.mu.Unlock()

	// Admin ops run here, where world and client-list access is safe. They
	// drain even while paused — that's how resume arrives.
	for _, op := range ops {
		v, err := op.fn()
		op.reply <- adminReply{v, err}
	}
	if in.paused != in.pauseDesired {
		in.paused = in.pauseDesired
		in.sendPause(in.clients, in.paused)
	}

	in.tickTimes = append(in.tickTimes, time.Now())
	if len(in.tickTimes) > tickRateWindow {
		in.tickTimes = in.tickTimes[1:]
	}

	rosterChanged := false
	for _, c := range leaves {
		rosterChanged = in.removeClient(c) || rosterChanged
	}
	var welcomes []*client
	for _, c := range joins {
		if in.spawnClient(c) {
			welcomes = append(welcomes, c)
			rosterChanged = rosterChanged || c.name != ""
		}
	}

	if !in.paused {
		// Stable sort by actor: fair, and preserves each client's own command
		// order. Arrival interleaving across clients is network timing — the
		// server's ordering is the authoritative one.
		sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].Actor < cmds[j].Actor })
		in.sim.Step(cmds)

		// Harvest this step's events before any run logic: a floor swap
		// replaces the world, and the old world's last events (the death
		// that triggered the eject, say) must already be banked. The sim
		// runs every tick; views go out every SendEvery ticks, carrying the
		// events of the whole window.
		fresh := in.sim.EncodeEvents()
		in.eventBuf = append(in.eventBuf, fresh...)
		for _, ev := range fresh {
			in.recentEvents = append(in.recentEvents, adminEvent{Tick: in.sim.W.Tick, EventSnap: ev})
		}
		if len(in.recentEvents) > adminEventCap {
			in.recentEvents = in.recentEvents[len(in.recentEvents)-adminEventCap:]
		}

		// The descent: deaths, stairs, portals — may swap the world and
		// re-welcome everyone (descent.go).
		in.runTick(fresh, descends, portals, plants)
	}
	// (Paused: cmds are dropped, not queued — a long pause must not release
	// a flood of stale intent on resume.)

	for _, c := range welcomes {
		c.send(in.welcomeFrame(c), false)
	}
	if in.paused && len(welcomes) > 0 {
		in.sendPause(welcomes, true) // joined mid-pause; tell them why nothing moves
	}
	if rosterChanged {
		// Welcomes already carry the roster; this catches everyone else.
		in.broadcastRoster()
	}

	// Character banking: periodically copy every named client's live
	// character into the store so a crash loses minutes, not sessions.
	// SaveIfDue then debounces the actual disk write.
	in.tickCount++
	if in.tickCount%(30*core.TicksPerSecond) == 0 {
		for _, c := range in.clients {
			if c.token == "" {
				continue
			}
			if a := in.sim.W.ActorByID(c.actor); a != nil && !a.Dead {
				ch := core.ExtractCharacter(a)
				in.ids.Bank(c.token, &ch)
			}
		}
	}
	in.ids.SaveIfDue()

	in.sinceSend++
	if in.sinceSend < in.cfg.SendEvery {
		return
	}
	in.sinceSend = 0
	events := in.eventBuf
	in.eventBuf = nil

	for _, c := range in.clients {
		frame, binary := in.frameFor(c, events)
		if frame == nil {
			continue
		}
		if !c.send(frame, binary) {
			// Close for readLoop's sake, but also file the leave ourselves:
			// a client whose readLoop already exited (connected and dropped
			// within one tick) would otherwise linger as a zombie — and a
			// zombie named client squats its identity's online slot.
			c.tr.Close()
			in.mu.Lock()
			in.leaves = append(in.leaves, c)
			in.mu.Unlock()
		}
	}
}

// frameFor builds one client's view and encodes it per the client's mode.
// Runs on the tick goroutine.
func (in *Instance) frameFor(c *client, events []protocol.EventSnap) (frame []byte, binary bool) {
	if c.mode == modeJSONWorld {
		view := in.sim.BuildSnapshotFor(0, 0, events)
		frame, err := json.Marshal(protocol.ServerMsg{Type: "snapshot", Snapshot: &view})
		if err != nil {
			return nil, false
		}
		return frame, false
	}

	view := in.sim.BuildSnapshotFor(c.actor, fm.FromMilli(in.cfg.InterestRadius), events)
	if c.mode == modeJSONView {
		frame, err := json.Marshal(protocol.ServerMsg{Type: "snapshot", Snapshot: &view})
		if err != nil {
			return nil, false
		}
		return frame, false
	}

	in.mu.Lock()
	ack, dirty := c.ack, c.ackDirty
	c.ackDirty = false
	in.mu.Unlock()
	if dirty {
		if v, ok := c.sent[ack]; ok {
			c.baseline = v
			// Everything at or before the acked tick can never be a baseline
			// again — the client acks monotonically.
			for len(c.sentTicks) > 0 && c.sentTicks[0] <= ack {
				delete(c.sent, c.sentTicks[0])
				c.sentTicks = c.sentTicks[1:]
			}
		} else {
			// Ack gap (tick 0, or a view we already pruned): full resend.
			c.baseline = nil
		}
	}

	frame = protocol.EncodeViewFrame(c.baseline, &view)
	// A tick-0 view can never be a baseline: baseTick 0 is the wire's
	// "keyframe" sentinel, and ack 0 is the client's reset signal. Tick 0
	// happens for real right after a floor swap (the new world hasn't
	// stepped yet) — storing it would make the next frame delta against a
	// view the client must treat as empty.
	if view.Tick != 0 {
		if c.sent == nil {
			c.sent = make(map[uint64]*protocol.Snapshot)
		}
		c.sent[view.Tick] = &view
		c.sentTicks = append(c.sentTicks, view.Tick)
	}
	for len(c.sentTicks) > maxUnackedViews {
		delete(c.sent, c.sentTicks[0])
		c.sentTicks = c.sentTicks[1:]
	}
	return frame, true
}

// welcomeFrame builds one client's welcome: protocol version, welcome
// generation, actor, cadence, terrain — and, on descent worlds, the stairs
// position and run state. Any welcome is a full client reset.
func (in *Instance) welcomeFrame(c *client) []byte {
	msg := protocol.ServerMsg{
		Type: "welcome", V: protocol.Version, Gen: c.gen, Actor: uint64(c.actor),
		TickHz: core.TicksPerSecond, SendEvery: in.cfg.SendEvery,
		Map: in.mapSnap,
	}
	for _, p := range in.db.Passives {
		msg.Passives = append(msg.Passives, protocol.PassiveSnap{
			ID: p.ID, Name: p.Name, Desc: p.Desc, Milestone: p.Milestone,
		})
	}
	if in.run > 0 {
		if in.floor > 0 {
			msg.Stairs = &protocol.Vec{X: in.stairs.X.Milli(), Y: in.stairs.Y.Milli()}
		}
		msg.Run = in.runSnap()
	}
	msg.Name = c.name
	msg.Roster = in.roster()
	frame, _ := json.Marshal(msg)
	return frame
}

// roster maps live named actors to display names. Guests aren't in it —
// clients label them generically. Rebuilt per send; it's tiny.
func (in *Instance) roster() map[uint64]string {
	r := map[uint64]string{}
	for _, c := range in.clients {
		if c.name != "" && c.actor != 0 {
			r[uint64(c.actor)] = c.name
		}
	}
	return r
}

// broadcastRoster announces a membership change that comes without a new
// world — swaps don't need it, their welcomes carry the roster.
func (in *Instance) broadcastRoster() {
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "roster", Roster: in.roster()})
	for _, c := range in.clients {
		if !c.send(frame, false) {
			c.tr.Close()
		}
	}
}

func (in *Instance) spawnClient(c *client) bool {
	// Spread joiners out so simultaneous spawns don't stack exactly. Grid
	// worlds enter at the map's spawn room; sim.Spawn clamps the offset
	// back to walkable ground if it pokes into a wall.
	pos := space.V(fm.FromInt(int64(in.joinCount*2)), 0)
	if g := in.sim.W.Grid; g != nil {
		pos = g.Spawn.Add(space.V(fm.FromInt(int64(in.joinCount%4)), 0))
	}
	var id core.EntityID
	// A named player with a banked character resumes it — level, gear,
	// passives, wallet — exactly like a floor swap resumes everyone.
	if c.hasChar {
		if a, err := core.InjectCharacter(in.sim.W, c.lastChar, pos); err == nil {
			id = a.ID
		} else {
			log.Printf("server: inject %q: %v", c.name, err)
		}
	}
	if id == 0 {
		var err error
		id, err = in.sim.Spawn(in.cfg.PlayerDef, pos)
		if err != nil {
			c.tr.Close()
			return false
		}
	}
	in.joinCount++
	in.mu.Lock()
	c.actor = id
	c.gen = 1
	for _, cmd := range c.early {
		cmd.Actor = id
		in.pending = append(in.pending, cmd)
	}
	c.early = nil
	in.mu.Unlock()
	in.clients = append(in.clients, c)
	return true
}

// removeClient despawns a leaver and, for named players, banks their
// character. Idempotent (a leave can be filed twice); reports whether the
// visible roster changed.
func (in *Instance) removeClient(c *client) bool {
	for i, cc := range in.clients {
		if cc == c {
			in.clients = append(in.clients[:i], in.clients[i+1:]...)
			break
		}
	}
	// Despawn between ticks: tombstone now, the next EndTick compacts.
	// Named players' characters are banked below; a guest's carried items
	// vanish with the actor — ephemerality is the guest deal.
	var live *core.Character
	if c.actor != 0 {
		if a := in.sim.W.ActorByID(c.actor); a != nil {
			if !a.Dead {
				ch := core.ExtractCharacter(a)
				live = &ch
			}
			a.Dead = true
		}
	}
	if c.token == "" {
		return false
	}
	// Prefer the still-standing actor; fall back to the death-machinery
	// copy (at most one tick stale) so dying mid-disconnect loses nothing.
	if live == nil && c.hasChar {
		live = &c.lastChar
	}
	in.ids.Disconnect(c.token, live)
	c.token = ""
	return true
}

func (c *client) send(frame []byte, binary bool) bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.bytesSent += uint64(len(frame))
	return c.tr.WriteFrame(frame, binary) == nil
}

// sendPause announces a pause state change. Both wires speak this JSON
// control frame; a failed write closes the client like any send.
func (in *Instance) sendPause(cs []*client, paused bool) {
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "pause", Paused: &paused})
	for _, c := range cs {
		if !c.send(frame, false) {
			c.tr.Close()
		}
	}
}

// Handler is the instance's whole HTTP surface: the WebSocket endpoint,
// the identity API, and (with StaticDir) the web client.
func (in *Instance) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", in.HandleWS)
	mux.HandleFunc("/api/claim", in.handleClaim)
	mux.HandleFunc("/api/whoami", in.handleWhoami)
	if in.cfg.StaticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(in.cfg.StaticDir)))
	}
	return mux
}

// serveHTTP hosts Handler until ctx ends.
func (in *Instance) serveHTTP(ctx context.Context) {
	srv := &http.Server{Addr: in.cfg.HTTPAddr, Handler: in.Handler()}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		// The TCP wire still works without HTTP, but say so instead of
		// silently serving nothing (a squatted port looks exactly like this).
		log.Printf("server: http listener: %v", err)
	}
}
