// Package server hosts a sim instance over TCP with newline-delimited JSON
// frames: clients send protocol.Command lines, the server sends
// protocol.ServerMsg lines (a welcome, then one snapshot per tick).
//
// Concurrency model: the tick goroutine owns ALL world mutation — joins,
// leaves, commands, and Step happen there, at tick boundaries. Connection
// goroutines only decode lines and append to a mutex-guarded queue. This
// preserves the sim's single-goroutine invariant; the server scales by
// running more instances, not by threading one.
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	Addr string
	Seed uint64
	// TickInterval defaults to one real tick (1s / core.TicksPerSecond).
	// Tests shrink it; the sim itself never reads the clock.
	TickInterval time.Duration
	// Spawns places the map's starting actors (monsters, dummies).
	Spawns []protocol.ScriptSpawn
	// PlayerDef is the actor def spawned per connecting client.
	PlayerDef string
}

type Instance struct {
	cfg Config
	sim *sim.Sim

	mu      sync.Mutex
	pending []core.Command
	joins   []*client
	leaves  []*client

	clients   []*client
	joinCount int

	listenerAddr chan net.Addr
}

type client struct {
	conn  net.Conn
	actor core.EntityID
	// early buffers commands that arrive before the tick loop has spawned
	// this client's actor (a fast client races its own welcome); they flush
	// into the pending queue at spawn. Guarded by the instance mutex.
	early []core.Command
	wmu   sync.Mutex
}

func New(db *core.ContentDB, cfg Config) (*Instance, error) {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = time.Second / core.TicksPerSecond
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

	ticker := time.NewTicker(in.cfg.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, c := range in.clients {
				c.conn.Close()
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
		c := &client{conn: conn}
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
		c.conn.Close()
		in.mu.Lock()
		in.leaves = append(in.leaves, c)
		in.mu.Unlock()
	}()
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 0, 4096), maxLineBytes)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		var wc protocol.Command
		if err := json.Unmarshal(scanner.Bytes(), &wc); err != nil {
			continue // garbage line; the sim never sees it
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
	in.joins, in.leaves, in.pending = nil, nil, nil
	in.mu.Unlock()

	for _, c := range leaves {
		in.removeClient(c)
	}
	var welcomes []*client
	for _, c := range joins {
		if in.spawnClient(c) {
			welcomes = append(welcomes, c)
		}
	}

	// Stable sort by actor: fair, and preserves each client's own command
	// order. Arrival interleaving across clients is network timing — the
	// server's ordering is the authoritative one.
	sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].Actor < cmds[j].Actor })
	in.sim.Step(cmds)

	snap := in.sim.BuildSnapshot()
	frame, err := json.Marshal(protocol.ServerMsg{Type: "snapshot", Snapshot: &snap})
	if err != nil {
		return
	}
	for _, c := range welcomes {
		welcome, _ := json.Marshal(protocol.ServerMsg{Type: "welcome", Actor: uint64(c.actor)})
		c.send(welcome)
	}
	for _, c := range in.clients {
		if !c.send(frame) {
			c.conn.Close() // readLoop notices and files the leave
		}
	}
}

func (in *Instance) spawnClient(c *client) bool {
	// Spread joiners out so simultaneous spawns don't stack exactly.
	pos := space.V(fm.FromInt(int64(in.joinCount*2)), 0)
	id, err := in.sim.Spawn(in.cfg.PlayerDef, pos)
	if err != nil {
		c.conn.Close()
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

func (c *client) send(frame []byte) bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := c.conn.Write(frame); err != nil {
		return false
	}
	_, err := c.conn.Write([]byte{'\n'})
	return err == nil
}
