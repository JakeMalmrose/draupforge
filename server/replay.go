// The replay log — where the determinism discipline pays out for ops. With
// -replaydir set, every world an instance runs gets a segment file: a full
// World.Save as the header, then one NDJSON line per tick that carried
// commands (in the exact sorted order Step received). Re-executing the
// segment (cmd/headless -replay) reproduces the world bit-for-bit, so a
// live bug report becomes a local repro. Off by default: recording costs a
// world serialization per swap and a line per commanded tick.
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/JakeMalmrose/draupforge/sim/core"
)

// ReplayHeader is a segment's first line: the world it starts from.
type ReplayHeader struct {
	Version int             `json:"version"`
	World   json.RawMessage `json:"world"`
}

// ReplayLine is one commanded tick. Tick is the value World.Tick will hold
// AFTER the Step that consumed these commands — matching how the recorder
// sees it — so the replayer steps empty ticks until World.Tick+1 == Tick.
type ReplayLine struct {
	Tick     uint64         `json:"tick"`
	Commands []core.Command `json:"commands"`
}

const replayVersion = 1

// replayLog is one instance's recorder. Tick-goroutine-only.
type replayLog struct {
	dir  string
	id   int // instance id, for filenames
	seq  int
	f    *os.File
	w    *bufio.Writer
	sync int // ticks since last flush
}

// rotate closes the current segment and starts a new one from the given
// world, which must sit at a tick boundary (fresh worlds and swaps do).
// Failures disable recording for the segment and log once — replay is a
// debug aid, never worth stalling the game for.
func (r *replayLog) rotate(w *core.World) {
	r.close()
	data, err := w.Save()
	if err != nil {
		log.Printf("server: replay header: %v", err)
		return
	}
	r.seq++
	path := filepath.Join(r.dir, fmt.Sprintf("replay-i%d-s%d.ndjson", r.id, r.seq))
	f, err := os.Create(path)
	if err != nil {
		log.Printf("server: replay segment: %v", err)
		return
	}
	r.f = f
	r.w = bufio.NewWriter(f)
	head, _ := json.Marshal(ReplayHeader{Version: replayVersion, World: data})
	r.w.Write(head)
	r.w.WriteByte('\n')
}

// record appends one commanded tick. No-op while no segment is open.
func (r *replayLog) record(tick uint64, cmds []core.Command) {
	if r.w == nil || len(cmds) == 0 {
		return
	}
	line, err := json.Marshal(ReplayLine{Tick: tick, Commands: cmds})
	if err != nil {
		return
	}
	r.w.Write(line)
	r.w.WriteByte('\n')
	r.sync++
	if r.sync >= 30 { // ~1s of commanded ticks between flushes
		r.sync = 0
		r.w.Flush()
	}
}

func (r *replayLog) close() {
	if r.w != nil {
		r.w.Flush()
		r.f.Close()
		r.w, r.f = nil, nil
	}
}

// Replay re-executes a recorded segment against a content registry,
// calling observe (if set) after every tick. It returns the finished
// world — callers compare hashes, dump state, or bisect from there.
func Replay(db *core.ContentDB, path string, step func(w *core.World, cmds []core.Command), observe func(w *core.World)) (*core.World, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24) // headers embed whole worlds
	if !sc.Scan() {
		return nil, fmt.Errorf("server: replay %s: empty file", path)
	}
	var head ReplayHeader
	if err := json.Unmarshal(sc.Bytes(), &head); err != nil {
		return nil, fmt.Errorf("server: replay header: %w", err)
	}
	if head.Version != replayVersion {
		return nil, fmt.Errorf("server: replay version %d, this build reads %d", head.Version, replayVersion)
	}
	w, err := core.LoadWorld(db, head.World)
	if err != nil {
		return nil, err
	}
	for sc.Scan() {
		var line ReplayLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return nil, fmt.Errorf("server: replay line: %w", err)
		}
		for w.Tick+1 < line.Tick {
			step(w, nil)
			if observe != nil {
				observe(w)
			}
		}
		step(w, line.Commands)
		if observe != nil {
			observe(w)
		}
	}
	return w, sc.Err()
}
