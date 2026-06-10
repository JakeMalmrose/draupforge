# draupforge

A PoE-like ARPG, starting from the simulation engine outward.

## Architecture decisions (locked in 2026-06-10)

- **Headless sim core.** This repo owns game rules only: entities, combat/damage
  calculation, items + affixes, skills, status effects, maps/pathing, and the tick
  loop. No rendering, no input handling, no graphics dependencies in the core.
  Everything is unit-testable.
- **Server-authoritative from day one.** The sim core is hosted by a server; clients
  send commands and receive state snapshots. The server is the single source of truth
  for combat, loot, and economy.
- **Deterministic simulation.** Fixed tick rate, command-based input, seeded RNG.
  Same inputs + same seed = same outcome, always. This is what makes server
  authority, replays, and regression-testing combat math possible.
- **Rendering deliberately undecided.** The core exposes snapshots over a clean
  boundary; debug visualization first, real client (2D or 3D, any stack) later.
- **Language: Go.** Fast compile/test loop, easy concurrency for the server layer.

## Layout

```
draupforge/
├── sim/        # Deterministic sim core — pure, no I/O (see DESIGN.md for internals)
├── content/    # Game data as typed Go literals: skills, monsters, affix pools
├── protocol/   # Command + snapshot wire types, versioned; binary delta view codec
├── server/     # Hosts the sim over TCP/NDJSON (debug) + WebSocket: per-client delta views
├── web/        # Browser client: canvas renderer + WebSocket, no build step
├── scripts/    # Scenario scripts for the headless runner
└── cmd/        # Entrypoints: headless debug runner, server binary
```

## Quickstart

```sh
go test ./...                                          # full suite incl. golden replay
go run ./cmd/headless -script scripts/slice.json       # watch the vertical slice fight
go run ./cmd/headless -script scripts/slice.json -hash # per-tick state hashes

go run ./cmd/server -scenario scripts/arena.json       # host an instance
open http://localhost:8080                             # play it in the browser
echo '{"kind":"move","x":5000,"y":0}' | nc localhost 7777   # or be a brave TCP client
```

Intentional behavior changes re-record the golden trace:
`DRAUPFORGE_UPDATE_GOLDEN=1 go test ./sim/ -run TestGoldenReplay`

See `DESIGN.md` for the foundational decisions (stat algebra, damage
pipeline, determinism rules, package layout).

## Conventions

- The sim core never imports from `server/` or anything that does I/O.
- No wall-clock time or unseeded randomness inside `sim/` — ticks and seeds come
  in from outside.
