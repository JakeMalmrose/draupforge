# Draupforge Sim Core — Foundational Design

This documents the decisions that are expensive to change later. Anything not
covered here is fair game to figure out during implementation.

## 1. Entity model: pragmatic actors, not ECS

We do **not** use an ECS library. A PoE-like has a small, stable entity taxonomy —
the genre's complexity lives in the stat/modifier system and skill interactions,
not in entity composition. An archetype ECS solves a problem we don't have and
costs indirection everywhere.

Instead:

- **`Actor`** — one struct for everything that acts: players, monsters, minions,
  totems. Holds position, stat sheet, resource pools (life/mana/ES), current
  action, status effects. Behavior differences come from data (its `ActorDef` and
  AI behavior field), not from different types.
- **Separate pools for high-volume ephemera** — projectiles, ground effects, and
  loot drops get their own small structs in their own slices. They vastly
  outnumber actors and need only a fraction of the state.
- **Stable IDs, slice iteration** — every entity has a `EntityID` (uint64,
  monotonically assigned by the world, never reused). Entities live in slices and
  are iterated in insertion order. **Sim logic never iterates a Go map** — map
  order is random and would break determinism. Maps are allowed only as
  ID→index lookups.
- Dead entities are tombstoned during the tick and compacted at tick end, so
  indices are stable within a tick.

## 2. The stat system (the heart of the genre)

Every number in the game flows through one evaluator. The PoE modifier algebra:

```
final = (base + Σflat) × (1 + Σincreased − Σreduced) × Π(1 + more_i) × Π(1 − less_i)
```

then `override` (if any) wins outright. The crucial property: *increased* values
share one additive bucket; each *more* multiplier applies separately. This split
is the genre's entire balance language and must be first-class from day one.

```go
type Modifier struct {
    Stat   StatID     // what it modifies (Life, Damage, CastSpeed, ...)
    Layer  Layer      // Flat | Increased | More | Override
    Value  Fixed      // see §5 on numbers
    Tags   TagSet     // when it applies: Fire, Spell, Hit, DoT, Melee, ...
    Source SourceID   // the item/passive/buff that granted it, for removal
}
```

- **Tags are the conditionality system.** "10% increased fire damage" is
  `{Stat: Damage, Layer: Increased, Tags: {Fire}}`. A query carries a tag
  context — `sheet.Eval(Damage, TagSet{Fire, Spell, Hit})` — and a modifier
  applies iff its tags are a subset of the query's tags. TagSet is a uint64
  bitset; subset check is one AND+compare.
- Conditional modifiers ("while at full life") are tags whose presence is
  computed per-tick on the actor (`FullLife` tag set when applicable), keeping
  the evaluator itself condition-free.
- **Evaluation is on-demand with a per-tick memo cache** keyed by
  (stat, tag context), invalidated when the modifier list changes. No dirty-flag
  dependency graph in v1 — measure first, optimize later. The evaluator is a pure
  function of the modifier list, so it's trivially golden-testable.

## 3. Damage pipeline

A hit is an explicit value flowing through named stages — not arithmetic smeared
across call sites. Each stage is independently unit-testable.

```
SkillBase → AddedDamage (× skill effectiveness) → Conversion (phys→ele etc.)
  → Inc/More from attacker stats → HitCheck (accuracy vs evasion)
  → CritCheck (chance, then multiplier) → Mitigation per damage type
    (armour for phys, resists for ele, flat reductions)
  → DamageTaken modifiers on defender → ApplyToPools (ES before life)
  → PostHit (ailment rolls: ignite/shock/chill; leech; on-hit/on-kill events)
```

```go
type Hit struct {
    Attacker, Defender EntityID
    Damage             [DamageTypeCount]Fixed  // phys, fire, cold, lightning, chaos
    SkillTags          TagSet
    CritRoll, Crit     bool
    // accumulates outcomes as it flows: blocked, evaded, ailments inflicted...
}
```

Damage types are a fixed array indexed by enum, not a map. Conversion happens
once, in pipeline order (a PoE lesson: conversion order is a balance lever —
locked as base → added → converted, modifiers from all applicable types apply
post-conversion).

Damage-over-time is **not** a hit. DoTs skip hit/crit/armour and run through a
separate, simpler per-tick path. Conflating hits and DoTs is a classic ARPG
implementation mistake; they share only the mitigation tail.

## 4. Time, ticks, and actions

- **Fixed 30 ticks/sec.** All durations, cooldowns, and speeds are stored in
  ticks (`type Ticks uint32`). Seconds exist only at the content-authoring and
  display boundary. World time is a single `uint64` tick counter.
- `World.Step(commands []Command)` advances exactly one tick:
  `intake commands → AI decides → actions advance → projectiles/areas move &
  collide → hits resolve → DoTs tick → deaths & loot → events drain → compact`.
  Fixed phase order, every tick, no exceptions — phase order is the determinism
  contract.
- **Actions**: an actor does one thing at a time (idle / moving / using a skill).
  Skill use is a tick-counted action with windup → effect point → recovery, which
  is what makes attack/cast speed, animation canceling, and stun interrupts
  modelable later.
