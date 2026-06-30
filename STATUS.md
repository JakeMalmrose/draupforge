# Status — where we are and what's next

Handoff doc for whoever (human or Claude) picks this up next. Update it when
a work session meaningfully moves the project. Read `README.md` for what this
project is, `DESIGN.md` for the foundational decisions — this file is only
the current state.

**Keep this doc small.** When updating, prune as you go: drop shipped next
steps, shortcuts that got fixed, invariants that graduated into DESIGN.md or
tests, and session-log entries older than a few sessions (git history is the
archive). If this file outgrows ~150 lines, it has stopped being a status doc
and started being a changelog — cut it back.

**Last updated: 2026-06-30** (session 15: the descent — ROADMAP.md phase 1
shipped, including a hideout safe zone added mid-session)

## Where things stand

The game is playable in a browser, now as an actual run, not just a
sandbox: `cmd/server -descent` hosts a multi-floor descent (stairs escalate
floors, death costs XP and ejects to a safe hideout, a run grants a few
lives before it ends and resets — ROADMAP.md phase 1) over the same
TCP/NDJSON and WebSocket wires `web/` — a no-build-step canvas client with
click-to-move (pathing around walls), Q fireball / E nova / R spark / T
adrenaline (self-buff), drop pickup, a drag-drop inventory panel (I) with
item icons and hover tooltips, HUD orbs, floor/score/lives readout, an
event log, and a death screen. The full item flow works (kill → drop →
pickup → bag → equip → affixes on the sheet); damage runs the whole
pipeline; elemental hits inflict ailments (ignite/chill/shock); rooms and
corridors make geometry matter — projectiles stop at walls, monsters path
around them, and skeleton archers kite to firing range. Run it:

```sh
go test ./...                                    # tests, all green
go run ./cmd/headless -script scripts/slice.json # watch the fight as events
go run ./cmd/server -scenario scripts/arena.json -descent
                                                 # open localhost:8080
                                                 # admin dashboard: localhost:9090
```

All foundational machinery from DESIGN.md is real, not stubbed:

| System | Where | State |
|--------|-------|-------|
| Fixed-point math (no floats in sim) | `sim/fixmath` | done, tested |
| Geometry, projectile sweep, terrain: tile grid (clearance-eroded walkability), DDA wall raycast, deterministic A* + smoothing, rooms-and-corridors mapgen off RNGMap | `sim/space` | done, tested; `Walkable` seam is real now |
| Stat algebra (flat/inc/more/override + tags) | `sim/stats` | done, tested, memoized |
| World/Actor/Hit/defs, RNG, state hashing | `sim/core` | done |
| Damage pipeline + DoTs + regen | `sim/combat` | done, tested |
| Statuses: ignite (DoT) + chill/shock (hit-scaled, strongest-wins) + content buffs (`BuffDef` packages, refresh-not-stack, `SkillBuff` skills via pending-buff queue) | `sim/combat/ailments.go`, `buff.go` | done, tested |
| Persistence: `World.Save`/`LoadWorld` (versioned JSON, content by string ID, bit-exact continuation), admin `POST /api/save`, `cmd/server -load` | `sim/core/save.go`, `sim/space/save.go` | done, tested |
| Actions (windup/recovery) + projectiles | `sim/skills` | done |
| Loot: per-table rarity weights, weighted affixes, group caps, rolled base implicits, starved-pool event | `sim/items` | done, tested |
| Progression: XP on kill, quadratic curve, level cap 50, PerLevel growth mods under `LevelModSource`, ding heal, HUD level + XP bar | `sim/progress`, `core.Actor.SetLevel` | done, tested |
| Equipment: 10 slots (weapon…belt), slot-addressed equip command (auto fallback), affix→sheet | `sim/items/equip.go` | done, tested |
| Inventory: pickup/unequip/drop_item, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, joins/leaves, send-rate decoupling, interest culling, binary deltas + acks, pause | `server/` | done, race-tested |
| Descent: stairs escalate floors (monster level == floor depth), a safe monster-free hideout starts every run and catches every death, death halves XP progress and consumes one of a few lives, exhausting them hard-resets the run; character extract/inject re-mints item IDs across zone transfers | `server/run.go`, `cmd/server -descent` | done, tested (unit + WS e2e), verified live |
| Admin dashboard: observe (tick health, counts, bandwidth, events, world hash) + poke (pause/resume, spawn, kick), own port, embedded HTML | `server/admin.go` | done, tested; NO AUTH — localhost/tailnet only |
| Web client: canvas, input, terrain render (walls/floor), drag-drop inventory grid (icons, tooltips), delta decoding, tick-timeline interpolation, fade-in/out, cast/impact VFX + ailment rings | `web/` | working, no build step |
| AI: behavior registry — `melee_chaser`, `ranged_kiter` (LoS-gated shooting, retreat band) | `sim/ai` | real, tested |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome, JSON snapshots, binary delta view codec | `protocol/` | done, tested |
| Content tables | `content/` | fireball, frost_nova (AoE), spark, zombie_slam, bone_arrow, 4 actors (player/zombie/archer/dummy), 32 affixes (tiered groups), 9 bases (one per slot family, each with a rolled implicit), 3 drop tables (zombie/archer/dummy) |
| Debug client | `cmd/headless` | done |
| Determinism + golden replay tests | `sim/sim_test.go` | done |

