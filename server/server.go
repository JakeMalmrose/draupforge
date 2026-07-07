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
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
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
	// writeTimeout bounds one frame write on a client's writer goroutine —
	// sends are queued per client (newClient), so a stalled socket stalls
	// only its own writer, never the tick loop, and dies within a timeout.
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
	// Seed is the world seed. 0 (the default) rolls a random one from the
	// OS entropy pool and logs it — pass it back as -seed to reproduce a
	// session. Randomness enters the system only here, at the host edge;
	// everything below stays deterministic in the seed.
	Seed uint64
	// StartFloor is where a descent run begins. 0 (the default) starts in
	// the hideout, floor 1 reachable through its portal; tests and dev
	// servers can start directly on a floor. Applies only to Map worlds.
	StartFloor int
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
	// WSOrigins is extra allowed WebSocket origin patterns (host[:port],
	// * wildcards OK). Empty means same-origin only — browsers on other
	// hosts are refused; non-browser clients send no Origin and pass.
	WSOrigins []string
	// ReplayDir, if set, records every world this instance runs as a
	// replayable segment (replay.go) — a live bug becomes a local repro
	// via cmd/headless -replay. Off by default.
	ReplayDir string
}

type Instance struct {
	cfg Config
	db  *core.ContentDB
	sim *sim.Sim
	ids *IdentityStore
	// lobby is set when this instance is one of many (party mode); nil for
	// a standalone instance. The tick goroutine calls into it for social
	// verbs and membership changes; the lobby never touches the world.
	lobby *Lobby
	// id names this instance in the lobby's registry and admin UI.
	id int
	// mapSnap is the terrain encoded once per world; rides every welcome.
	mapSnap *protocol.MapSnap

	// replay records this instance's worlds when cfg.ReplayDir is set;
	// nil otherwise. surgery marks the world mutated outside Step (joins,
	// leaves, swaps, grace, admin ops, stash) — the recorder rotates to a
	// fresh segment before the next Step, so every segment spans a pure
	// command-driven stretch. Both tick-goroutine-only.
	replay  *replayLog
	surgery bool

	// tickCount drives periodic host-layer work (character banking); it is
	// process time, not world time — world swaps don't reset it.
	tickCount uint64

	// Lobby-facing telemetry, written on the tick goroutine, read by lobby
	// goroutines: the current party (named clients), the client count, and
	// when the instance last emptied (for reaping).
	partyNames atomic.Value // []string
	clientN    atomic.Int32
	emptyAt    atomic.Int64 // unix nanos; 0 = occupied or never occupied

	// Descent run state (descent.go), tick-goroutine-only. run == 0 means
	// no descent (open-plane worlds); everything else is meaningful only
	// when run > 0.
	run         int        // 1-based run counter; a run ends when the portals run out
	runSeed     uint64     // this run's seed; floor worlds derive from it
	floor       int        // current depth; 0 is the hideout
	route       int        // current floor's route address (descent chart)
	chamber     int        // side chambers taken at this depth (mods stack)
	best        int        // deepest floor reached this process — the score
	stairs      space.Vec2 // this floor's descent stairs (farthest walkable from spawn)
	portalFloor int        // where death ejects to
	portalRoute int        // the anchor floor's full route address...
	portalChmbr int        // ...so an eject rebuilds the exact same world
	portalPos   space.Vec2
	// portalPlaced: portalPos is valid for portalFloor. False while a run
	// starts in the hideout — the anchor lands on the floor's spawn the
	// first time someone steps through.
	portalPlaced bool
	portalsLeft  int

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

	// mu guards the client's inbound state below — everything readLoop and
	// a tick goroutine both touch. It is its own lock domain so a client
	// can move between instances (party transfers) without entangling two
	// instance mutexes; never hold an instance mutex while taking it.
	mu sync.Mutex
	// inst is the instance currently housing this client — where readLoop
	// routes commands and files the leave. Rewritten when a party transfer
	// queues the client onto its destination.
	inst  *Instance
	actor core.EntityID
	// early buffers commands that arrive before the tick loop has spawned
	// this client's actor (a fast client races its own welcome); they flush
	// into the pending queue at spawn.
	early []core.Command

	// out is the outbound frame queue, drained by one writer goroutine per
	// connection (newClient starts it) — the tick loop never blocks on a
	// socket. A full queue means the client is hopelessly behind: send
	// closes it. quit ends the writer when the connection dies; nil out
	// (hand-built test clients) falls back to writing synchronously.
	out      chan outFrame
	quit     chan struct{}
	quitOnce sync.Once

	// gen is the welcome generation: +1 per welcome (join, floor swap,
	// party transfer). readLoop drops acks whose gen doesn't match — they
	// deltaed against a world this client no longer sees.
	gen int
	// wantDescend/wantPlant/wantPortal buffer the transport-level run verbs
	// for the tick goroutine, like ack. The social verbs ride along:
	// wantInvite names the invitee, the rest are flags.
	wantDescend bool
	// wantRoute is a "route" verb's chart pick, stored +1 so the zero
	// value means "none pending".
	wantRoute   int
	wantPlant   bool
	wantPortal  bool
	wantInvite  string
	wantAccept  bool
	wantDecline bool
	wantLeave   bool
	// stashOps buffers stash verbs in arrival order (hideout bank; stash.go).
	stashOps []stashOp
	// wantSheet buffers a "sheet" verb: the client's C panel wants the
	// computed character sheet after this tick.
	wantSheet bool
	// chatMsgs buffers "chat" verbs (lines and pings) for the tick; the
	// bucket meters them so a spammer drops instead of flooding the party.
	chatMsgs   []protocol.ChatSnap
	chatBucket *tokenBucket

	// recentHits are the last few hits taken, tick-goroutine-only — the
	// death recap's evidence (ladder.go). Cleared on every world swap.
	recentHits []protocol.RecapHit

	// hardcore/ssf mirror the in-play character's permanent mode flags,
	// set once at connect (before the client is shared) and read-only
	// after: one death ends a hardcore character; SSF sees no stash and
	// no parties.
	hardcore bool
	ssf      bool
	// doom, when positive, counts ticks until the server closes this
	// client's socket — the grace that lets a hardcore death's recap and
	// farewell frames flush before the kick.
	doom int

	// lastChar is the freshest character extraction for this client's
	// actor, taken after every step — death compacts the actor away before
	// the host can see it, so eject/respawn works from this copy (at most
	// one tick stale). Tick-goroutine-only.
	lastChar core.Character
	hasChar  bool

	// ack is the latest view tick the client confirmed, recorded by readLoop
	// and consumed by the tick goroutine. Guarded by mu.
	ack      uint64
	ackDirty bool

	// Delta-encoder state, tick-goroutine-only. baseline is the last acked
	// view (nil → next send is a keyframe); sent holds unacked views the
	// client may still ack, oldest first in sentTicks.
	baseline  *protocol.Snapshot
	sent      map[uint64]*protocol.Snapshot
	sentTicks []uint64

	// bytesSent feeds the admin dashboard's bandwidth column. Atomic: the
	// lobby's social pushes enqueue from lobby goroutines while admin ops
	// read on the tick goroutine.
	bytesSent atomic.Uint64
}

