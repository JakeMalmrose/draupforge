// Command server hosts a lobby of world instances: WebSocket + web client
// on -http, a TCP/NDJSON debug wire on -addr. Poke the debug wire by hand:
//
//	go run ./cmd/server -addr :7777 -scenario scripts/arena.json
//	nc localhost 7777
//	{"kind":"move","x":5000,"y":0}
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

func main() {
	addr := flag.String("addr", ":7777", "TCP/NDJSON listen address")
	httpAddr := flag.String("http", ":8080", "HTTP listen address for /ws and the web client (\"\" disables)")
	adminAddr := flag.String("admin", ":9090", "admin dashboard/API listen address (\"\" disables) — no auth, keep it off the open internet")
	webDir := flag.String("web", "web", "static web client directory")
	seed := flag.Uint64("seed", 0, "world seed; 0 rolls a random one each boot (logged, reproducible)")
	scenario := flag.String("scenario", "", "scenario script (JSON); only spawns are used")
	load := flag.String("load", "", "world save file to restore (admin /api/save writes them); overrides -seed and -scenario")
	sendEvery := flag.Int("sendevery", 3, "send a view every N sim ticks (3 = 10Hz at the 30Hz sim)")
	interest := flag.Int64("interest", 60, "interest radius in world units for WS clients (0 = whole world)")
	portals := flag.Int("portals", 3, "portal uses per descent run (deaths and hideout trips consume them)")
	identities := flag.String("identities", "identities.json", "named-player store; \"\" keeps identities in memory only")
	startFloor := flag.Int("startfloor", 0, "floor runs begin on (0 = the hideout) — a dev shortcut to deep floors")
	origins := flag.String("origins", "", "extra allowed WebSocket origins, comma-separated host[:port] patterns (default: same-origin only)")
	replayDir := flag.String("replaydir", "", "record every world as a replayable segment in this directory (cmd/headless -replay re-executes one)")
	flag.Parse()

	cfg := server.Config{
		Addr: *addr, HTTPAddr: *httpAddr, AdminAddr: *adminAddr, StaticDir: *webDir,
		Seed: *seed, SendEvery: *sendEvery, InterestRadius: *interest * 1000,
		Portals: *portals, IdentityPath: *identities, StartFloor: *startFloor,
		ReplayDir: *replayDir,
	}
	if *origins != "" {
		for _, o := range strings.Split(*origins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.WSOrigins = append(cfg.WSOrigins, o)
			}
		}
	}
	if *load != "" {
		raw, err := os.ReadFile(*load)
		if err != nil {
			fatal(err)
		}
		cfg.Load = raw
	}
	if *scenario != "" {
		raw, err := os.ReadFile(*scenario)
		if err != nil {
			fatal(err)
		}
		var script protocol.Script
		if err := json.Unmarshal(raw, &script); err != nil {
			fatal(fmt.Errorf("parsing scenario: %w", err))
		}
		cfg.Map = script.Map
		cfg.Spawns = script.Spawns
		cfg.Scatter = script.Scatter
	}

	lb, err := server.NewLobby(content.DB(), cfg)
	if err != nil {
		fatal(err)
	}
	go func() {
		fmt.Println("tcp listening on", lb.Addr())
		if *httpAddr != "" {
			fmt.Printf("web client on http://localhost%s\n", *httpAddr)
		}
		if *adminAddr != "" {
			fmt.Printf("admin dashboard on http://localhost%s\n", *adminAddr)
		}
	}()
	if err := lb.ListenAndServe(context.Background()); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "server:", err)
	os.Exit(1)
}
