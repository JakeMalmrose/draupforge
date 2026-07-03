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

The skill half of this got its deliberate design in DESIGN §15 (staged
skills, session 45): multi-effect sequences compose from stage primitives
instead of growing the `fire()` switch, and the Barrow King proved it.
What remains of the risk: the legacy single-effect path still IS a switch
on `SkillKind`, and every action — staged or not — still owns the actor
completely. Channel/movement-skill/interrupt each still need the same
design-first treatment.

## 2. No mid-tick entity creation

Nothing can add an actor to the world during a tick — spawning happens from
the host between ticks. Minions, totems, spawn-on-death adds, and
split-on-hit monsters all need it, and the genre guarantees we'll want
them. The hazards are ordering: AoE hits queue in live actor-slice
iteration order, IDs are assigned at insert, compaction runs at tick end.
The fix is a deliberate spawn queue drained at a fixed phase — design it
before the first minion skill, because retrofitting changes hit order and
re-records every golden.

## Smaller, recoverable (listed for honesty)

- The public server is unhardened: session identity shipped (session
  37–38 — named characters survive disconnects; 60s empty-instance reap is
  the reconnect grace), but the Funnel URL has no rate limiting, the WS
  accepts any origin, a slow client can stall its instance's tick for up
  to 1s (no per-client send queues), and `identities.json` is a plaintext
  blob of auth tokens. Fine at friends-scale; harden before strangers
  matter.
- Content-as-Go-code: balance changes need recompile+redeploy. Two sharper
  edges found in audit: saves reference content by string ID, so editing a
  def retro-patches every saved world (usually what you want, occasionally
  surprising), and content slice order feeds weighted rolls — reordering
  the affix table is a replay-relevant change, not a refactor.
- AI deciders are stateless functions of the current world. Leashing
  shipped stateless off `Actor.Home` (saved + hashed), but cooldowns,
  threat tables, and "don't repeat last tick's dodge" still need per-actor
  AI memory that doesn't exist — when a behavior wants it, add a saved
  (and hashed) AI-state blob on Actor rather than smuggling state into
  ActorDef.
- The world hash is a curated subset of saved state: Action's
  path/aim/target internals are saved but not hashed, so divergence there
  surfaces only later as position/outcome drift. Tolerable while known —
  but every new piece of actor state must ship save support AND hash
  coverage in the same commit, or golden coverage quietly thins.
- RNG-stream alignment is conditional-consumption shaped: shock rolls only
  on lightning damage, chill and buffs consume nothing — pinned by
  `TestAilmentRNGConsumption`. The pattern compounds: every new
  proc/on-hit effect adds conditions, and each needs the same pinning test
  or replays break invisibly.
- Collision/AoE are O(actors×projectiles) scans; add a spatial grid when
  density demands it.
- Pointer-heavy sim state will eventually make the GC visible. Game clients
  get binary deltas; the debug wires (TCP, ?format=json) still pay a
  per-tick JSON marshal.
- The hand-maintained server/client drift surface is wider than the
  protocol codec pair (`protocol/binary.go` ↔ `web/net.js`): `client.js`
  also hand-mirrors slot families (`BASE_SLOTS`, `EQUIP_SLOTS`) and
  `PICKUP_RANGE`. The codec is guarded by the version-bump discipline; the
  mirrored constants are guarded by nothing but play-testing.
- Replays/goldens are version-locked to the code that produced them. Fine as
  tests; never let anything durable (saves, trade history) depend on
  replay-by-reexecution.
- `Fixed` is for sim quantities only — `fixmath.Mul` panics loudly on
  overflow rather than corrupting, but accumulators/meta-stats (total
  damage, economy counters, XP) should be plain integers with their own
  range analysis.

## Parked here (not a risk): server operations leftovers

- In-process load/rollback without a restart: needs re-welcoming every
  client (terrain and actor IDs change under them) — machinery the floor
  swap now has, but nothing drives it for loads. Worse, the lobby refuses
  `-load` outright (run saves predate parties), so save-and-restart isn't
  even a rollback story right now — STATUS.md tracks the regression.
- No auth: the admin port must stay on localhost/tailnet until an auth
  story exists. Adjustable tick rate also still missing.

## Non-risks (deliberate, stop re-evaluating)

- Single-threaded sim per instance — instances scale horizontally (PoE's own
  model).
- The determinism tax (no floats/maps/wall-clock) — keeps paying for itself.
- Go — nothing above is language-shaped.
- Delta-codec field masks: the mask is a uvarint uint64, 11 of 64 bits
  used — adding view fields is a bit + a version bump, not a redesign.
