package server

// Transfer mechanics on the tick goroutine's data, no sockets or tickers:
// the character rides the move, the source forgets the client, the
// destination adopts it.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
)

// TestLobbyAdminInstanceAPI walks the path a browser takes from the lobby
// index into a live instance: a client joins (spawning instance 0), the
// dashboard page serves under /i/0/, and the instance API answers under
// the same prefix. The page must never reference absolute "/api/" paths —
// they escape the StripPrefix mount and 404 at the lobby root, which is
// exactly the regression this test pins.
func TestLobbyAdminInstanceAPI(t *testing.T) {
	lb, err := NewLobby(content.DB(), Config{
		Addr: "127.0.0.1:0", Seed: 5, TickInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go lb.ListenAndServe(ctx)
	addr := lb.Addr() // one-shot channel receive — capture it once
	if addr == nil {
		t.Fatal("lobby failed to listen")
	}
	// A TCP debug connection joins like a guest, spinning up instance 0.
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	admin := httptest.NewServer(lb.adminHandler())
	t.Cleanup(admin.Close)

	// The instance spins up with the join; poll briefly until it answers.
	deadline := time.Now().Add(5 * time.Second)
	var status *http.Response
	for {
		res, err := http.Get(admin.URL + "/i/0/api/status")
		if err == nil && res.StatusCode == http.StatusOK {
			status = res
			break
		}
		if err == nil {
			res.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatal("/i/0/api/status never answered through the lobby prefix")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var st struct {
		TickHzTarget float64 `json:"tick_hz_target"`
	}
	if err := json.NewDecoder(status.Body).Decode(&st); err != nil || st.TickHzTarget <= 0 {
		t.Fatalf("status JSON through the prefix: err %v, tick_hz_target %v", err, st.TickHzTarget)
	}
	status.Body.Close()

	page, err := http.Get(admin.URL + "/i/0/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("dashboard page = %d, want 200", page.StatusCode)
	}
	if strings.Contains(string(body), `"/api/`) {
		t.Error(`dashboard HTML references absolute "/api/" paths — they escape the /i/{id}/ mount and 404 at the lobby root`)
	}
	if !strings.Contains(string(body), `api("api/status`) {
		t.Error("dashboard HTML lost its relative api/status call — did the JS change shape?")
	}
}

// TestLobbyAdminIndex: the admin landing page lists live instances with
// links to their dashboards, and stray favicon fetches get a quiet 204
// instead of an error.
func TestLobbyAdminIndex(t *testing.T) {
	lb, _ := bareLobby(t, 2)
	ts := httptest.NewServer(lb.adminHandler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d, want 200", res.StatusCode)
	}
	for _, want := range []string{"draupforge lobby", `href="/i/0/"`, `href="/i/1/"`, "2 instance(s)"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("index missing %q", want)
		}
	}

	fav, err := http.Get(ts.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	fav.Body.Close()
	if fav.StatusCode != http.StatusNoContent {
		t.Errorf("favicon status = %d, want 204", fav.StatusCode)
	}
}

// TestLobbyLoadSeedsFirstInstance: -load under the lobby resumes the saved
// run in the first instance created — whoever connects first continues it —
// while every later instance is fresh, and a corrupt file fails the boot
// instead of the first join.
func TestLobbyLoadSeedsFirstInstance(t *testing.T) {
	db := content.DB()
	spec := &protocol.MapSpec{Width: 24, Height: 24, Rooms: 4}

	// A mid-run world worth resuming: floor 3, one portal use already spent.
	src, err := New(db, Config{Seed: 99, StartFloor: 3, Portals: 2, Map: spec})
	if err != nil {
		t.Fatal(err)
	}
	src.portalsLeft = 1
	world, err := src.sim.W.Save()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := src.encodeRunSave(world)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewLobby(db, Config{Addr: "127.0.0.1:0", Load: []byte("junk")}); err == nil {
		t.Fatal("a garbage -load booted a lobby; want a boot-time failure")
	}

	lb, err := NewLobby(db, Config{
		Addr: "127.0.0.1:0", Seed: 7, TickInterval: 2 * time.Millisecond,
		Map: spec, Portals: 3, Load: envelope,
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

	welcome := func(which string) *protocol.RunSnap {
		t.Helper()
		wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer wcancel()
		ws, _, err := websocket.Dial(wctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })
		for {
			kind, data, err := ws.Read(wctx)
			if err != nil {
				t.Fatalf("%s: waiting for welcome: %v", which, err)
			}
			if kind != websocket.MessageText {
				continue
			}
			var msg protocol.ServerMsg
			if json.Unmarshal(data, &msg) == nil && msg.Type == "welcome" {
				if msg.Run == nil {
					t.Fatalf("%s: welcome carries no run state", which)
				}
				return msg.Run
			}
		}
	}

	first := welcome("first client")
	if first.Floor != 3 || first.Portals != 1 || first.Best != 3 {
		t.Errorf("first client resumed floor %d, portals %d, best %d; want 3, 1, 3",
			first.Floor, first.Portals, first.Best)
	}
	second := welcome("second client")
	if second.Floor != 0 || second.Portals != 3 {
		t.Errorf("second client got floor %d, portals %d; want a fresh hideout (0, 3)",
			second.Floor, second.Portals)
	}
}

// bareLobby wires a lobby and n hand-driven instances (no goroutines — the
// test calls tick itself).
func bareLobby(t *testing.T, n int) (*Lobby, []*Instance) {
	t.Helper()
	lb, err := NewLobby(content.DB(), Config{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	lb.ctx = context.Background()
	var ins []*Instance
	for i := 0; i < n; i++ {
		in, err := New(content.DB(), Config{Seed: uint64(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		in.ids = lb.ids
		in.lobby = lb
		in.id = i
		in.publishParty()
		lb.instances[i] = &instanceRef{in: in, cancel: func() {}}
		ins = append(ins, in)
	}
	return lb, ins
}

func TestTransferCarriesCharacter(t *testing.T) {
	lb, ins := bareLobby(t, 2)
	from, to := ins[0], ins[1]

	c := namedClient(t, from, "Mover")
	c.inst = from
	if !from.spawnClient(c) {
		t.Fatal("spawn refused")
	}
	from.publishParty()
	lb.online[c.token] = c
	from.sim.W.ActorByID(c.actor).XP = 777

	lb.mu.Lock()
	lb.transferLocked(from, to, c)
	lb.mu.Unlock()

	if got := len(from.clients); got != 0 {
		t.Fatalf("source still holds %d client(s)", got)
	}
	to.tick() // adoption happens on the destination's tick
	if got := len(to.clients); got != 1 {
		t.Fatalf("destination holds %d client(s), want 1", got)
	}
	a := to.sim.W.ActorByID(c.actor)
	if a == nil || a.XP != 777 {
		t.Fatalf("transferred actor = %+v, want XP 777", a)
	}
	// The identity never blinked offline: reconnecting is still a dup.
	if _, _, ok, dup := lb.ids.Connect(c.token); ok || !dup {
		t.Fatalf("identity mid-party: ok=%v dup=%v, want online (dup)", ok, dup)
	}
	// And home now points at the destination.
	if lb.homes[c.token] != to {
		t.Error("home was not moved to the destination instance")
	}
}