// outFrame is one queued outbound frame.
type outFrame struct {
	data   []byte
	binary bool
}

// sendQueueDepth bounds the per-client outbound backlog: ~6s of views at
// the default 10Hz send rate. A client that far behind has a busted
// interpolation buffer anyway — closing it beats stalling anyone.
const sendQueueDepth = 64

// newClient wires a connection with its send queue and starts the writer
// goroutine. Every real connection comes through here; tests that hand-
// build clients leave out nil and send() writes synchronously.
func newClient(tr transport, m mode) *client {
	c := &client{
		tr: tr, mode: m,
		out:  make(chan outFrame, sendQueueDepth),
		quit: make(chan struct{}),
	}
	go func() {
		for {
			select {
			case f := <-c.out:
				if c.tr.WriteFrame(f.data, f.binary) != nil {
					c.tr.Close() // readLoop errors and files the leave
					return
				}
			case <-c.quit:
				return
			}
		}
	}()
	return c
}

// stopWriter ends the client's writer goroutine; safe to call twice, and a
// no-op for queue-less test clients.
func (c *client) stopWriter() {
	if c.quit == nil {
		return
	}
	c.quitOnce.Do(func() { close(c.quit) })
}

// maxUnackedViews bounds the per-client baseline candidates (~3s at the
// default 10Hz send rate). A client acking something older just gets a
// keyframe.
const maxUnackedViews = 32