- The sim never reads the wall clock. The host (server, test, replay runner)
  decides when to call `Step`.

## 5. Numbers: fixed-point, no floats in the sim

`type Fixed int64`, scaled ×1000 (milli-units). All gameplay math — damage,
stats, positions, speeds — uses it. Rationale: float math is *probably*
reproducible on one server arch, but "probably" is a bad foundation for replays
and golden tests, and cross-arch (arm64 dev Mac vs amd64 server) drift is real.
Fixed-point makes determinism boring and certain. 1/1000 granularity is plenty
for ARPG math; int64 gives ~9×10¹⁵ headroom, so intermediate products won't
overflow at sane scales.

The one place this costs us is sqrt/trig for movement vectors — implemented once
in `sim/fixmath` with integer algorithms and golden-tested.

## 6. RNG

- One **PCG-64 stream per concern**, all seeded from the world seed:
  `rngCombat`, `rngLoot`, `rngAI`, `rngMapGen`. Split streams mean adding an AI
  random call doesn't reshuffle every loot drop after it — replays and golden
  tests stay diff-able when unrelated systems change.
- RNG is only ever consumed inside `Step`, in phase order. No RNG in snapshot
  encoding, logging, or anything outside the sim.

## 7. Concurrency model: single-threaded sim, instances as the unit of parallelism

A map instance's `World` is **strictly single-goroutine**. No locks, no atomics,
no races, perfect determinism. The server scales by running many instances on
many goroutines — which is exactly how PoE itself scales. A 30Hz tick with a few
hundred entities is trivial for one core; we will never need intra-instance
parallelism before we need a profiler.

## 8. Space and movement

2D plane, fixed-point coordinates, collision circles for entities. v1 is an open
arena: straight-line movement, circle-vs-circle and circle-vs-segment tests.
Pathing (navgrid + A*) comes with real map layouts — the `sim/space` API takes a
`Walkable(from, to)` interface from day one so pathing slots in without touching
movement consumers.

## 9. Commands in, snapshots out (`protocol/`)

- **Commands** are the only way anything outside the sim affects it:
  `MoveTo{pos}`, `UseSkill{skill, target|dir}`, `StopAction{}`. Each is stamped
  with the issuing actor and validated by the sim (range, resources, alive) —
  the client is a suggestion box, never an authority.
- **Snapshots** are the only way state leaves: per-tick structs of visible
  entity state. v1 emits full-world snapshots as JSON for debuggability; the
  delta/interest-management/binary-encoding layer is a server concern bolted on
  later without touching the sim.
- `protocol/` holds both, versioned, with no `sim/` imports in clients' future.

## 10. Events

Synchronous FIFO event queue inside the tick (`Death`, `HitLanded`,
`AilmentApplied`, ...) drained in its phase. Triggers ("on kill, explode") enqueue
follow-up effects rather than recursing; a per-tick trigger-depth cap (16) makes
infinite trigger loops impossible by construction. The same event stream is the
hook for combat logging and tests.

## 11. Content as Go code (for now)

Skills, monster defs, base items, and affix pools are **typed Go literals** in
`content/` — compile-time checked, refactorable, no parser to write, fastest
possible iteration loop. Migration to data files only when there's a reason
(modding, an editor, a balance-tuning workflow). Definitions are pure data
consumed by the sim; the sim never imports `content/` — the host wires defs in.

## 12. Testing strategy (determinism is a feature only if enforced)

- Unit tests per stage: stat evaluator, each damage-pipeline stage, fixmath.
- **Golden replay tests**: scripted command sequences + seed → hash of world
  state per N ticks, committed. Any unintended behavior change fails CI loudly.
- **Determinism test**: run the same script twice in one process and on both
  arches in CI; assert identical state hashes every tick.

## 13. Networking model (decided 2026-06-10)

**Server-authoritative state replication is the spine.** Clients receive
views; they never compute truth. The alternative — deterministic lockstep
with the sim compiled to wasm — was considered and deliberately demoted to
an *optional later layer* for own-character latency feel (PoE-style
predictive/lockstep modes), because a serious online ARPG needs hidden
information (anti-maphack, economy), mid-instance joins/reconnects, and
client-tech freedom, all of which replication gives and input-streaming
lockstep structurally can't.

Two contracts follow:

- **A snapshot is one client's *view* of the world, not the world.** With
  interest management, different clients legitimately see different state.
  Nothing client-side may assume omniscience.
- **Determinism stays mandatory** even though replication doesn't strictly
  need it — it powers replays, golden tests, server verification, and keeps
  the prediction/rollback option open. The no-floats/no-map-iteration/
  seeded-RNG rules do not relax.

## 14. Characters, zones, instances (decided 2026-06-12)

Four words, four layers — don't let them collapse back into each other:

- **A `core.World` is one zone.** Self-contained and deterministic: its
  actors, drops, RNG streams, terrain, tick. It never references anything
  outside itself. A dungeon floor is a whole World; "three floors on one
  map joined by teleporters" is explicitly rejected.
