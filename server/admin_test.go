package server

// Internal tests: they hit adminMux through httptest instead of binding the
// real admin port, and reach into the instance only via the HTTP surface.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
)

// startAdminInstance boots a fast-ticking instance and an httptest server
// for its admin mux. Addr() is a one-shot channel receive, so the game
// listener's address is captured here and returned.
func startAdminInstance(t *testing.T, spawns []protocol.ScriptSpawn) (net.Addr, *httptest.Server) {
	t.Helper()
	in, err := New(content.DB(), Config{
		Addr:         "127.0.0.1:0",
		Seed:         1,
		TickInterval: 2 * time.Millisecond,
		Spawns:       spawns,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go in.ListenAndServe(ctx)
	addr := in.Addr()
	if addr == nil {
		t.Fatal("server failed to listen")
	}
	ts := httptest.NewServer(in.adminMux())
	t.Cleanup(ts.Close)
	return addr, ts
}

func adminGET(t *testing.T, ts *httptest.Server, path string, out any) {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func adminPOST(t *testing.T, ts *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestAdminStatus(t *testing.T) {
	_, ts := startAdminInstance(t, []protocol.ScriptSpawn{
		{Def: "training_dummy", X: 50000, Y: 0},
		{Def: "zombie", X: 60000, Y: 0},
	})

	var st adminStatus
	adminGET(t, ts, "/api/status", &st)
	if st.Actors != 2 {
		t.Errorf("actors = %d, want the 2 scenario spawns", st.Actors)
	}
	if st.Paused {
		t.Error("instance reports paused at boot")
	}
	if st.WorldHash == "" || len(st.ActorDefs) == 0 || len(st.CutSkills) == 0 {
		t.Errorf("missing observability fields: hash=%q defs=%v cuttable=%v",
			st.WorldHash, st.ActorDefs, st.CutSkills)
	}
	if st.Run != nil {
		t.Errorf("plain arena reports run state %+v", st.Run)
	}

	// The world must be advancing.
	time.Sleep(20 * time.Millisecond)
	var st2 adminStatus
	adminGET(t, ts, "/api/status", &st2)
	if st2.Tick <= st.Tick {
		t.Errorf("tick did not advance: %d → %d", st.Tick, st2.Tick)
	}
}

func TestAdminPauseFreezesWorld(t *testing.T) {
	_, ts := startAdminInstance(t, nil)

	if res := adminPOST(t, ts, "/api/pause", map[string]bool{"paused": true}); res.StatusCode != http.StatusOK {
		t.Fatalf("pause = %d", res.StatusCode)
	}
	var st adminStatus
	adminGET(t, ts, "/api/status", &st)
	if !st.Paused {
		t.Fatal("status not paused after /api/pause")
	}
	time.Sleep(30 * time.Millisecond)
	var st2 adminStatus
	adminGET(t, ts, "/api/status", &st2)
	if st2.Tick != st.Tick {
		t.Errorf("world advanced while paused: tick %d → %d", st.Tick, st2.Tick)
	}

	if res := adminPOST(t, ts, "/api/pause", map[string]bool{"paused": false}); res.StatusCode != http.StatusOK {
		t.Fatalf("resume = %d", res.StatusCode)
	}
	time.Sleep(20 * time.Millisecond)
	var st3 adminStatus
	adminGET(t, ts, "/api/status", &st3)
	if st3.Paused || st3.Tick <= st2.Tick {
		t.Errorf("world did not resume: paused=%v tick %d → %d", st3.Paused, st2.Tick, st3.Tick)
	}
}

func TestAdminSpawnAndKick(t *testing.T) {
	gameAddr, ts := startAdminInstance(t, nil)

	res := adminPOST(t, ts, "/api/spawn", map[string]any{"def": "zombie", "X": 5000, "Y": 0})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("spawn = %d", res.StatusCode)
	}
	var st adminStatus
	adminGET(t, ts, "/api/status", &st)
	if st.Actors != 1 {
		t.Errorf("actors = %d after spawn, want 1", st.Actors)
	}
	if res := adminPOST(t, ts, "/api/spawn", map[string]any{"def": "no_such_def"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad-def spawn = %d, want 400", res.StatusCode)
	}

	// Connect a TCP client, then kick it through the API.
	conn, err := net.Dial("tcp", gameAddr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 4*1024*1024)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !sc.Scan() {
		t.Fatal("no welcome")
	}
	var welcome protocol.ServerMsg
	if err := json.Unmarshal(sc.Bytes(), &welcome); err != nil || welcome.Type != "welcome" {
		t.Fatalf("welcome = %s (%v)", sc.Bytes(), err)
	}

	if res := adminPOST(t, ts, "/api/kick", map[string]uint64{"actor": welcome.Actor}); res.StatusCode != http.StatusOK {
		t.Fatalf("kick = %d", res.StatusCode)
	}
	// The connection must die promptly.
	for sc.Scan() {
	}
	// The leave lands on the next tick; wait for the roster to empty before
	// asserting that re-kicking fails.
	deadline := time.Now().Add(5 * time.Second)
	for {
		adminGET(t, ts, "/api/status", &st)
		if len(st.Clients) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("kicked client still on the roster: %+v", st.Clients)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if res := adminPOST(t, ts, "/api/kick", map[string]uint64{"actor": welcome.Actor}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("kicking a gone client = %d, want 400", res.StatusCode)
	}
}

// TestAdminCheats: the dev pokes — force-cut gems, the god-mode toggle, orb
// grants — hold their HTTP contracts. Spawn order makes the player actor 1.
func TestAdminCheats(t *testing.T) {
	_, ts := startAdminInstance(t, []protocol.ScriptSpawn{
		{Def: "player", X: 0, Y: 0},
	})

	// Force-cut grants once, rejects the duplicate and unknown actors.
	if res := adminPOST(t, ts, "/api/gem", map[string]any{"actor": 1, "skill": "arc", "level": 5}); res.StatusCode != http.StatusOK {
		t.Fatalf("gem = %d", res.StatusCode)
	}
	if res := adminPOST(t, ts, "/api/gem", map[string]any{"actor": 1, "skill": "arc"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("duplicate gem = %d, want 400", res.StatusCode)
	}
	if res := adminPOST(t, ts, "/api/gem", map[string]any{"actor": 99, "skill": "spark"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("gem for unknown actor = %d, want 400", res.StatusCode)
	}

	// God mode toggles on, then off.
	var god struct {
		God bool `json:"god"`
	}
	res := adminPOST(t, ts, "/api/god", map[string]any{"actor": 1})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("god = %d", res.StatusCode)
	}
	if json.NewDecoder(res.Body).Decode(&god); !god.God {
		t.Error("first /api/god did not enable")
	}
	res = adminPOST(t, ts, "/api/god", map[string]any{"actor": 1})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("god (second) = %d", res.StatusCode)
	}
	if json.NewDecoder(res.Body).Decode(&god); god.God {
		t.Error("second /api/god did not toggle off")
	}

	// Orbs accumulate; unknown kinds are rejected.
	var orbs struct {
		Count int32 `json:"count"`
	}
	res = adminPOST(t, ts, "/api/orbs", map[string]any{"actor": 1, "orb": "jeweller", "count": 10})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("orbs = %d", res.StatusCode)
	}
	if json.NewDecoder(res.Body).Decode(&orbs); orbs.Count != 10 {
		t.Errorf("orb count = %d, want 10", orbs.Count)
	}
	res = adminPOST(t, ts, "/api/orbs", map[string]any{"actor": 1, "orb": "jeweller", "count": 10})
	if json.NewDecoder(res.Body).Decode(&orbs); orbs.Count != 20 {
		t.Errorf("orb count after second grant = %d, want 20", orbs.Count)
	}
	if res := adminPOST(t, ts, "/api/orbs", map[string]any{"actor": 1, "orb": "nope"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown orb kind = %d, want 400", res.StatusCode)
	}
}

// TestPauseFrameReachesClients: clients learn about pause transitions, and a
// client that joins mid-pause is told immediately after its welcome.
func TestPauseFrameReachesClients(t *testing.T) {
	gameAddr, ts := startAdminInstance(t, nil)

	readPause := func(sc *bufio.Scanner, conn net.Conn, want bool) error {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for sc.Scan() {
			var msg protocol.ServerMsg
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				return err
			}
			if msg.Type == "pause" {
				if msg.Paused == nil || *msg.Paused != want {
					return fmt.Errorf("pause frame %s, want paused=%v", sc.Bytes(), want)
				}
				return nil
			}
		}
		return fmt.Errorf("stream ended before pause frame: %v", sc.Err())
	}

	dial := func() (net.Conn, *bufio.Scanner) {
		conn, err := net.Dial("tcp", gameAddr.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 4096), 4*1024*1024)
		return conn, sc
	}

	connA, scA := dial()
	adminPOST(t, ts, "/api/pause", map[string]bool{"paused": true})
	if err := readPause(scA, connA, true); err != nil {
		t.Errorf("existing client: %v", err)
	}

	connB, scB := dial()
	if err := readPause(scB, connB, true); err != nil {
		t.Errorf("mid-pause joiner: %v", err)
	}

	adminPOST(t, ts, "/api/pause", map[string]bool{"paused": false})
	if err := readPause(scA, connA, false); err != nil {
		t.Errorf("resume: %v", err)
	}
}
