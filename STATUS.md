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

**Last updated: 2026-06-10** (session 10: full equipment slot set, slot-addressed equip, bag rearranging)

## Where things stand

The game is playable in a browser: `cmd/server` hosts the sim (TCP/NDJSON
and WebSocket, same frames) and serves `web/` — a no-build-step canvas
client with click-to-move, Q fireball / E nova / R spark, drop pickup, a
drag-drop inventory panel (I) with item icons and hover tooltips, HUD
orbs, an event log, and a death screen. The full
item flow works (kill → drop → pickup → bag → equip → affixes on the
sheet); damage runs the whole pipeline, and elemental hits inflict
ailments (ignite/chill/shock) with client-side visuals. Run it:

```sh
go test ./...                                    # ~30 tests, all green
go run ./cmd/headless -script scripts/slice.json # watch the fight as events
go run ./cmd/server -scenario scripts/arena.json # then open localhost:8080
                                                 # admin dashboard: localhost:9090
```

All foundational machinery from DESIGN.md is real, not stubbed:

| System | Where | State |
|--------|-------|-------|
| Fixed-point math (no floats in sim) | `sim/fixmath` | done, tested |
| Geometry + projectile sweep | `sim/space` | done; `Walkable` seam exists, pathing doesn't |
| Stat algebra (flat/inc/more/override + tags) | `sim/stats` | done, tested, memoized |
| World/Actor/Hit/defs, RNG, state hashing | `sim/core` | done |
| Damage pipeline + DoTs + regen | `sim/combat` | done, tested |
| Ailments: ignite (DoT) + chill/shock (timed sheet-modifier statuses, strongest-wins) | `sim/combat/ailments.go` | done, tested |
| Actions (windup/recovery) + projectiles | `sim/skills` | done |
| Loot: rarity, weighted affixes, group caps | `sim/items` | done, tested |
| Equipment: 10 slots (weapon…belt), slot-addressed equip command (auto fallback), affix→sheet | `sim/items/equip.go` | done, tested |
| Inventory: pickup/unequip/drop_item, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, joins/leaves, send-rate decoupling, interest culling, binary deltas + acks, pause | `server/` | done, race-tested |
| Admin dashboard: observe (tick health, counts, bandwidth, events, world hash) + poke (pause/resume, spawn, kick), own port, embedded HTML | `server/admin.go` | done, tested; NO AUTH — localhost/tailnet only |
| Web client: canvas, input, drag-drop inventory grid (icons, tooltips), delta decoding, tick-timeline interpolation, fade-in/out, cast/impact VFX + ailment rings | `web/` | working, no build step |
| AI (`melee_chaser`) | `sim/ai` | minimal but real |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome, JSON snapshots, binary delta view codec | `protocol/` | done, tested |
| Content tables | `content/` | fireball, frost_nova (AoE), spark, zombie_slam, 3 actors, 10 affixes, 9 bases (one per slot family) |
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
- Ailment statuses grant their sheet modifiers under
  `StatusKind.ModSource()` (high bit set — disjoint from item-ID sources);
  `TickStatuses` removes them at expiry. Chill consumes no combat RNG and
  shock rolls only on lightning damage — `TestAilmentRNGConsumption` pins
  the stream alignment so old fire-only replays stay stable.
- Golden replay: any behavior change fails `TestGoldenReplay`. If the change
  is intentional, re-record: `DRAUPFORGE_UPDATE_GOLDEN=1 go test ./sim/ -run
  TestGoldenReplay` and commit the new `sim/testdata/golden_slice.txt`.
- `protocol/binary.go` and `web/net.js` are a hand-maintained codec pair.
  Any wire change updates both AND bumps `protocol.Version` — a stale client
  fails loudly at the welcome instead of misreading frames.

Structural risks live in `RISKS.md` — read it before building anything load-bearing (top one by far: no persistence story).

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
- No actor-actor collision; movement is straight-line on an open plane.
- AI keys off a magic string (`"melee_chaser"`); fine until ~3 behaviors.
- `zombie_drops` table is 100% drop chance — tuned for proving loot, not play.
- Snapshots are full-world JSON; delta/interest management is a server concern.

