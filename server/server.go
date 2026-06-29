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
	// Load, if set, restores the world from a World.Save file instead of
	// building one — Seed, Map, Spawns, and Scatter are ignored. Player-def
	// actors in the save are removed at load (no session identity exists yet
	// to reclaim them) with their gear dropped at their feet, so a restart
	// never deletes items.
	Load []byte
}

type Instance struct {
	cfg Config
	db  *core.ContentDB
	sim *sim.Sim
	// mapSnap is the current floor's terrain, encoded when a floor loads; it
	// rides every welcome (initial join and re-welcome on descent).
	mapSnap *protocol.MapSnap

	// runSeed seeds the whole descent; floor N's world derives from it. floor
	// is the current depth (1-based) — the run's score. Both tick-goroutine
	// state once running (runSeed is set once at New).
	runSeed uint64
	floor   int

	mu       sync.Mutex
	pending  []core.Command
	joins    []*client
	leaves   []*client
	adminOps []adminOp
	descends []*client // clients requesting a descent; validated on the tick goroutine

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

	actor core.EntityID
	// early buffers commands that arrive before the tick loop has spawned
	// this client's actor (a fast client races its own welcome); they flush
	// into the pending queue at spawn. Guarded by the instance mutex.
	early []core.Command
	wmu   sync.Mutex

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

// descendRange is how close to the stairs (Grid.Exit) a player must stand to
// trigger a descent. Generous, matching the pickup-reach feel.
var descendRange = fm.FromMilli(2500)

// levelsPerFloor is how many actor levels packs gain per floor descended —
// the escalation knob. Tunable; floor 1 adds nothing.
const levelsPerFloor = 2

func levelBonus(floor int) int {
	if floor <= 1 {
		return 0
	}
	return (floor - 1) * levelsPerFloor
}

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
	in := &Instance{
		cfg:          cfg,
		db:           db,
		runSeed:      cfg.Seed,
		floor:        1,
		listenerAddr: make(chan net.Addr, 1),
	}
	if cfg.Load != nil {
		s, err := sim.Load(db, cfg.Load)
		if err != nil {
			return nil, fmt.Errorf("server: loading world: %w", err)
		}
		in.sim = s
		reclaimOrphanPlayers(s.W, cfg.PlayerDef)
		in.mapSnap = in.sim.EncodeMap()
		return in, nil
	}
	in.sim = sim.New(db, cfg.Seed)
	if cfg.Map != nil {
		in.sim.GenerateMap(space.MapSpec{
			Width: cfg.Map.Width, Height: cfg.Map.Height, Rooms: cfg.Map.Rooms,
		})
		in.mapSnap = in.sim.EncodeMap()
	}
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
		if wc.Kind == "ack" {
			// Transport-level: moves this client's delta baseline. ack 0 (or
			// any tick we no longer hold) resets to keyframes.
			in.mu.Lock()
			c.ack, c.ackDirty = wc.Tick, true
			in.mu.Unlock()
			continue
		}
		if wc.Kind == "descend" {
			// Host-level intent, never a sim command: it swaps the whole world.
			// Validated (proximity to the stairs) on the tick goroutine.
			in.mu.Lock()
			in.descends = append(in.descends, c)
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
	descends := in.descends
	in.joins, in.leaves, in.pending, in.adminOps, in.descends = nil, nil, nil, nil, nil
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

	for _, c := range leaves {
		in.removeClient(c)
	}

	// A descent swaps the whole world between ticks: extract every live
	// player, build the next floor (scaled packs), inject them, and re-welcome
	// everyone. When it fires this tick belongs to the transfer — joiners
	// spawn straight into the new floor and we skip the step/send below; the
	// fresh floor ticks normally next time.
	if !in.paused && len(descends) > 0 && in.descend(descends) {
		for _, c := range joins {
			if in.spawnClient(c) {
				in.sendWelcome(c)
			}
		}
		return
	}

	var welcomes []*client
	for _, c := range joins {
		if in.spawnClient(c) {
			welcomes = append(welcomes, c)
		}
	}

	if !in.paused {
		// Stable sort by actor: fair, and preserves each client's own command
		// order. Arrival interleaving across clients is network timing — the
		// server's ordering is the authoritative one.
		sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].Actor < cmds[j].Actor })
		in.sim.Step(cmds)
	}
	// (Paused: cmds are dropped, not queued — a long pause must not release
	// a flood of stale intent on resume.)

	for _, c := range welcomes {
		in.sendWelcome(c)
	}
	if in.paused && len(welcomes) > 0 {
		in.sendPause(welcomes, true) // joined mid-pause; tell them why nothing moves
	}

	// The sim runs every tick; views go out every SendEvery ticks, carrying
	// the events of the whole window. A paused world re-reports its last
	// step's events, so only harvest fresh ones.
	if !in.paused {
		fresh := in.sim.EncodeEvents()
		in.eventBuf = append(in.eventBuf, fresh...)
		for _, ev := range fresh {
			in.recentEvents = append(in.recentEvents, adminEvent{Tick: in.sim.W.Tick, EventSnap: ev})
		}
		if len(in.recentEvents) > adminEventCap {
			in.recentEvents = in.recentEvents[len(in.recentEvents)-adminEventCap:]
		}
	}
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
			c.tr.Close() // readLoop notices and files the leave
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
	if c.sent == nil {
		c.sent = make(map[uint64]*protocol.Snapshot)
	}
	c.sent[view.Tick] = &view
	c.sentTicks = append(c.sentTicks, view.Tick)
	for len(c.sentTicks) > maxUnackedViews {
		delete(c.sent, c.sentTicks[0])
		c.sentTicks = c.sentTicks[1:]
	}
	return frame, true
}

