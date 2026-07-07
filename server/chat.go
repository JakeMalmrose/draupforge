// Chat — the party line (ROADMAP v2 Track 2 item 8). The game is
// multiplayer; the players were mute. A "chat" command relays to everyone
// in the instance (party = instance, so party chat IS instance chat) as a
// JSON frame; a coordinate instead of text is a map ping, drawn as a pulse
// on everyone's floor. Named players speak, guests read; a per-client
// bucket keeps it from becoming a firehose. The sim never knows.
package server

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/JakeMalmrose/draupforge/protocol"
)

const (
	chatMaxLen     = 200 // runes; longer messages truncate
	chatRatePerSec = 1.5
	chatBurst      = 4
)

func newChatBucket() *tokenBucket {
	return &tokenBucket{tokens: chatBurst, max: chatBurst, rate: chatRatePerSec, last: time.Now()}
}

// chatWant is one client's harvested chat verbs for a tick.
type chatWant struct {
	c    *client
	msgs []protocol.ChatSnap // Text or Ping filled, Name stamped here
}

// cleanChatText trims, truncates, and strips control characters — the
// client renders via textContent so markup is inert, but newlines and
// terminal escapes have no business in a chat line either.
func cleanChatText(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		n++
		if n >= chatMaxLen {
			break
		}
	}
	return b.String()
}

// processChat relays this tick's chat verbs to the whole instance. Runs on
// the tick goroutine, no locks held.
func (in *Instance) processChat(wants []chatWant) {
	for _, w := range wants {
		if w.c.name == "" {
			continue // guests read; claim a name to speak
		}
		for _, m := range w.msgs {
			m.Name = w.c.name
			if m.Ping == nil {
				m.Text = cleanChatText(m.Text)
				if m.Text == "" {
					continue
				}
			}
			frame, _ := json.Marshal(protocol.ServerMsg{Type: "chat", Chat: &m})
			for _, c := range in.clients {
				if !c.send(frame, false) {
					c.tr.Close()
				}
			}
		}
	}
}
