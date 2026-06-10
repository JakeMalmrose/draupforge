# draupforge — Claude Code Context

A PoE-like ARPG built sim-first in Go. Orient in this order:

1. `README.md` — what this is, the locked architecture decisions, quickstart commands.
2. `DESIGN.md` — the foundational design: stat algebra, damage pipeline, determinism rules, package layout. Treat its decisions as settled unless Jake reopens them.
3. `STATUS.md` — current state, invariants, known shortcuts, and next steps. **Read this before starting work, and update it (plus its session log) before you finish.**

Ground rules:

- `go test ./...` must be green before any commit. The golden replay test failing means you changed behavior — if intentional, re-record per the instructions in STATUS.md and say so in the commit.
- Determinism is the product: no floats, wall-clock, unseeded randomness, or map iteration inside `sim/`.
- Work on feature branches off `main`.