func (in *Instance) spawnClient(c *client) bool {
	// Spread joiners out so simultaneous spawns don't stack exactly. Grid
	// worlds enter at the map's spawn room; sim.Spawn clamps the offset
	// back to walkable ground if it pokes into a wall.
	pos := space.V(fm.FromInt(int64(in.joinCount*2)), 0)
	if g := in.sim.W.Grid; g != nil {
		pos = g.Spawn.Add(space.V(fm.FromInt(int64(in.joinCount%4)), 0))
	}
	id, err := in.sim.Spawn(in.cfg.PlayerDef, pos)
	if err != nil {
		c.tr.Close()
		return false
	}
	in.joinCount++
	in.mu.Lock()
	c.actor = id
	for _, cmd := range c.early {
		cmd.Actor = id
		in.pending = append(in.pending, cmd)
	}
	c.early = nil
	in.mu.Unlock()
	in.clients = append(in.clients, c)
	return true
}

// sendWelcome marshals and sends the welcome frame for a client — the same
// frame on an initial join and on a descent re-welcome (DESIGN §14: a zone
// transfer is a full re-welcome on the live socket). It carries the current
// floor and that floor's terrain.
func (in *Instance) sendWelcome(c *client) {
	welcome, _ := json.Marshal(protocol.ServerMsg{
		Type: "welcome", V: protocol.Version, Actor: uint64(c.actor),
		TickHz: core.TicksPerSecond, SendEvery: in.cfg.SendEvery,
		Floor: in.floor, Map: in.mapSnap,
	})
	c.send(welcome, false)
}

// buildFloor generates a fresh world for the given floor: seed derived from
// the run seed, the run's map footprint, fixed scenario spawns at base level,
// and scattered packs scaled up by depth. The world is whole and tickable but
// holds no players yet — descend injects them.
func (in *Instance) buildFloor(floor int) (*sim.Sim, error) {
	s := sim.New(in.db, core.FloorSeed(in.runSeed, floor))
	if in.cfg.Map != nil {
		s.GenerateMap(space.MapSpec{
			Width: in.cfg.Map.Width, Height: in.cfg.Map.Height, Rooms: in.cfg.Map.Rooms,
		})
	}
	for _, sp := range in.cfg.Spawns {
		if _, err := s.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y))); err != nil {
			return nil, fmt.Errorf("server: floor %d spawn: %w", floor, err)
		}
	}
	bonus := levelBonus(floor)
	for _, sc := range in.cfg.Scatter {
		if err := s.ScatterSpawnLeveled(sc.Def, sc.Count, bonus); err != nil {
			return nil, fmt.Errorf("server: floor %d scatter: %w", floor, err)
		}
	}
	return s, nil
}

