package server_test

// The party lifecycle over real sockets: solo worlds on connect, invite →
// accept moves the acceptor into the inviter's world, leave_party moves
// them back out, and a reconnect inside the grace window goes home.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

func startLobby(t *testing.T) string {
	t.Helper()
	lb, err := server.NewLobby(content.DB(), server.Config{
		Addr:         "127.0.0.1:0",
		TickInterval: 2 * time.Millisecond,
		IdentityPath: filepath.Join(t.TempDir(), "ids.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go lb.ListenAndServe(ctx)
	if lb.Addr() == nil {
		t.Fatal("lobby failed to listen")
	}
	hs := httptest.NewServer(lb.Handler())
	t.Cleanup(hs.Close)
	return hs.URL
}

// socialConn is one named player's socket plus the latest social state.
type socialConn struct {
	t    *testing.T
	ws   *websocket.Conn
	last protocol.ServerMsg // most recent frame of each read
}

func dialLobby(t *testing.T, base string, cookie *http.Cookie) *socialConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hdr := http.Header{}
	if cookie != nil {
		hdr.Set("Cookie", cookie.String())
	}
	url := "ws" + strings.TrimPrefix(base, "http") + "/ws"
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })
	return &socialConn{t: t, ws: ws}
}

// until reads frames (skipping binary views) until pred says yes.
func (sc *socialConn) until(what string, pred func(protocol.ServerMsg) bool) protocol.ServerMsg {
	sc.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		kind, data, err := sc.ws.Read(ctx)
		if err != nil {
			sc.t.Fatalf("waiting for %s: %v", what, err)
		}
		if kind != websocket.MessageText {
			continue
		}
		var msg protocol.ServerMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		sc.last = msg
		if pred(msg) {
			return msg
		}
	}
}

func (sc *socialConn) send(cmd protocol.Command) {
	sc.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, _ := json.Marshal(cmd)
	if err := sc.ws.Write(ctx, websocket.MessageText, raw); err != nil {
		sc.t.Fatal(err)
	}
}

func rosterNames(msg protocol.ServerMsg) []string {
	var names []string
	for _, n := range msg.Roster {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func TestPartyLifecycle(t *testing.T) {
	base := startLobby(t)
	alice := dialLobby(t, base, claim(t, base, "Alice"))
	aw := alice.until("alice welcome", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })
	if got := rosterNames(aw); !slices.Equal(got, []string{"Alice"}) {
		t.Fatalf("alice spawns with roster %v, want her own world", got)
	}

	bob := dialLobby(t, base, claim(t, base, "Bob"))
	bw := bob.until("bob welcome", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })
	if got := rosterNames(bw); !slices.Equal(got, []string{"Bob"}) {
		t.Fatalf("bob spawns with roster %v, want his own world", got)
	}

	// The default-visible friends list: each sees the other online.
	alice.until("alice sees bob online", func(m protocol.ServerMsg) bool {
		return m.Type == "social" && slices.Contains(m.Social.Online, "Bob")
	})

	// Invite → the invitee's social frame names the inviter.
	alice.send(protocol.Command{Kind: "invite", Name: "Bob"})
	bob.until("bob sees the invite", func(m protocol.ServerMsg) bool {
		return m.Type == "social" && m.Social.Invite == "Alice"
	})

	// Accept → re-welcome into Alice's world, both names in the roster.
	bob.send(protocol.Command{Kind: "accept_invite"})
	joined := bob.until("bob joins alice's world", func(m protocol.ServerMsg) bool {
		return m.Type == "welcome" && len(m.Roster) == 2
	})
	if got := rosterNames(joined); !slices.Equal(got, []string{"Alice", "Bob"}) {
		t.Fatalf("party roster = %v, want Alice+Bob", got)
	}
	bob.until("bob's party view", func(m protocol.ServerMsg) bool {
		if m.Type != "social" {
			return false
		}
		p := slices.Clone(m.Social.Party)
		slices.Sort(p)
		return slices.Equal(p, []string{"Alice", "Bob"})
	})

	// Leave → a fresh solo world.
	bob.send(protocol.Command{Kind: "leave_party"})
	solo := bob.until("bob back solo", func(m protocol.ServerMsg) bool {
		return m.Type == "welcome" && len(m.Roster) == 1
	})
	if got := rosterNames(solo); !slices.Equal(got, []string{"Bob"}) {
		t.Fatalf("post-leave roster = %v, want just Bob", got)
	}
}

func TestReconnectGoesHome(t *testing.T) {
	base := startLobby(t)
	carol := dialLobby(t, base, claim(t, base, "Carol"))
	carol.until("carol welcome", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })
	daveCookie := claim(t, base, "Dave")
	dave := dialLobby(t, base, daveCookie)
	dave.until("dave welcome", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })

	carol.send(protocol.Command{Kind: "invite", Name: "Dave"})
	dave.until("dave invited", func(m protocol.ServerMsg) bool {
		return m.Type == "social" && m.Social.Invite == "Carol"
	})
	dave.send(protocol.Command{Kind: "accept_invite"})
	dave.until("dave in party", func(m protocol.ServerMsg) bool {
		return m.Type == "welcome" && len(m.Roster) == 2
	})

	// Dave drops and comes right back: same world, Carol still there.
	dave.ws.Close(websocket.StatusNormalClosure, "")
	back := dialLobby(t, base, daveCookie)
	rw := back.until("dave rejoins", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })
	if got := rosterNames(rw); !slices.Equal(got, []string{"Carol", "Dave"}) {
		t.Fatalf("reconnect roster = %v, want back home with Carol", got)
	}
}

func TestGuestsAreInvisibleToParties(t *testing.T) {
	base := startLobby(t)
	// A guest and a named player: separate worlds, and the named player's
	// online list stays empty (guests aren't listed).
	guest := dialLobby(t, base, nil)
	gw := guest.until("guest welcome", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })
	if len(gw.Roster) != 0 || gw.Name != "" {
		t.Fatalf("guest welcome roster=%v name=%q, want anonymous solo", gw.Roster, gw.Name)
	}
	erin := dialLobby(t, base, claim(t, base, "Erin"))
	ew := erin.until("erin welcome", func(m protocol.ServerMsg) bool { return m.Type == "welcome" })
	if got := rosterNames(ew); !slices.Equal(got, []string{"Erin"}) {
		t.Fatalf("erin roster = %v, want her own world", got)
	}
	erin.until("erin's empty friends list", func(m protocol.ServerMsg) bool {
		return m.Type == "social" && len(m.Social.Online) == 0
	})
}