## Invariants the code currently honors (don't break casually)

- No floats, no wall-clock, no unseeded randomness inside `sim/`.
- Sim logic never iterates a Go map (random order). Maps are lookup-only.
- All RNG flows through the world's four named streams (combat/loot/ai/map);
  consumption order is part of replay compatibility.
- Data types live in `sim/core`; leaf packages hold logic and import core;
  only root `sim` knows the phase order. `protocol/` imports nothing.
- Per-damage-type stat queries REPLACE the damage-type tag in the context
  (see `damageTypeTags` in `sim/combat/pipeline.go`). Session 1 shipped and
  fixed a bug here — added-fire was leaking into other types' rolls.
- Statuses grant their sheet modifiers under `Status.ModSource()`: ailments
  use `StatusKind.ModSource()` (high bit set, bit 62 clear), buffs use
  `BuffDef.ModSource()` (top two bits set + FNV of the buff ID — stable
  across content reordering and saves; content.DB() panics on collision),
  and per-level growth lives under `core.LevelModSource` (bit 62 alone).
  All three spaces are disjoint from item-ID sources. `TickStatuses` removes
  them at expiry. Chill consumes no combat RNG, shock rolls only on
  lightning damage, buffs consume none — `TestAilmentRNGConsumption` pins
  the stream alignment so old fire-only replays stay stable.
- Saves are durable state: any new world state ships its save-format
  support (and a `SaveVersion` bump on shape changes) AND `World.Hash`
  coverage in the same commit — the hash is already a curated subset
  (Action path/aim internals are saved but unhashed); don't let it thin
  further. Save only at tick boundaries; `World.Save` refuses pending
  hits/buffs.
- Golden replays: any behavior change fails `TestGoldenReplay` (open plane,
  `golden_slice.txt`) and/or `TestGoldenDungeon` (generated map + pathing +
  ranged AI, `golden_dungeon.txt`). If intentional, re-record both:
  `DRAUPFORGE_UPDATE_GOLDEN=1 go test ./sim/` and commit the testdata.
- Terrain (`World.Grid`) is immutable once set, installed before any spawn.
  Nil grid = the v1 open plane, bit-exact with pre-terrain behavior — the
  old golden depends on it. One shared clearance radius (0.65 > fattest
  actor); paths are computed at command time and followed blind (static
  terrain makes that safe). `FindPath` uses per-grid scratch buffers —
  single-goroutine, like everything else. Mapgen consumes only RNGMap.
- `protocol/binary.go` and `web/net.js` are a hand-maintained codec pair.
  Any wire change updates both AND bumps `protocol.Version` — a stale client
  fails loudly at the welcome instead of misreading frames.

Structural risks live in `RISKS.md` — read it before building anything load-bearing (top entry: the action model is still one-thing-at-a-time — no channelling/stun/interrupt).

## Known shortcuts (deliberate, fine for now)

- Conversion stage in the pipeline is identity — the slot in the order is
  reserved, no skill converts yet.
