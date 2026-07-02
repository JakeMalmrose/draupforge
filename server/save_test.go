package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// savedWorldWithPlayer builds a world holding a geared player and a zombie,
// runs one tick, and returns its save.
func savedWorldWithPlayer(t *testing.T) []byte {
	t.Helper()
	db := content.DB()
	s := sim.New(db, 42)
	w := s.W
	player := w.SpawnActor(db.Actors["player"], space.Vec2{})
	w.SpawnActor(db.Actors["zombie"], space.V(10000, 0))

	sword := core.Item{ID: w.AllocID(), Base: db.BaseItems["rusty_sword"]}
	ring := core.Item{ID: w.AllocID(), Base: db.BaseItems["iron_ring"]}
	player.Inventory = append(player.Inventory, sword, ring)
	if !items.Equip(w, player, sword.ID, core.EquipAuto) {
		t.Fatal("test setup: equip failed")
	}
	s.Step(nil)

	data, err := w.Save()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestLoadReclaimsOrphanPlayers: a server started from a save removes
// player actors it has no session for, dropping their gear where they stood.
func TestLoadReclaimsOrphanPlayers(t *testing.T) {
	in, err := New(content.DB(), Config{Addr: "127.0.0.1:0", Seed: 1, Load: savedWorldWithPlayer(t)})
	if err != nil {
		t.Fatal(err)
	}
	w := in.sim.W
	for _, a := range w.Actors {
		if a.Def.ID == "player" {
			t.Error("orphan player actor survived the load")
		}
	}
	if len(w.Actors) != 1 {
		t.Errorf("world has %d actors, want just the zombie", len(w.Actors))
	}
	if len(w.Drops) != 2 {
		t.Fatalf("world has %d drops, want the orphan's sword and ring", len(w.Drops))
	}
}

// TestLoadRejectsBadSave: a corrupt save fails server construction loudly.
func TestLoadRejectsBadSave(t *testing.T) {
	if _, err := New(content.DB(), Config{Addr: "127.0.0.1:0", Seed: 1, Load: []byte("not json")}); err == nil {
		t.Error("server started from a corrupt save")
	}
}

// TestAdminSaveEndpoint: POST /api/save writes a file LoadWorld accepts —
// the full operate-tier loop (save from the dashboard, restart with -load).
func TestAdminSaveEndpoint(t *testing.T) {
	_, ts := startAdminInstance(t, nil)
	path := filepath.Join(t.TempDir(), "world.json")

	body, _ := json.Marshal(map[string]string{"path": path})
	res, err := http.Post(ts.URL+"/api/save", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/save = %d", res.StatusCode)
	}
	var reply struct {
		Path string `json:"path"`
		Tick uint64 `json:"tick"`
	}
	if err := json.NewDecoder(res.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(reply.Path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := core.LoadWorld(content.DB(), data)
	if err != nil {
		t.Fatalf("admin save does not load back: %v", err)
	}
	if w.Tick != reply.Tick {
		t.Errorf("restored tick %d, reply said %d", w.Tick, reply.Tick)
	}
}
