package server

// Transfer mechanics on the tick goroutine's data, no sockets or tickers:
// the character rides the move, the source forgets the client, the
// destination adopts it.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
)

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