- Leech, block, stun, ES recharge: absent.
- Chill doesn't slow an action already in flight — windup/recovery tick
  counts bind at use time, like cast speed. Movement slows immediately.
- Client cast VFX key off windup→done action transitions between views;
  a windup shorter than the send interval (~3 ticks) would slip through
  unrendered. No current skill is that fast.
- Corpses compact away at tick end — fine until on-corpse mechanics matter.
- Inventory is a flat ID-addressed bag — no stacking. Bag *arrangement* is
  client-side presentation state (`bagLayout` in client.js): rearranging
  cells sends nothing, and the layout dies with the page (no localStorage).
- Item icons are hand-drawn inline SVGs keyed by base id; unknown bases
  fall back to a diamond. The client's `BASE_SLOTS` map mirrors the
  server's slot families by hand — new base items must update both.
- Server: no auth, no persistence (disconnect deletes the actor and its
  items), one instance per process, and a slow client can stall a tick for
  up to 1s (no per-client send queues). Fine for now; on the list.
- No client prediction — input feels its latency. Prediction is the thing
  that would justify compiling sim/ to wasm (DESIGN.md §13's optional layer).
- Projectiles are excluded from the entity fade-in/out (too short-lived to
  read as anything but mush); they still pop at interest-range edges.
- WS endpoint accepts any origin (LAN-dev convenience); static files come
  from -web dir at runtime, not embedded.
- Live server play is not replay-deterministic (network timing decides
  command arrival ticks); determinism holds within a tick via stable command
  sort. A replay log (seed + per-tick commands) would restore full replays —
  cheap to add when wanted.
- No actor-actor collision (archers can stack on one tile).
- Monsters aggro through walls, then path around — "they heard you."
- AI re-issues its chase target every tick; the half-tile repath throttle
  in `CmdMove` keeps that cheap. A swarm in a maze could still make A* the
  hot path — measure before optimizing.
- Terrain travels as JSON rows in the welcome (~2KB at 48×48) — fine until
  maps get big or revealable (fog of war would change this).
- Kiter retreat picks from 5 fixed directions, no flee pathfinding — a
  cornered archer stands and fights.
- Affix pool is global — no per-slot pools, so boots can roll cast speed.
  Fine until itemization depth matters.
- Spawn-room pressure is real: scatter keeps monsters 10u out, but they
  converge once anyone aggros. The hideout's exit, and wherever a portal
  is planted, inherit this — both are fixed, deterministic points, so an
  unlucky scatter roll right at a floor's entrance repeats every visit
  until someone plants a portal somewhere safer.
- Descent is single-instance, whole-party-follows (DESIGN.md §14: no
  instance manager yet): one player's death, descend, or run-over drags
  every connected client through the same transition. Fine solo or small
  co-op; a stranger's death interrupting your fight is the rough edge.
- Floors are stateless on revisit: portal-eject or hideout-resume
  regenerates the target floor fresh (same seed → same layout, but new
  monsters) — nothing about a previous visit (kills, picked-up loot)
  persists. `core.World` truly is "one self-contained zone" each time.
- A death that triggers a same-tick floor swap (hideout eject, run-over)
  won't show its own death/hit lines in the dying client's event log:
  `BuildSnapshotFor`'s interest-culling filters events by entity ID
  against the *current* (post-swap) world, and the swap re-mints every
  connected client's actor ID, so the old event's Actor/Other never
  matches anyone there. The synthetic transition event (`hideout_eject`,
  `floor_change`, `run_over`) still arrives — that's the signal that
  actually matters — and the admin dashboard's `recentEvents` (omniscient)
  isn't affected. Cosmetic gap, not a correctness one.
- Run numbers (3 lives, monster level == floor depth, death halves XP
  progress) are deliberately simple and tunable — see the constants and
  comments at the top of `server/run.go`.
- A freshly built World must never let its first view reach a client
  before at least one tick has passed: the binary wire's keyframe sentinel
  is baseline-tick 0, and a client's ack echoes a view's own tick, so a
  genuine Tick-0 view gets misread as the unrelated "I lost my state, send
  a keyframe" signal — and once that mismatch happens, every subsequent
  delta inherits the corruption (verified live: a death→hideout eject left
  "YOU DIED" stuck with zero actors in view, even though the server's own
  state was correct). `server/run.go`'s `newFloorSim` bumps Tick before
  anything else; any future code that builds a `*sim.Sim` mid-session
  (not at process startup) needs the same.

## Feature plan (set 2026-06-30, session 15)

The descent shipped (ROADMAP.md phase 1) — floors escalate via stairs,
death costs XP and ejects to a hideout, a run grants a few lives before a
hard reset. ROADMAP.md's fun-first ordering says what's next:

1. **A telegraphed boss** at floor milestones (ROADMAP phase 2) — the
   destination of a descent, and the first fight that's about reading and
   dodging a wind-up rather than just trading hits. Forces the action
   model to grow on purpose (RISKS.md #1) — design that state machine
   first, don't accrete it onto the existing skill switch.
2. **Pack variety and juice** (ROADMAP phase 2) — a few more monster
   archetypes, occasional rare/magic packs, and the cheap client-side
   feedback (damage numbers, hit flashes, telegraphs) that makes the
   descent feel as good as it now functions. Sprinkle continuously rather
   than batching into a polish pass.
3. **Skill gems + a small passive choice** (ROADMAP phase 3) — build
   identity is the next thing loot and levels are currently missing.

Later: the character store + sessions (characters survive disconnects;
pulls connection ownership above the instance — DESIGN.md §14 phase 2,
and the natural fix for the single-instance/whole-party-follows shortcut
above), then server hardening (replay log, per-client send queues) when
strangers connect.

## Session log

- **2026-06-30 (15)** — The descent (ROADMAP.md phase 1), end to end:
  stairs (`space.Grid.Stairs`, last room's center) escalate floors via
  `server.Instance.transitionToFloor` (extract every connected client's
  character, build the next floor from `floorSeed(runSeed, floor)`,
  re-mint item IDs at injection, re-welcome). Mid-session, on Jake's call,
  reworked death from "eject to a portal on whatever floor you died on"
  into a proper hideout: a small monster-free World every run starts in
  and every death returns to (XP halved, a life consumed); the run's
  floor/score/portal-plant survive the trip, so it's a pause, not a
  reset. Exhausting lives hard-resets the run (new seed, fresh level-1
  characters, back to the hideout) — `protocol.Version` 10 carries
  Floor/MaxFloor/PortalCharges on the welcome (0 floor = hideout).
  Shipped with real test coverage (sim/space + server unit tests, a WS
  e2e test driving the full wire), then manually verified live in a
  browser, which caught a genuine bug no test had: a freshly built
  World's first view could carry Tick 0, colliding with the binary wire's
  keyframe-ack sentinel and silently emptying every connected client's
  view from then on (`newFloorSim` fix + regression test, see Known
  shortcuts). `go test ./...` green, goldens untouched (descent code
  never touches `sim/sim.go`'s phase order).
- **2026-06-12 (14c)** — Docs only: DESIGN.md §14 settles the
  character/zone/instance/server separation that session 15's descent
  was built against (worlds self-contained, characters server-owned
  projections, item IDs re-mint at zone injection, transfer = re-welcome).
- **2026-06-12 (14b)** — XP and levels. New `sim/progress` (AwardXP off
  death events after RollLoot, 100·level² curve, cap 50, ding heal);
  `Actor.SetLevel` rebuilds `Def.PerLevel` mods under `LevelModSource`;
  monsters carry levels/XP values/growth packages for future floor
  scaling. SaveVersion 3, hash covers level+XP, protocol v9, HUD level
  badge + XP bar. Goldens re-recorded (hash shape). Verified live: dummy
  kill paid 10 XP on the wire.
- **2026-06-12 (14)** — Loot 2.0. Rolled implicit per base, affix pool
  10 → 32 with tiered groups, per-actor drop tables with rarity weights
  in `LootTableDef`, `EvLootStarved` on pool starvation. SaveVersion 2,
  item hash covers rarity+implicit, protocol v8, tooltip implicit line.
  Only `golden_slice` re-recorded. Verified live: spawned dummy's drop
  carried an in-range implicit. (Detail in both commit messages.)
- (older sessions pruned — git history is the archive)
