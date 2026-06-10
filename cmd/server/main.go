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
	addr := flag.String("addr", ":7777", "listen address")
	seed := flag.Uint64("seed", 1, "world seed")
	scenario := flag.String("scenario", "", "scenario script (JSON); only spawns are used")
	flag.Parse()

	cfg := server.Config{Addr: *addr, Seed: *seed}
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
	go func() { fmt.Println("listening on", in.Addr()) }()
	if err := in.ListenAndServe(context.Background()); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "server:", err)
	os.Exit(1)
}
