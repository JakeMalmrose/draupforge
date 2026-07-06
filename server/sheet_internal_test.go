package server

// The sheet contract: a "sheet" verb is answered after the tick with a
// "sheet" frame carrying the requesting client's computed stats.
// (Read-only-ness is pinned at the sim layer by TestBuildSheetReadOnly.)

import (
	"encoding/json"
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
)

func TestSheetVerbAnswers(t *testing.T) {
	in, c, tr := descentInstanceAt(t, 1, 0)
	c.mu.Lock()
	c.wantSheet = true
	c.mu.Unlock()
	in.tick()

	tr.mu.Lock()
	defer tr.mu.Unlock()
	for i := len(tr.frames) - 1; i >= 0; i-- {
		var msg protocol.ServerMsg
		if json.Unmarshal(tr.frames[i], &msg) == nil && msg.Type == "sheet" {
			if msg.Sheet == nil || len(msg.Sheet.Stats) == 0 {
				t.Fatalf("sheet frame carries no stats: %+v", msg.Sheet)
			}
			return
		}
	}
	t.Fatal("no sheet frame after a sheet verb")
}
