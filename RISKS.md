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

## 2. Fixed-point overflow under power creep (silent corruption)

`fixmath.Mul` computes `a*b/1000` in raw int64 — the *intermediate* wraps
silently past ~9.2e18. Binding site: the stat evaluator's multiplier chain
`(base+flat) × inc × more`. Practical ceiling ≈ 1e10–1e11 damage per hit
under heavy multipliers — ~1000× PoE1's infamous 32-bit DoT cap (2^31
damage-*per-minute* ÷ 60 ≈ 35.79M dps), but ARPG power creep is exponential
by design.

**Lessons from PoE1's cap:** the unit choice multiplies into the ceiling
(their per-minute base cost 60×; our milli base costs 1000×), and the first
thing to overflow is rarely the hit — it's rates and accumulators.

**Rules:** `Fixed` is for sim quantities only; accumulators/meta-stats
(total damage dealt, economy counters, XP) get plain integers with their own
range analysis. **Mitigation (cheap, do before content tuning calcifies):**
route `Mul` through `math/bits.Mul64` for a 128-bit intermediate and panic
on result overflow so determinism tests catch it loudly.

## 3. TagSet is uint64 — 64 tags, ever

12 used so far. PoE-scale conditionality (weapon classes, ailment states,
"while leeching", …) exceeds 64 eventually. Migration to a wider set is
mechanical but touches the hottest path plus hashing → golden-invalidating.
Watch the count; widen deliberately, not mid-feature.

## 4. One-action-at-a-time actor model is too small for the genre

No buffs/debuffs with durations (only DoTs), no channelling, no
cast-while-moving, no stun/interrupt semantics. The stat system is ready
(sourced, removable modifiers) but nothing ticks temporary effects. Action
semantics leak into AI, command validation, and netcode feel — grow this
deliberately (status-effect container first, channelling later) rather than
discovering it mid-feature.

## 5. Protocol has no versioning

No version field in the handshake; renamed JSON fields fail *silently* via
omitempty. One line now; a compatibility matrix after clients proliferate.
**Land with the netcode overhaul.**

## Smaller, recoverable (listed for honesty)

- Content-as-Go-code: balance changes need recompile+redeploy. Fine until
  live-ops; migration to data files is mechanical.
- Collision/AoE are O(actors×projectiles) scans; add a spatial grid when
  density demands it.
- Per-tick JSON marshal + pointer-heavy state will eventually make the GC
  visible; the netcode overhaul (binary deltas) removes most of it.
- Replays/goldens are version-locked to the code that produced them. Fine as
  tests; never let anything durable (saves, trade history) depend on
  replay-by-reexecution.
- Reconnect/session identity doesn't exist: disconnect = actor and items
  deleted. Needs an account/session layer alongside persistence.

## Non-risks (deliberate, stop re-evaluating)

- Single-threaded sim per instance — instances scale horizontally (PoE's own
  model).
- The determinism tax (no floats/maps/wall-clock) — keeps paying for itself.
- Go — nothing above is language-shaped.