## Natural next steps (in rough order of leverage)

1. Map gen + pathing behind `space.Walkable`.
2. Server hardening: replay log, per-client send queues.
3. World persistence (RISKS.md #1) + the dashboard's operate tier
   (save/load/rollback, parked at the bottom of RISKS.md) — natural pair;
   the observe/poke tiers already exist to receive it.
4. Client prediction for own-character feel (the wasm question) — only if
   input latency starts to grate; interpolation covers everything else.
5. A second monster (ranged or caster) — the ailment/VFX machinery is in
   place; one more AI behavior would make packs tactically interesting.

## Session log

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
- **2026-06-10 (9)** — Inventory UX. Panel rebuilt: fixed labeled
  equipment slots + a capacity-sized inventory grid (one item per cell),
  procedural SVG icons tinted by rarity, hover tooltips (name/rarity/
  affixes), and HTML5 drag-drop — bag→equipment equips, equipment→bag
  unequips, bag→canvas drops at your feet. Click-to-equip removed.
  Protocol v4: `inv_size` rides the actor identity field group so the
  client can draw real capacity. Client now logs unequips (it never had
  that case). Verified end-to-end in headless Chrome, including all three
  drag gestures over the binary wire. No sim changes; golden untouched.
- **2026-06-10 (8)** — Ailments + game feel. Chill/shock as a status system
  (`sim/combat/ailments.go`): timed sheet-modifier packages under
  `StatusKind.ModSource()`, strongest-wins like ignite; chill = chance-free
  More-layer slow on move/attack/cast scaled by hit size (5% floor, 30%
  cap, 2s), shock = chance-rolled increased damage taken (50% cap, 2s).
  New `spark` skill (fast lightning projectile, 30% shock, wild 3–28
  rolls) on R; lightning affixes. Protocol v3: `ail` bitmask on actor
  snaps (both codecs + JS mirror, parity-checked Go→node). Client: ailment
  rings, spark colors, chill/shock log lines, and client-side VFX on the
  server timeline — frost nova shard-ring out to the real 4u radius,
  zombie-slam ground-crack star (keyed off windup→done transitions, so
  whiffed casts still read), per-skill impact starbursts, camera shake
  when you're hit. Verified in-browser via headless Chrome. Golden
  re-recorded: slice's nova now chills, and the hash covers statuses.
- **2026-06-10 (7)** — Quality-of-play pass. Client interpolation re-keyed
  from arrival time to the server tick timeline (`tick × tickMs` + a
  clock-offset estimate that locks to fastest arrivals, decays slowly, and
  resnaps after stalls) — jitter no longer leaks into render timing; actors
  and drops fade in/out at interest edges instead of popping. Server pause:
  admin-driven, loop keeps ticking (joins, views, admin ops) but Step is
  skipped and commands dropped; clients get a "pause" control frame
  (protocol v2). Admin dashboard on its own port (`-admin`, default :9090,
  NO AUTH): status/pause/spawn/kick JSON API + embedded HTML page; handlers
  run closures on the tick goroutine, so no new locking of the world.
  `fixmath.Mul` overflow guard closed RISKS.md #2 (entries renumbered). No
  sim behavior changes; golden replay untouched.
- **2026-06-10 (6)** — Netcode overhaul, one pass as planned: sim stays
  30Hz, views send every N ticks (default 3) with events accumulated;
  `BuildSnapshotFor(viewer, radius, events)` does server-side interest
  culling; WS wire is now binary delta frames (`protocol/binary.go` +
  `web/net.js` mirror, verified byte-identical over a real fight) with
  client acks, keyframe fallback, and permessage-deflate; web client
  reconstructs views and renders ~150ms behind with position lerping.
  Welcome frame now carries `protocol.Version` + cadence (closed RISKS.md
  #5). TCP/nc wire unchanged (full-world JSON); `/ws?format=json` debugs a
  culled view. No sim behavior changes; golden replay untouched.
- (older sessions pruned — git history is the archive)
