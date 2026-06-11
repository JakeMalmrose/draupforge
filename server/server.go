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
	// Spawns places the map's starting actors (monsters, dummies).
	Spawns []protocol.ScriptSpawn
	// PlayerDef is the actor def spawned per connecting client.
	PlayerDef string
}

type Instance struct {
	cfg Config
	sim *sim.Sim

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
		sim:          sim.New(db, cfg.Seed),
		listenerAddr: make(chan net.Addr, 1),
	}
	for _, sp := range cfg.Spawns {
		if _, err := in.sim.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y))); err != nil {
			return nil, fmt.Errorf("server: scenario spawn: %w", err)
		}
	}
	return in, nil
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
		welcome, _ := json.Marshal(protocol.ServerMsg{
			Type: "welcome", V: protocol.Version, Actor: uint64(c.actor),
			TickHz: core.TicksPerSecond, SendEvery: in.cfg.SendEvery,
		})
		c.send(welcome, false)
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
	// Spread joiners out so simultaneous spawns don't stack exactly.
	pos := space.V(fm.FromInt(int64(in.joinCount*2)), 0)
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
