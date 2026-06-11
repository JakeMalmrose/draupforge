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

**Last updated: 2026-06-11** (session 12: risk burndown — TagSet widened,
world save/restore + dashboard operate tier, content-defined buffs)

## Where things stand

The game is playable in a browser, now in a generated dungeon: `cmd/server`
hosts the sim (TCP/NDJSON and WebSocket, same frames) and serves `web/` — a
no-build-step canvas client with click-to-move (pathing around walls),
Q fireball / E nova / R spark / T adrenaline (self-buff), drop pickup, a drag-drop inventory panel (I)
with item icons and hover tooltips, HUD orbs, an event log, and a death
screen. The full item flow works (kill → drop → pickup → bag → equip →
affixes on the sheet); damage runs the whole pipeline; elemental hits
inflict ailments (ignite/chill/shock); rooms and corridors make geometry
matter — projectiles stop at walls, monsters path around them, and
skeleton archers kite to firing range. Run it:

```sh
go test ./...                                    # ~40 tests, all green
go run ./cmd/headless -script scripts/slice.json # watch the fight as events
go run ./cmd/server -scenario scripts/arena.json # then open localhost:8080
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
| Loot: rarity, weighted affixes, group caps | `sim/items` | done, tested |
| Equipment: 10 slots (weapon…belt), slot-addressed equip command (auto fallback), affix→sheet | `sim/items/equip.go` | done, tested |
| Inventory: pickup/unequip/drop_item, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, joins/leaves, send-rate decoupling, interest culling, binary deltas + acks, pause | `server/` | done, race-tested |
| Admin dashboard: observe (tick health, counts, bandwidth, events, world hash) + poke (pause/resume, spawn, kick), own port, embedded HTML | `server/admin.go` | done, tested; NO AUTH — localhost/tailnet only |
| Web client: canvas, input, terrain render (walls/floor), drag-drop inventory grid (icons, tooltips), delta decoding, tick-timeline interpolation, fade-in/out, cast/impact VFX + ailment rings | `web/` | working, no build step |
| AI: behavior registry — `melee_chaser`, `ranged_kiter` (LoS-gated shooting, retreat band) | `sim/ai` | real, tested |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome, JSON snapshots, binary delta view codec | `protocol/` | done, tested |
| Content tables | `content/` | fireball, frost_nova (AoE), spark, zombie_slam, bone_arrow, 4 actors (player/zombie/archer/dummy), 10 affixes, 9 bases (one per slot family) |
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
  across content reordering and saves; content.DB() panics on collision).
  Both spaces are disjoint from item-ID sources. `TickStatuses` removes
  them at expiry. Chill consumes no combat RNG, shock rolls only on
  lightning damage, buffs consume none — `TestAilmentRNGConsumption` pins
  the stream alignment so old fire-only replays stay stable.
- Saves are durable state: any new world state ships its save-format
  support (and a `SaveVersion` bump on shape changes) in the same commit.
  Save only at tick boundaries; `World.Save` refuses pending hits/buffs.
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
- `zombie_drops` is 100% drop chance, and archers share it. Spawn-room
  pressure is real: scatter keeps monsters 10u out, but they converge once
  anyone aggros.

## Natural next steps (in rough order of leverage)

Standing recommendation (set 2026-06-11, after the risk-burndown run): the
foundations are no longer the bottleneck — pick by what makes play better.

1. **Quick bite that fits any session: base-item implicits.** Seven of the
   nine bases are stat-less affix-holders. An implicit modifier per base
   (boots = move speed, shield = armour, amulet = a resist) is a small
   `BaseItemDef` addition that makes the slots meaningful and drops worth
   reading.
2. **Dungeon playability pass**, if a play session grates: spawn-room
   safety, arrow projectile leading, a minimap or explored-fog overlay.
   All client/AI polish, no new systems.
3. **Action-model growth (RISKS.md #1)** when a feature wants it:
   channelling or stun/interrupt — design the action state machine
   deliberately before the feature forces it.
4. **Session identity + periodic autosave** — persistence exists; a small
   account/session layer would make disconnects survivable for players,
   not just worlds.
5. Server hardening: replay log, per-client send queues — when strangers
   connect, not before.

## Session log

- **2026-06-11 (12)** — Risk burndown, top three in one run. (a) TagSet:
  uint64 → compile-time-sized word array off `TagCount`; future widenings
  are automatic and golden-invisible. (b) Persistence: `sim/core/save.go`
  + `sim/space/save.go` serialize world ↔ versioned JSON; restored worlds
  continue bit-identically (continuation tests); admin save button/API +
  `cmd/server -load` (orphan player actors removed at load, gear dropped).
  (c) Buffs: `Actor.Statuses` generalized — `BuffDef` content packages,
  `SkillBuff` kind, pending-buff queue resolved before hits, `adrenaline`
  player skill on T, AilBuffed ring. Protocol v7. Goldens untouched (all
  three changes are behavior-neutral for existing scenarios); verified
  live over the TCP wire and a save/restart cycle.
- **2026-06-10 (11)** — Terrain. `sim/space` grows a tile Grid (one shared
  clearance radius, eroded walkability, fixed-point DDA `SegmentHit`,
  deterministic A* with seq-tie-broken heap + string-pulling smoothing) and
  a rooms-and-corridors generator off RNGMap (3-wide corridors so erosion
  keeps a walkable center line; unreachable-tile pruning post-gen).
  `CmdMove` paths at command time (half-tile repath throttle for AI);
  movement follows waypoints; projectiles clip on walls. AI moved to a
  registry; new `ranged_kiter` + `skeleton_archer`/`bone_arrow` content
  (`PreferredRange` on ActorDef). Protocol v6: welcome carries the map as
  ASCII rows; scenarios gained `map` + `scatter`; arena.json is now a real
  dungeon; client renders walls/floor. Second golden (`golden_dungeon.txt`)
  pins mapgen+pathing+kiting; the open-plane golden is untouched (nil-grid
  paths are bit-exact). Verified in headless Chrome: pathing around walls,
  closest-approach wall clicks, archer arrows, death screen.
- **2026-06-10 (10)** — Full equipment + slot-addressed equip. EquipSlot
  grows to the real set (weapon/offhand/helmet/body/gloves/boots/amulet/
  ring1/ring2/belt), one slot family each, one base item per family in
  content (all in the zombie drop table). `CmdEquip` now carries an
  optional slot (`HasSlot` guards the zero value); the sim validates
  family-vs-slot before moving anything, `EquipAuto` keeps the old
  pick-for-me behavior for scripts and gap-drops. Client: equipment is a
  10-cell labeled grid, drags highlight only legal slots (`BASE_SLOTS`
  mirror), drops equip into the named slot, and bag cells are
  rearrangeable client-side (drag to cell swaps; unequip lands in the
  cell you dropped on). Protocol v5. Golden re-recorded (hash covers 10
  slots; loot table widened). Verified in headless Chrome: illegal drop
  bounces, ring2-style targeting, rearrange, unequip-into-cell.
- **2026-06-10 (9)** — Inventory UX. Panel rebuilt: labeled equipment
  slots + a capacity-sized grid, procedural SVG icons tinted by rarity,
  hover tooltips, HTML5 drag-drop (bag→equipment equips, equipment→bag
  unequips, bag→canvas drops); click-to-equip removed. Protocol v4
  (`inv_size` in the identity field group). No sim changes.
- (older sessions pruned — git history is the archive)