// socialWant is one client's harvested social verbs for a tick, handed to
// the lobby after the world steps.
type socialWant struct {
	c               *client
	invite          string
	accept, decline bool
	leave           bool
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
	if cfg.Seed == 0 {
		cfg.Seed = randomSeed()
		log.Printf("server: rolled world seed %d (pass -seed %d to reproduce)", cfg.Seed, cfg.Seed)
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
			in.route, in.chamber = rs.Route, rs.Chamber
			in.portalFloor, in.portalPos = rs.PortalFloor, rs.PortalPos
			in.portalRoute, in.portalChmbr = rs.PortalRoute, rs.PortalChamber
			in.portalPlaced = rs.PortalPlaced
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
		if err := in.startRunWorld(); err != nil {
			return nil, err
		}
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
// process has no live session to hand them to (identities bank characters
// separately), so they'd stand frozen forever; instead their gear drops
// where they stood and the actor goes away. Runs before any client or tick
// exists, so mutating the world directly is safe.
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

// randomSeed rolls a nonzero seed from the OS entropy pool. This is the
// only place randomness enters the stack — everything below the host edge
// stays deterministic in the seed it is given.
func randomSeed() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("server: no entropy for a world seed: %v", err))
	}
	n := binary.LittleEndian.Uint64(b[:])
	if n == 0 {
		n = 1 // 0 means "roll one"; never hand it back
	}
	return n
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

	in.runLoop(ctx)
	return ctx.Err()
}

// run drives the tick loop until ctx ends — the whole life of an instance
// in lobby mode, where the lobby owns all listeners.
func (in *Instance) runLoop(ctx context.Context) {
	if in.cfg.ReplayDir != "" {
		in.replay = &replayLog{dir: in.cfg.ReplayDir, id: in.id}
		in.surgery = true // first tick opens the first segment
		defer in.replay.close()
	}
	ticker := time.NewTicker(in.cfg.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, c := range in.clients {
				c.tr.Close()
			}
			return
		case <-ticker.C:
			in.tick()
		}
	}
}

// publishParty snapshots the named-client list for lobby goroutines, and
// keeps the occupancy telemetry the reaper reads. Tick goroutine only.
func (in *Instance) publishParty() {
	names := []string{}
	for _, c := range in.clients {
		if c.name != "" {
			names = append(names, c.name)
		}
	}
	in.partyNames.Store(names)
	in.clientN.Store(int32(len(in.clients)))
	if len(in.clients) == 0 {
		if in.emptyAt.Load() == 0 {
			in.emptyAt.Store(time.Now().UnixNano())
		}
	} else {
		in.emptyAt.Store(0)
	}
}

