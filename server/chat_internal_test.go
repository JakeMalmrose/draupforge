package server

// Chat unit tests: relay to the whole instance, guests read-only, the
// text scrub, and pings.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JakeMalmrose/draupforge/protocol"
)

func TestChatRelayAndScrub(t *testing.T) {
	in, c, tr := descentInstance(t, 3)
	c.name = "Talker"
	guest := &client{tr: &fakeTransport{}, mode: modeBinary}
	if !in.spawnClient(guest) {
		t.Fatal("guest spawn refused")
	}

	tr.mu.Lock()
	tr.frames = nil
	tr.mu.Unlock()
	in.processChat([]chatWant{{c: c, msgs: []protocol.ChatSnap{
		{Text: "  hello\x1b[31m down\nthere  "},
		{Text: strings.Repeat("x", 500)},
		{Text: "   "},
		{Ping: &protocol.Vec{X: 5000, Y: 6000}},
	}}})

	collect := func(f *fakeTransport) []protocol.ChatSnap {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []protocol.ChatSnap
		for _, raw := range f.frames {
			var msg protocol.ServerMsg
			if json.Unmarshal(raw, &msg) == nil && msg.Type == "chat" {
				out = append(out, *msg.Chat)
			}
		}
		return out
	}
	got := collect(tr)
	if len(got) != 3 {
		t.Fatalf("relayed %d messages, want 3 (blank line dropped)", len(got))
	}
	if got[0].Name != "Talker" || got[0].Text != "hello[31m downthere" {
		t.Errorf("scrubbed line = %+v, want control chars stripped", got[0])
	}
	if len(got[1].Text) != chatMaxLen {
		t.Errorf("long line = %d runes, want truncated to %d", len(got[1].Text), chatMaxLen)
	}
	if got[2].Ping == nil || got[2].Ping.X != 5000 {
		t.Errorf("ping = %+v, want the coordinate", got[2])
	}
	// The guest heard everything too — party = instance.
	if guestGot := collect(guest.tr.(*fakeTransport)); len(guestGot) != 3 {
		t.Errorf("guest heard %d messages, want 3", len(guestGot))
	}

	// Guests are read-only: their sends drop.
	tr.mu.Lock()
	tr.frames = nil
	tr.mu.Unlock()
	in.processChat([]chatWant{{c: guest, msgs: []protocol.ChatSnap{{Text: "boo"}}}})
	if got := collect(tr); len(got) != 0 {
		t.Errorf("a guest spoke: %+v", got)
	}
}
