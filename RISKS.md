# Architectural Risk Register

Known structural risks, ranked by how hard they'd be to recover from — not a
TODO list. Re-rank when one gets mitigated or a new one shows up; delete
entries that stop being true (same pruning rule as STATUS.md).

## 1. Action model is still one-thing-at-a-time

The status-effect container shipped 2026-06-11 (the first step this entry
prescribed): `Actor.Statuses` now holds ailments and content-defined buffs
(`BuffDef` = modifier package + duration; applied via `SkillBuff` skills
through a pending-buff queue, refresh-not-stack, retired by the same
`TickStatuses` as ailments) — "buffs/debuffs with durations" is no longer
missing. What remains is the action model itself: no channelling, no
cast-while-moving, no stun/interrupt semantics. Action semantics leak into
AI, command validation, and netcode feel — grow them deliberately, not
mid-feature.

## Smaller, recoverable (listed for honesty)

- Persistence (was risk #1, closed 2026-06-11): `World.Save`/`core.LoadWorld`
  (`sim/core/save.go`) serialize the whole world at a tick boundary — content
  by string ID, entity state as flat data (sheets verbatim, in modifier
  order: fixed-point More multipliers compose in list order, so re-deriving
  could drift a milli), tags as width-independent indices, terrain rows with
  the pruned walk layer, all four RNG streams. Restored worlds continue
  bit-identically (`TestSaveRestoreContinuation`). `SaveVersion` gates
  format changes: saves are durable state and must never lean on
  replay-by-reexecution. The discipline that keeps this closed: new world
  state (buffs, charges, cooldowns) extends the save format in the same
  commit that adds it.
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

## Parked here (not a risk): server dashboard — leftovers

Observe/poke shipped 2026-06-10; the operate tier shipped 2026-06-11 with
persistence (admin `POST /api/save` writes a save file between ticks;
`cmd/server -load` restarts from one — orphaned player actors are removed at
load with their gear dropped, since no session identity exists to reclaim
them). Still parked:

- In-process load/rollback without a restart: needs re-welcoming every
  client (terrain and actor IDs change under them). Restart-with-`-load` is
  the rollback story until that's worth building.
- Still no auth: the admin port must stay on localhost/tailnet until an
  auth story exists. Adjustable tick rate also still missing.

## Non-risks (deliberate, stop re-evaluating)

- Single-threaded sim per instance — instances scale horizontally (PoE's own
  model).
- The determinism tax (no floats/maps/wall-clock) — keeps paying for itself.
- Go — nothing above is language-shaped.