// releaseClient hands a client off for a party transfer: out of the client
// list and the world (character extracted like any zone transfer), but the
// socket stays open and the identity stays online. Tick goroutine only;
// reports false if the client isn't actually here.
func (in *Instance) releaseClient(c *client) bool {
	found := false
	for i, cc := range in.clients {
		if cc == c {
			in.clients = append(in.clients[:i], in.clients[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if a := in.sim.W.ActorByID(c.actor); a != nil {
		if !a.Dead {
			c.lastChar, c.hasChar = core.ExtractCharacter(a), true
		}
		a.Dead = true
	}
	in.publishParty()
	return true
}

func (in *Instance) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		c := newClient(newTCPTransport(conn), modeJSONWorld)
		c.inst = in
		in.mu.Lock()
		in.joins = append(in.joins, c)
		in.mu.Unlock()
		go readLoop(ctx, c)
	}
}

// readLoop decodes one client's command lines. The actor field is always
// overwritten with the client's assigned actor — clients command only
// themselves, whatever they claim. Commands route to the client's current
// instance, which a party transfer may swap mid-stream.
func readLoop(ctx context.Context, c *client) {
	defer func() {
		c.stopWriter()
		c.tr.Close()
		c.mu.Lock()
		in := c.inst
		c.mu.Unlock()
		in.mu.Lock()
		in.leaves = append(in.leaves, c)
		in.mu.Unlock()
	}()
	bucket := newCmdBucket()
	for {
		frame, err := c.tr.ReadFrame()
		if err != nil || ctx.Err() != nil {
			return
		}
		if !bucket.allow(time.Now()) {
			continue // command flood: drop on the floor, never reach the tick
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
			c.mu.Lock()
			if wc.Gen == c.gen {
				c.ack, c.ackDirty = wc.Tick, true
			}
			c.mu.Unlock()
			continue
		case "descend", "route", "plant_portal", "enter_portal",
			"invite", "accept_invite", "decline_invite", "leave_party",
			"stash_put", "stash_take", "sheet", "chat":
			// Run, social, stash, sheet, and chat verbs are host-layer,
			// like ack — the sim never sees them.
			c.mu.Lock()
			switch wc.Kind {
			case "descend":
				c.wantDescend = true
			case "route":
				c.wantRoute = wc.Choice + 1
			case "plant_portal":
				c.wantPlant = true
			case "enter_portal":
				c.wantPortal = true
			case "invite":
				c.wantInvite = wc.Name
			case "accept_invite":
				c.wantAccept = true
			case "decline_invite":
				c.wantDecline = true
			case "leave_party":
				c.wantLeave = true
			case "stash_put":
				c.stashOps = append(c.stashOps, stashOp{item: core.EntityID(wc.Target)})
			case "stash_take":
				c.stashOps = append(c.stashOps, stashOp{take: true, idx: wc.Choice})
			case "sheet":
				c.wantSheet = true
			case "chat":
				if c.chatBucket == nil {
					c.chatBucket = newChatBucket()
				}
				if c.chatBucket.allow(time.Now()) {
					m := protocol.ChatSnap{Text: wc.Text}
					if wc.Text == "" && (wc.X != 0 || wc.Y != 0) {
						m.Ping = &protocol.Vec{X: wc.X, Y: wc.Y}
					}
					c.chatMsgs = append(c.chatMsgs, m)
				}
			}
			c.mu.Unlock()
			continue
		}
		cmd, err := sim.DecodeCommand(wc)
		if err != nil {
			continue
		}
		// The spawned check and the early append share one critical section:
		// a spawn between them would flush the early buffer under our feet
		// and strand this command.
		c.mu.Lock()
		if c.actor != 0 {
			cmd.Actor = c.actor
			in := c.inst
			c.mu.Unlock()
			in.mu.Lock()
			in.pending = append(in.pending, cmd)
			in.mu.Unlock()
		} else {
			c.early = append(c.early, cmd)
			c.mu.Unlock()
		}
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
	in.mu.Unlock()
	var descends, portals, plants, sheets []*client
	var social []socialWant
	var stashes []stashWant
	var routes []routeWant
	var chats []chatWant
	for _, c := range in.clients {
		c.mu.Lock()
		if c.wantDescend {
			c.wantDescend = false
			descends = append(descends, c)
		}
		if c.wantRoute > 0 {
			routes = append(routes, routeWant{c: c, choice: c.wantRoute - 1})
			c.wantRoute = 0
		}
		if c.wantPortal {
			c.wantPortal = false
			portals = append(portals, c)
		}
		if c.wantPlant {
			c.wantPlant = false
			plants = append(plants, c)
		}
		if c.wantInvite != "" || c.wantAccept || c.wantDecline || c.wantLeave {
			social = append(social, socialWant{
				c: c, invite: c.wantInvite,
				accept: c.wantAccept, decline: c.wantDecline, leave: c.wantLeave,
			})
			c.wantInvite, c.wantAccept, c.wantDecline, c.wantLeave = "", false, false, false
		}
		if len(c.stashOps) > 0 {
			stashes = append(stashes, stashWant{c: c, ops: c.stashOps})
			c.stashOps = nil
		}
		if len(c.chatMsgs) > 0 {
			chats = append(chats, chatWant{c: c, msgs: c.chatMsgs})
			c.chatMsgs = nil
		}
		if c.wantSheet {
			c.wantSheet = false
			sheets = append(sheets, c)
		}
		c.mu.Unlock()
	}

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

	// Joins before leaves: a client that connected and dropped inside one
	// tick window spawns and is removed in order, instead of its leave
	// no-opping first and the join then spawning a zombie.
	rosterChanged := false
	var welcomes []*client
	for _, c := range joins {
		if in.spawnClient(c) {
			welcomes = append(welcomes, c)
			rosterChanged = rosterChanged || c.name != ""
		}
	}
	for _, c := range leaves {
		rosterChanged = in.removeClient(c) || rosterChanged
	}
	if len(ops)+len(joins)+len(leaves) > 0 {
		in.surgery = true // replay: world possibly touched outside Step
	}

	if !in.paused {
		// Stable sort by actor: fair, and preserves each client's own command
		// order. Arrival interleaving across clients is network timing — the
		// server's ordering is the authoritative one.
		sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].Actor < cmds[j].Actor })
		if in.replay != nil && in.surgery {
			// Everything between the last Step and here (joins, leaves,
			// swaps, grace, admin, stash) is outside the command record —
			// start a fresh segment from this boundary.
			in.replay.rotate(in.sim.W)
			in.surgery = false
		}
		in.sim.Step(cmds)
		if in.replay != nil {
			in.replay.record(in.sim.W.Tick, cmds)
		}

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

		// Chat relays before run logic — a floor swap re-welcomes everyone
		// and a line sent on the death tick should still land.
		in.processChat(chats)

		// Stash verbs before run logic: runTick refreshes each client's
		// banked character copy, and a just-stashed item must not linger in
		// that copy's bag (put + crash would otherwise duplicate it).
		in.processStash(stashes)

		// The descent: deaths, stairs, portals — may swap the world and
		// re-welcome everyone (descent.go).
		in.runTick(fresh, descends, portals, plants, routes)

		// Doomed clients (hardcore falls) close once their fuse burns —
		// the delay lets the recap and farewell frames flush first.
		for _, c := range in.clients {
			if c.doom > 0 {
				c.doom--
				if c.doom == 0 {
					c.tr.Close()
				}
			}
		}

		// Character sheets last, off the settled world: read-only, so no
		// surgery flag — the replay never notices a sheet request.
		for _, c := range sheets {
			if sheet := sim.BuildSheet(in.sim.W, c.actor); sheet != nil {
				frame, _ := json.Marshal(protocol.ServerMsg{Type: "sheet", Sheet: sheet})
				if !c.send(frame, false) {
					c.tr.Close()
				}
			}
		}
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
	if in.lobby != nil {
		if rosterChanged || len(joins) > 0 || len(leaves) > 0 {
			in.publishParty()
			in.lobby.partyChanged(in)
		}
		if len(social) > 0 {
			in.lobby.processSocial(in, social)
		}
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

	c.mu.Lock()
	ack, dirty := c.ack, c.ackDirty
	c.ackDirty = false
	c.mu.Unlock()
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
	c.mu.Lock()
	gen, actor := c.gen, c.actor
	c.mu.Unlock()
	msg := protocol.ServerMsg{
		Type: "welcome", V: protocol.Version, Gen: gen, Actor: uint64(actor),
		TickHz: core.TicksPerSecond, SendEvery: in.cfg.SendEvery,
		Map: in.mapSnap,
	}
	for _, p := range in.db.Passives {
		msg.Passives = append(msg.Passives, protocol.PassiveSnap{
			ID: p.ID, Name: p.Name, Desc: p.Desc, Milestone: p.Milestone,
		})
	}
	for _, sup := range in.db.Supports {
		ss := protocol.SupportSnap{ID: sup.ID, Name: sup.Name, Desc: sup.Desc}
		for _, sk := range in.db.Cuttable {
			if sk.Tags.ContainsAll(sup.Requires) {
				ss.LegalFor = append(ss.LegalFor, sk.ID)
			}
		}
		msg.Supports = append(msg.Supports, ss)
	}
	for _, sk := range in.db.Cuttable {
		msg.CutSkills = append(msg.CutSkills, protocol.SkillSnap{ID: sk.ID, Name: sk.Name})
	}
	if in.run > 0 {
		if in.floor > 0 {
			msg.Stairs = &protocol.Vec{X: in.stairs.X.Milli(), Y: in.stairs.Y.Milli()}
		}
		msg.Run = in.runSnap()
	}
	msg.Name = c.name
	msg.Hardcore, msg.SSF = c.hardcore, c.ssf
	if c.token != "" {
		msg.Feats = in.ids.Feats(c.token)
	}
	msg.Roster = in.roster()
	if !c.ssf {
		msg.Stash = in.stashSnap(c) // nil for guests — no identity, no bank
	}
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
	c.mu.Lock()
	c.actor = id
	c.gen++ // monotonic per client: a party transfer must outrun old acks
	c.ack, c.ackDirty = 0, false
	early := c.early
	c.early = nil
	c.mu.Unlock()
	if len(early) > 0 {
		in.mu.Lock()
		for _, cmd := range early {
			cmd.Actor = id
			in.pending = append(in.pending, cmd)
		}
		in.mu.Unlock()
	}
	c.baseline, c.sent, c.sentTicks = nil, nil, nil
	in.clients = append(in.clients, c)
	return true
}

// removeClient despawns a leaver and, for named players, banks their
// character. Only a client actually in this instance's list is acted on —
// that makes double-filed leaves idempotent and keeps a leave that raced a
// party transfer from disconnecting an identity that lives elsewhere now.
// Reports whether the visible roster changed.
func (in *Instance) removeClient(c *client) bool {
	found := false
	for i, cc := range in.clients {
		if cc == c {
			in.clients = append(in.clients[:i], in.clients[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return false
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
	if in.lobby != nil {
		in.lobby.playerLeft(c)
	}
	c.token = ""
	return true
}

func (c *client) send(frame []byte, binary bool) bool {
	c.bytesSent.Add(uint64(len(frame)))
	if c.out == nil {
		return c.tr.WriteFrame(frame, binary) == nil // queue-less test client
	}
	select {
	case c.out <- outFrame{data: frame, binary: binary}:
		return true
	default:
		// The queue is full: the client hasn't drained ~6s of frames. It's
		// beyond saving — cut it loose rather than let it shed backpressure
		// onto the tick loop (the whole point of the queue).
		c.tr.Close()
		return false
	}
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
	mux.HandleFunc("/api/claim", in.ids.handleClaim)
	mux.HandleFunc("/api/whoami", in.ids.handleWhoami)
	mux.HandleFunc("/api/forget", in.ids.handleForget(in.kickToken))
	mux.HandleFunc("/api/ladder", in.ids.handleLadder)
	if in.cfg.StaticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(in.cfg.StaticDir)))
	}
	return mux
}

// kickToken severs this instance's session holding token, if any — the
// standalone-mode counterpart of Lobby.kickToken, run on the tick
// goroutine like the admin kick.
func (in *Instance) kickToken(tok string) {
	in.runOnTick(func() (any, error) {
		for _, c := range in.clients {
			if c.token == tok {
				c.tr.Close() // readLoop files the leave
			}
		}
		return nil, nil
	})
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
