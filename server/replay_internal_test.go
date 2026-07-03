package server

// The replay log's contract: a recorded segment re-executes to the exact
// world the live instance reached — including across host surgery (a floor
// swap), which rotates to a fresh segment so every file spans a pure
// command-driven stretch.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func TestReplayReproducesLive(t *testing.T) {
	dir := t.TempDir()
	in, c, _ := descentInstance(t, 3)
	in.replay = &replayLog{dir: dir, id: in.id}
	in.surgery = true

	push := func(cmd core.Command) {
		cmd.Actor = c.actor
		in.mu.Lock()
		in.pending = append(in.pending, cmd)
		in.mu.Unlock()
	}

	// Segment 1: wander on floor 1.
	for i := 0; i < 40; i++ {
		if i%10 == 0 {
			push(core.Command{Kind: core.CmdMove, Point: space.V(fm.FromInt(int64(5+i)), fm.FromInt(5))})
		}
		in.tick()
	}
	// Host surgery: descend to floor 2 — the recorder must rotate.
	in.descend()
	// Segment 2: wander below.
	for i := 0; i < 40; i++ {
		if i%10 == 5 {
			push(core.Command{Kind: core.CmdMove, Point: space.V(fm.FromInt(int64(3+i)), fm.FromInt(7))})
		}
		in.tick()
	}
	in.replay.close()
	liveTick, liveHash := in.sim.W.Tick, in.sim.W.Hash()

	files, err := filepath.Glob(filepath.Join(dir, "replay-*.ndjson"))
	if err != nil || len(files) < 2 {
		names, _ := os.ReadDir(dir)
		t.Fatalf("want ≥2 segments (the swap rotates), got %d (%v, err %v)", len(files), names, err)
	}
	sort.Strings(files)

	step := func(w *core.World, cmds []core.Command) { (&sim.Sim{W: w}).Step(cmds) }
	w, err := Replay(content.DB(), files[len(files)-1], step, nil)
	if err != nil {
		t.Fatal(err)
	}
	for w.Tick < liveTick {
		step(w, nil)
	}
	if w.Tick != liveTick || w.Hash() != liveHash {
		t.Fatalf("replayed tick %d hash %016x; live tick %d hash %016x",
			w.Tick, w.Hash(), liveTick, liveHash)
	}
}
