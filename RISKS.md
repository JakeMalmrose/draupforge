# Architectural Risk Register

Known structural risks, ranked by how hard they'd be to recover from — not a
TODO list. Re-rank when one gets mitigated or a new one shows up; delete
entries that stop being true (git history is the archive of what got fixed
and why).

## 1. Action model is one-thing-at-a-time

An actor is idle, moving, or using one skill. Timed statuses (ailments,
content-defined buffs) exist, but there is no channelling, no
cast-while-moving, and no stun/interrupt semantics. Action semantics leak
into AI, command validation, and netcode feel — grow them deliberately, not
mid-feature.

## Smaller, recoverable (listed for honesty)

- Reconnect/session identity doesn't exist: disconnect still deletes the
  actor and its items (worlds survive restarts via save/load, players
  don't). Needs an account/session layer on top of persistence.
- Content-as-Go-code: balance changes need recompile+redeploy. Fine until
  live-ops; migration to data files is mechanical.
- Collision/AoE are O(actors×projectiles) scans; add a spatial grid when
  density demands it.
- Pointer-heavy sim state will eventually make the GC visible. Game clients
  get binary deltas; the debug wires (TCP, ?format=json) still pay a
  per-tick JSON marshal.
- protocol/binary.go and web/net.js are a hand-maintained codec pair — drift
  is caught only by the version bump discipline and play-testing, not by a
  shared schema or a cross-language test in CI.
- Replays/goldens are version-locked to the code that produced them. Fine as
  tests; never let anything durable (saves, trade history) depend on
  replay-by-reexecution.
- `Fixed` is for sim quantities only — `fixmath.Mul` panics loudly on
  overflow rather than corrupting, but accumulators/meta-stats (total
  damage, economy counters, XP) should be plain integers with their own
  range analysis.

## Parked here (not a risk): server operations leftovers

- In-process load/rollback without a restart: needs re-welcoming every
  client (terrain and actor IDs change under them). Save via the admin
  dashboard + restart with `cmd/server -load` is the rollback story until
  that's worth building.
- No auth: the admin port must stay on localhost/tailnet until an auth
  story exists. Adjustable tick rate also still missing.

## Non-risks (deliberate, stop re-evaluating)

- Single-threaded sim per instance — instances scale horizontally (PoE's own
  model).
- The determinism tax (no floats/maps/wall-clock) — keeps paying for itself.
- Go — nothing above is language-shaped.
