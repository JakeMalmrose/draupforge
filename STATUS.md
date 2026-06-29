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

**Last updated: 2026-06-29** (session 15: the descent — escalating floors
via stairs, character extract/inject, re-welcome on zone transfer, depth as
the score)

## Where things stand

The game is a **run** now, not a sandbox: you descend floor by floor through
generated dungeons via stairs, packs scale with depth, and "how deep did you
get" is the score (a `Floor N` readout, and a deadlier mob each floor). It's
playable in a browser: `cmd/server` hosts the sim (TCP/NDJSON and WebSocket,
same frames) and serves `web/` — a no-build-step canvas client with
click-to-move (pathing around walls), Q fireball / E nova / R spark /
T adrenaline (self-buff), **F descend** at the stairs, drop pickup, a
drag-drop inventory panel (I) with item icons and hover tooltips, HUD orbs, an
event log, and a death screen. The full item flow works (kill → drop → pickup
→ bag → equip → affixes on the sheet); damage runs the whole pipeline;
elemental hits inflict ailments (ignite/chill/shock); rooms and corridors make
geometry matter — projectiles stop at walls, monsters path around them, and
skeleton archers kite to firing range. Run it:

```sh
go test ./...                                      # ~45 tests, all green
go run ./cmd/headless -script scripts/slice.json   # watch the fight as events
go run ./cmd/server -scenario scripts/descent.json # then open localhost:8080,
                                                   # walk to the stairs, press F
                                                   # admin dashboard: localhost:9090
```

All foundational machinery from DESIGN.md is real, not stubbed:

| System | Where | State |
|--------|-------|-------|
| Fixed-point math (no floats in sim) | `sim/fixmath` | done, tested |
| Geometry, projectile sweep, terrain: tile grid (clearance-eroded walkability), DDA wall raycast, deterministic A* + smoothing, rooms-and-corridors mapgen off RNGMap, stairs-down `Exit` (farthest room) | `sim/space` | done, tested; `Walkable` seam is real now |
| Descent: portable `Character` extract/inject (re-mints item IDs, rebuilds sheet from def+level+gear), per-floor seed, host floor-swap (extract → build scaled floor → inject → re-welcome), depth as score | `sim/core/character.go`, `server/server.go` | done, tested (unit + wire + browser) |
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
  `Grid.Spawn` and `Grid.Exit` (stairs-down) are pure functions of the
  generated terrain, so — like all derived data — they are saved but **not**
  hashed; the exit is derived without an RNG draw, so adding it left both
  goldens untouched.
- Zone transfer = re-welcome on the live socket (DESIGN §14). Any welcome is
  a full client reset (`resetClientState` in client.js: interp buffers, delta
  baselines, myId, map, fades/VFX, bag layout) AND the server resets that
  client's delta-encoder/ack state (`resetClientView`) — the old baselines
  reference a world that no longer exists. `core.Character` extract/inject is
  the portable projection: item IDs **re-mint** at injection (they double as
  sheet mod sources and would collide), the sheet rebuilds from def + level +
  equipment, and pools enter full (a floor is a fresh start). Protocol v10,
  SaveVersion 4.
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
- Client cast VFX key off windup→done view transitions; a windup shorter
  than the send interval (~3 ticks) would render unseen. No skill is that fast.
- Corpses compact away at tick end — fine until on-corpse mechanics matter.
- Inventory is a flat ID-addressed bag — no stacking. Bag *arrangement* is
  client-side presentation state (`bagLayout`): rearranging sends nothing and
  dies with the page. Item icons are hand-drawn SVGs keyed by base id, and
  `BASE_SLOTS` mirrors the server's slot families by hand — new bases touch both.
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
  converge once anyone aggros.
- The descent is single-instance and shared: one player on the stairs takes
  the whole party down (no per-player zones), and a dead spectator doesn't
  carry and gets a fresh-but-actorless view. The instance manager (town hub,
  concurrent floors) is sequenced for later — DESIGN §14.
