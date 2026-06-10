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

**Last updated: 2026-06-10** (session 5: netcode overhaul planned, not yet built)

## Where things stand

The game is playable in a browser: `cmd/server` hosts the sim (TCP/NDJSON
and WebSocket, same frames) and serves `web/` — a no-build-step canvas
client with click-to-move, Q fireball / E nova, drop pickup, an inventory
panel (I), HUD orbs, an event log, and a death screen. The full item flow
works (kill → drop → pickup → bag → equip → affixes on the sheet); damage
runs the whole pipeline. Run it:

```sh
go test ./...                                    # ~25 tests, all green
go run ./cmd/headless -script scripts/slice.json # watch the fight as events
go run ./cmd/server -scenario scripts/arena.json # then open localhost:8080
```

All foundational machinery from DESIGN.md is real, not stubbed:

| System | Where | State |
|--------|-------|-------|
| Fixed-point math (no floats in sim) | `sim/fixmath` | done, tested |
| Geometry + projectile sweep | `sim/space` | done; `Walkable` seam exists, pathing doesn't |
| Stat algebra (flat/inc/more/override + tags) | `sim/stats` | done, tested, memoized |
| World/Actor/Hit/defs, RNG, state hashing | `sim/core` | done |
| Damage pipeline + DoTs + regen | `sim/combat` | done, tested |
| Actions (windup/recovery) + projectiles | `sim/skills` | done |
| Loot: rarity, weighted affixes, group caps | `sim/items` | done, tested |
| Equipment: slots, equip command, affix→sheet | `sim/items/equip.go` | done, tested |
| Inventory: pickup/unequip/drop_item, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WebSocket transports, joins/leaves | `server/` | done, race-tested |
| Web client: canvas, input, inventory UI | `web/` | working POC, no build step |
| AI (`melee_chaser`) | `sim/ai` | minimal but real |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types (commands/snapshots, JSON) | `protocol/` | done for debug use |
| Content tables | `content/` | fireball, frost_nova (AoE), zombie_slam, 3 actors, 8 affixes, 2 bases |
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
- Golden replay: any behavior change fails `TestGoldenReplay`. If the change
  is intentional, re-record: `DRAUPFORGE_UPDATE_GOLDEN=1 go test ./sim/ -run
  TestGoldenReplay` and commit the new `sim/testdata/golden_slice.txt`.

Structural risks live in `RISKS.md` — read it before building anything load-bearing (top two: no persistence story, fixed-point overflow under power creep).

## Known shortcuts (deliberate, fine for now)

- Conversion stage in the pipeline is identity — the slot in the order is
  reserved, no skill converts yet.
- Leech, block, stun, chill/shock (only ignite exists), ES recharge: absent.
- Corpses compact away at tick end — fine until on-corpse mechanics matter.
- Inventory is a flat ID-addressed bag — no spatial grid, no stacking.
- Server: no auth, no persistence (disconnect deletes the actor and its
  items), full-world snapshots every tick (no deltas/interest), one instance
  per process, and a slow client can stall a tick for up to 1s (no per-client
  send queues). All fine for a debug server; all on the list for a real one.
- Web client renders raw 30Hz snapshots — no interpolation (slight stutter)
  and no client prediction (input feels its latency). Both are known phase-2
  work; prediction is the thing that would justify compiling sim/ to wasm.
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

1. **Netcode overhaul — planned with Jake, build next session, one pass.**
   Decision context lives in DESIGN.md §13 (replication spine; snapshot =
   per-client view; lockstep/wasm demoted to optional later layer). Scope:
   - *Send-rate decoupling*: sim stays 30Hz; network send rate ~10–15Hz,
     configurable per instance.
   - *Client interpolation*: web client buffers snapshots and renders
     ~100–150ms behind, lerping entity positions — fixes the 30Hz stutter
     and makes the lower send rate invisible.
   - *WS compression*: permessage-deflate via coder/websocket options.
   - *Interest management*: per-client relevance (radius around the player
     to start), server-side culling at snapshot build. BuildSnapshot grows a
     viewer argument. Events filtered to relevant entities too.
   - *Binary delta encoding*: per-client baseline of last-acked view; send
     field-level diffs + entered/left-view entity lists; client acks by tick;
     full-baseline resend on ack gap. Keep full-JSON snapshots available as
     a debug mode and for the TCP/nc wire.
   - Sim itself should need no changes (snapshot building and transport
     only); golden trace should survive untouched — treat any golden diff
     as a red flag.
2. **Chill/shock ailments** — cold/lightning hits should do something besides
   damage; chill wants a status-effect notion beyond DoTs (a slow), which is
   the small system ignite didn't force.
3. Map gen + pathing behind `space.Walkable`.
4. Server hardening: replay log, per-client send queues, delta snapshots.

## Session log

- **2026-06-10 (5)** — Planned the netcode overhaul (see next steps #1) and
  recorded the networking model decision in DESIGN.md §13. No code.
- **2026-06-10 (4)** — Web client. Server refactored onto a transport
  interface (TCP/NDJSON + WebSocket via coder/websocket, first dependency);
  cmd/server serves /ws and the static client. web/ is vanilla JS + canvas.
  Protocol additions: radii on actor/projectile snaps, IDs on item snaps.
  No sim changes; golden untouched.
- **2026-06-10 (3)** — Inventory (flat bag, pickup/unequip/drop_item commands,
  displaced-item routing, capacity rules) and the server layer (`server/`:
  TCP/NDJSON instance hosting, joins/leaves at tick boundaries, command
  authority override, pre-welcome command buffering; race-tested). Golden
  re-recorded: slice now does pickup → equip-from-bag.
- **2026-06-10 (2)** — Equipment (slots, `equip` command, affix→modifier via
  item-ID source, displaced items drop at feet, pool clamping) and Frost Nova
  (SkillNova AoE kind, independent roll per target). Golden re-recorded:
  content pool + slice scenario changed (scenario now equips the drop).
- **2026-06-10 (1)** — Architecture Q&A with Jake. Wrote DESIGN.md. Built the
  whole sim spine + vertical slice with determinism/golden tests.
