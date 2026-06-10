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

**Last updated: 2026-06-10** (session 1: design + sim spine + vertical slice)

## Where things stand

The vertical slice works end to end and is locked by tests: a player casts
fireballs (windup → projectile → damage pipeline → ignite chance), a training
dummy dies, loot drops with rolled affixes, and a zombie AI chases the player
and lands melee swings through accuracy/evasion and armour. Run it:

```sh
go test ./...                                    # ~25 tests, all green
go run ./cmd/headless -script scripts/slice.json # watch the fight as events
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
| AI (`melee_chaser`) | `sim/ai` | minimal but real |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types (commands/snapshots, JSON) | `protocol/` | done for debug use |
| Content tables | `content/` | fireball, zombie_slam, player, zombie, training_dummy, 6 affixes, 2 bases |
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

## Known shortcuts (deliberate, fine for now)

- Conversion stage in the pipeline is identity — the slot in the order is
  reserved, no skill converts yet.
- Leech, block, stun, chill/shock (only ignite exists), ES recharge: absent.
- Corpses compact away at tick end — fine until on-corpse mechanics matter.
- Items drop but can't be equipped — `stats.Sheet.Add/RemoveSource` is ready
  for it, there are just no equipment slots on Actor yet.
- No actor-actor collision; movement is straight-line on an open plane.
- AI keys off a magic string (`"melee_chaser"`); fine until ~3 behaviors.
- `zombie_drops` table is 100% drop chance — tuned for proving loot, not play.
- Snapshots are full-world JSON; delta/interest management is a server concern.

## Natural next steps (in rough order of leverage)

1. **Equipment**: slots on Actor, equip/unequip commands, affix→modifier via
   `Sheet.Add(source)` — closes the loop that makes drops *matter*.
2. **Second damage skill with a different shape** (cold melee or lightning
   AoE) — stresses the tag system and forces the AoE story.
3. **`server/`**: tick driver + WebSocket/TCP transport hosting one instance —
   the protocol boundary is ready for it.
4. **Debug renderer** (terminal grid or tiny Ebiten view) — eyes on the arena
   beats reading JSON.
5. Map gen + pathing behind `space.Walkable`.

## Session log

- **2026-06-10** — Architecture Q&A with Jake (headless Go sim core,
  server-authoritative day one, rendering deferred). Wrote DESIGN.md. Built
  fixmath/space/stats/core foundations, combat/skills/items/ai systems, sim
  phase wiring, protocol, content tables, headless runner, determinism +
  golden tests. Vertical slice proven working. 6 commits on `main`.