- Death is terminal for the run — the "YOU DIED" screen, no portal/XP-loss
  economy yet. There's no character store either, so a reconnect rejoins the
  instance's *current* floor (deepest reached), not a fresh floor 1. Both are
  the next slice (Jake's run/portal rules + DESIGN §14 phase 2).
- Floor scaling is level-only: packs gain `levelsPerFloor` (2) levels per
  floor via `ActorDef.PerLevel`; counts, map size, and room count stay
  constant. Density/biome variety is Phase 2 of the roadmap.

## Feature plan (updated 2026-06-29, session 15)

The descent's **core loop shipped** this session: escalating floors via
stairs, character extract/inject, re-welcome on transfer, and depth as the
score (the machinery RISKS.md flagged as "in-process load/rollback" — built
once, two payoffs). What's left of Phase 1, then the queue:

1. **Run/portal economy** (Jake, numbers still open) — the stakes layer the
   descent set up but doesn't yet have. Death costs some XP (suggested:
   never below the current level's floor) and ejects you to your portal; the
   portal starts on floor 1 and re-plants where you stand; a run grants a
   limited number of portal uses — run out and it's over. Cast-on-death
   portal comes later and must carry an opportunity cost (a skill gem slot
   once gems exist) — do not ship it free. Today death is just terminal.
2. **Character store + sessions** (DESIGN §14 phase 2) — pulls connection
   ownership above the instance so a character survives disconnect and a
   fresh run starts at floor 1 (today a reconnect rejoins the current floor).
   This is also what makes the portal economy meaningful across logins.
3. **A boss with telegraphed multi-stage attacks** — forces deliberate
   action-model growth (RISKS.md #1; design the state machine first) and
   gives a descent milestone a real destination.

Then server hardening (replay log, per-client send queues) when strangers
connect. The single-instance Sim-swap is deliberately the whole descent for
now; an instance manager comes only when something needs it.

## Session log

- **2026-06-29 (15)** — The descent (Phase 1 core). New `Grid.Exit`
  (stairs = farthest room from spawn, derived without RNG → goldens
  untouched; saved like `Spawn`, not hashed). `sim/core/character.go`:
  portable `Character` extract/inject (item IDs re-mint, sheet rebuilds
  from def+level+gear, pools enter full) + `FloorSeed`. `Sim.ScatterSpawnLeveled`
  scales packs by depth (refactored `ScatterSpawn` to keep its draws
  byte-identical). Server: a host-level `descend` command (never a sim
  command) validates proximity to the stairs, then extracts everyone →
  builds the next floor (`buildFloor`, +2 levels/floor) → injects →
  swaps the running Sim → re-welcomes all (resetting per-client encoder
  state). Client: any welcome is a full reset, stairs render + "Press F"
  prompt, `Floor N` HUD. Protocol v10, SaveVersion 4. Tests: character
  round-trip, exit derive/save, wire-level descend, floor-scaling.
  Verified in the browser: walked the stairs, descended floor 1 → 2,
  re-welcome clean, floor-2 packs at level 3 (and lethal — they killed
  the player). Deferred: run/portal economy, sessions, density variety.
- **2026-06-12 (14c)** — Docs only: DESIGN.md §14 settles the
  character/zone/instance/server separation (worlds self-contained;
  characters are server-owned projections; item IDs re-mint at injection;
  transfer = full-reset re-welcome; run seed derives floor seeds;
  single-instance Sim-swap before any instance manager).
- **2026-06-12 (14b)** — XP and levels. New `sim/progress` (AwardXP off
  death events, 100·level² curve, cap 50, ding heal); `Actor.SetLevel`
  rebuilds `Def.PerLevel` mods under `LevelModSource`; monsters carry
  levels/XP/growth for floor scaling. SaveVersion 3, hash covers level+XP,
  protocol v9, HUD level badge + XP bar. Goldens re-recorded.
- **2026-06-12 (14)** — Loot 2.0. Rolled implicit per base, affix pool
  10 → 32 with tiered groups, per-actor drop tables with rarity weights,
  `EvLootStarved` on pool starvation. SaveVersion 2, item hash covers
  rarity+implicit, protocol v8. Only `golden_slice` re-recorded.
- (older sessions pruned — git history is the archive)