- **An instance is a running zone**: World + tick loop + the clients
  currently inside. (Originally `server.Instance` was also the listener and
  the whole process; since session 38 the Lobby owns the listeners and
  routes connections to many instances — the circumstance caught up with
  the design.)
- **A character is durable, server-owned, and lives outside every world**:
  identity, level, XP, bag, equipment (later: skills, passives). The actor
  in a World is a *projection* of the character, minted at zone entry and
  reduced back to character state at exit (stairs, death, disconnect).
  Zone-local state — position, action, buffs, DoTs — deliberately does not
  transfer; the stat sheet rebuilds from def + level + equipment at
  injection. The persistence players care about is the character store;
  `World.Save` remains for whole-instance snapshots and rollback.
- **The server owns sessions, the character store, and an instance
  manager.** All transfers happen at the host layer between ticks; the sim
  never participates. (All three are real now: `server/identity.go` is the
  session + character store, `server/lobby.go` the instance manager.)

Decisions that follow:

- **Entity IDs are world-local; re-mint item IDs at injection.** Item IDs
  double as sheet mod sources and would collide in a fresh World. Durable
  item identity (trade, dupe detection, someday) belongs to the character
  store, never to an entity ID.
- **Zone transfer = re-welcome on the same socket.** Any welcome is a full
  client reset: interpolation buffers, delta baselines, `myId`, map. The
  server resets its per-client encoder/ack state to match. This is the
  same machinery as in-process load/rollback — build once, pay twice.
- **A run owns a seed; floor N's World seed derives from it** (run seed +
  floor index), so whole descents are replayable floor by floor.
- **Sequence the build single-instance-first**: one Instance that swaps
  its Sim underneath connected clients is the whole descent feature. An
  instance manager (town hub, concurrent floors, connections owned above
  the instance) comes only when something needs it. (Parties needed it —
  the Lobby arrived in session 38, on exactly this schedule.)

## 15. Staged skills (decided 2026-07-03)

The action model's first deliberate growth (risk register #1): a skill is no
longer forced into the windup → one effect → recovery arc. A `SkillStaged`
skill is a **scripted sequence of stages** — each stage a tick countdown
ending in an effect at an aim point **locked when the stage begins**:

- **Stage effects** are composable primitives (`StageBlast` — full-damage
  AoE at the locked point, `StageRing` — a projectile circle from the
  caster, `StageNone` — a pause; recovery is just a trailing effect-less
  stage). New boss attacks are authored by sequencing these, not by growing
  a switch.
- **Aim modes** (`Target` / `Self` / `Point`) resolve at stage start and
  pin. The telegraph the client renders IS the hitbox; dodging means
  leaving the zone during the countdown, so telegraphed blasts skip the
  accuracy/evasion roll entirely — their dodge is spatial.
- **Durations bind at use time** (like recovery ticks): chill mid-sequence
  can't stretch a committed attack. The caster is committed for the whole
  sequence — one action at a time still holds.
- Telegraphs travel on the wire per actor (`TelegraphSnap`: center, radius,
  countdown), so clients render exact danger zones instead of inferring
  them from skill metadata.

Still deliberately absent (grow on purpose, not by accretion): channelling,
cast-while-moving, stun/interrupt semantics, and mid-tick entity creation
(risk register #2 — rings spawn projectiles, which pools allow; minions
would not be).

## Package layout

```
sim/         # package sim: Step (the phase order), command validation, snapshots
  core/      # shared data types: World, Actor, Hit, defs, events, RNG, hashing
  stats/     # StatID, Modifier, TagSet, the evaluator
  combat/    # Hit pipeline, DoTs, ailments, mitigation, upkeep
  skills/    # action advancement (windup/recovery), projectile flight
  items/     # affix rolling, loot generation
  ai/        # monster behaviors (deciders emitting the same Commands players use)
  space/     # fixed-point geometry, collision, Walkable interface
  fixmath/   # Fixed arithmetic, sqrt
content/     # typed Go data: skills, monsters, base items, affix pools
protocol/    # Command + Snapshot wire types (imports nothing from sim/)
server/      # identity/sessions, lobby of instances, transports, tick driver per instance
cmd/
  headless/  # script-driven runner: feed commands, print events/snapshots
```

Cycle-free by construction: data types live in `sim/core`, system logic lives
in leaf packages that import core, and only the root `sim` package knows the
phase order. `sim/` imports nothing outside `sim/`; `protocol/` imports
nothing at all; `content/` builds `core` defs; `server/` and `cmd/` sit on top.

## First vertical slice (the "is this real" milestone)

One arena. One player actor with Move and a Fireball (projectile, fire damage,
can ignite). One melee zombie type with chase-and-swing AI. Full loop:

> spawn → command intake → cast → projectile flight → hit → full damage
> pipeline → death → loot drops with rolled affixes → JSON snapshots out

driven by `cmd/headless` from a scripted command file, with a golden replay test
locking it in. Everything after that — more skills, defenses, map gen, the
server layer — is iteration on a proven spine.