// descend swaps the instance onto the next floor when a requester is standing
// on the stairs. It extracts every live player into a portable character,
// builds the next floor, injects them, swaps the running world, and
// re-welcomes everyone (their delta encoder state can't survive a world
// swap). Returns false — leaving the floor unchanged — if nobody is on the
// stairs or the floor fails to build. Runs on the tick goroutine, so touching
// the world and client list is safe.
//
// One requester moves the whole party: this is a shared, single-instance
// descent (DESIGN §14 sequences the instance manager for later). Dead
// spectators don't carry down; they get a fresh-but-actorless view.
func (in *Instance) descend(reqs []*client) bool {
	cur := in.sim.W
	g := cur.Grid
	if g == nil {
		return false // the open plane has no stairs
	}
	onStairs := false
	for _, c := range reqs {
		if a := cur.ActorByID(c.actor); a != nil && !a.Dead && space.Dist(a.Pos, g.Exit) <= descendRange {
			onStairs = true
			break
		}
	}
	if !onStairs {
		return false
	}

	type survivor struct {
		c  *client
		ch core.Character
	}
	var survivors []survivor
	for _, c := range in.clients {
		if a := cur.ActorByID(c.actor); a != nil && !a.Dead {
			survivors = append(survivors, survivor{c, core.ExtractCharacter(a)})
		}
	}

	next, err := in.buildFloor(in.floor + 1)
	if err != nil {
		log.Printf("server: descent to floor %d failed: %v", in.floor+1, err)
		return false // keep the current floor rather than strand the party
	}

	spawn := space.Vec2{}
	if next.W.Grid != nil {
		spawn = next.W.Grid.Spawn
	}
	for i, s := range survivors {
		pos := spawn.Add(space.V(fm.FromInt(int64(i%4)), 0))
		a, err := next.W.InjectCharacter(s.ch, pos)
		if err != nil {
			log.Printf("server: descent inject failed: %v", err)
			return false
		}
		s.c.actor = a.ID
	}

	in.sim = next
	in.floor++
	in.mapSnap = next.EncodeMap()
	in.eventBuf = nil
	in.sinceSend = 0
	for _, c := range in.clients {
		in.resetClientView(c)
		in.sendWelcome(c)
	}
	return true
}

func (in *Instance) removeClient(c *client) {
	for i, cc := range in.clients {
		if cc == c {
			in.clients = append(in.clients[:i], in.clients[i+1:]...)
			break
		}
	}
	// Despawn between ticks: tombstone now, the next EndTick compacts.
	// Carried items vanish with the actor — no persistence yet.
	if c.actor != 0 {
		if a := in.sim.W.ActorByID(c.actor); a != nil {
			a.Dead = true
		}
	}
}

// resetClientView clears a client's delta-encoder and ack bookkeeping so the
// next view is a fresh keyframe. Called on a re-welcome (descent): the old
// baselines reference a world that no longer exists. baseline/sent/sentTicks
// are tick-goroutine-only; ack/ackDirty are guarded by the instance mutex.
func (in *Instance) resetClientView(c *client) {
	c.baseline = nil
	c.sent = nil
	c.sentTicks = nil
	in.mu.Lock()
	c.ack, c.ackDirty = 0, false
	in.mu.Unlock()
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

// serveHTTP hosts the WebSocket endpoint (and the web client, if a static
// dir is configured) until ctx ends.
func (in *Instance) serveHTTP(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", in.HandleWS)
	if in.cfg.StaticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(in.cfg.StaticDir)))
	}
	srv := &http.Server{Addr: in.cfg.HTTPAddr, Handler: mux}
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
