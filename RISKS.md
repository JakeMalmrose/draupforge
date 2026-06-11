# Architectural Risk Register

Known structural risks, ranked by how hard they'd be to recover from — not a
TODO list. Re-rank when one gets mitigated or a new one shows up; delete
entries that stop being true (same pruning rule as STATUS.md).

## 1. No persistence story (compounding)

World state is unserializable pointer graphs (`Actor.Def → *ActorDef`,
`Action.Skill → *SkillDef`, …) and dies with the process. A real ARPG is
built on permanent state: characters, stashes, instance handoff, reconnects
that don't delete your gear. Every new feature quietly adds more
unserializable state, so the retrofit grows riskier the longer it waits.
Also silently blocks rollback netcode and mid-instance joins under lockstep
(both need cheap world save/restore).

**Mitigation path:** design a World serialize/restore early — content
referenced by string ID at the boundary, entity state as flat data. Don't
wait for the "save system" feature to force it.

## 2. One-action-at-a-time actor model is too small for the genre

No buffs/debuffs with durations (only DoTs), no channelling, no
cast-while-moving, no stun/interrupt semantics. The stat system is ready
(sourced, removable modifiers) but nothing ticks temporary effects. Action
semantics leak into AI, command validation, and netcode feel — grow this
deliberately (status-effect container first, channelling later) rather than
discovering it mid-feature.

## Smaller, recoverable (listed for honesty)

- TagSet width (was risk #2, closed 2026-06-11): `TagSet` is now an array of
  uint64 words sized at compile time from `TagCount` — adding a tag past any
  64-bit boundary widens the set automatically, with no call-site, hash, or
  golden impact (nothing serializes raw words; saves encode tag indices via
  `TagSet.Tags()`). Cost is one AND+compare per word, paid only once the
  count actually crosses a boundary. The risk that remains is ordinary
  hot-path growth, not a migration cliff.
- Fixed-point overflow (was risk #2, closed 2026-06-10): `fixmath.Mul` now
  runs a 128-bit intermediate via `math/bits.Mul64` and panics on result
  overflow — power creep past the int64 ceiling crashes loudly instead of
  corrupting silently. Standing rule kept from the old entry: `Fixed` is for
  sim quantities only; accumulators/meta-stats (total damage, economy
  counters, XP) get plain integers with their own range analysis.
- Content-as-Go-code: balance changes need recompile+redeploy. Fine until
  live-ops; migration to data files is mechanical.
- Collision/AoE are O(actors×projectiles) scans; add a spatial grid when
  density demands it.
- Pointer-heavy sim state will eventually make the GC visible. Binary deltas
  (2026-06-10) removed the per-tick JSON marshal for game clients; the debug
  wires (TCP, ?format=json) still pay it.
- protocol/binary.go and web/net.js are a hand-maintained codec pair — drift
  is caught only by the version bump discipline and play-testing, not by a
  shared schema or a cross-language test in CI.
- Replays/goldens are version-locked to the code that produced them. Fine as
  tests; never let anything durable (saves, trade history) depend on
  replay-by-reexecution.
- Reconnect/session identity doesn't exist: disconnect = actor and items
  deleted. Needs an account/session layer alongside persistence.

## Parked here (not a risk): server dashboard — operate tier

The observe/poke tiers shipped 2026-06-10 (`server/admin.go`: HTTP+JSON on
its own port, default :9090 — tick health, entity/client counts, bandwidth
per client, event stream, world hash; pause/resume, spawn, kick). What
remains parked is the tier that *consumes* risk #1's mitigation, and whoever
fixes #1 should design with it in mind:

- **Operate (needs #1 solved):** save world to file, load world from file,
  rollback to an earlier tick — rollback = restore a saved state, or replay
  from a snapshot + command log (determinism makes both exact).
- Still no auth: the admin port must stay on localhost/tailnet until an
  auth story exists. Adjustable tick rate also still missing.

## Non-risks (deliberate, stop re-evaluating)

- Single-threaded sim per instance — instances scale horizontally (PoE's own
  model).
- The determinism tax (no floats/maps/wall-clock) — keeps paying for itself.
- Go — nothing above is language-shaped.
