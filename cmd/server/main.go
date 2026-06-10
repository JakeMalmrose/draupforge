// Command server hosts one map instance over TCP/NDJSON. Poke it by hand:
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

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
)

func main() {
	addr := flag.String("addr", ":7777", "TCP/NDJSON listen address")
	httpAddr := flag.String("http", ":8080", "HTTP listen address for /ws and the web client (\"\" disables)")
	webDir := flag.String("web", "web", "static web client directory")
	seed := flag.Uint64("seed", 1, "world seed")
	scenario := flag.String("scenario", "", "scenario script (JSON); only spawns are used")
	sendEvery := flag.Int("sendevery", 3, "send a view every N sim ticks (3 = 10Hz at the 30Hz sim)")
	interest := flag.Int64("interest", 60, "interest radius in world units for WS clients (0 = whole world)")
	flag.Parse()

	cfg := server.Config{
		Addr: *addr, HTTPAddr: *httpAddr, StaticDir: *webDir, Seed: *seed,
		SendEvery: *sendEvery, InterestRadius: *interest * 1000,
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
		cfg.Spawns = script.Spawns
	}

	in, err := server.New(content.DB(), cfg)
	if err != nil {
		fatal(err)
	}
	go func() {
		fmt.Println("tcp listening on", in.Addr())
		if *httpAddr != "" {
			fmt.Printf("web client on http://localhost%s\n", *httpAddr)
		}
	}()
	if err := in.ListenAndServe(context.Background()); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "server:", err)
	os.Exit(1)
}
