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

## Planned layout (subject to change)

```
draupforge/
├── sim/        # Deterministic sim core — pure, no I/O
├── server/     # Hosts the sim: sessions, command intake, snapshot broadcast
├── protocol/   # Command + snapshot wire types shared with future clients
└── cmd/        # Entrypoints (server binary, debug/headless runners)
```

## Conventions

- The sim core never imports from `server/` or anything that does I/O.
- No wall-clock time or unseeded randomness inside `sim/` — ticks and seeds come
  in from outside.
