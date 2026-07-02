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

**Last updated: 2026-07-02** (session 34: skill & support gems — gems-only
player skills, uncut drops with cutting drafts, sockets, jeweller orb)

## Where things stand

The game is a game now: you descend. `cmd/server` hosts one run at a time —
floors are whole fresh Worlds seeded from (run seed, floor index); stairs at
the far end of each floor take everyone a level deeper; packs grow and level
with depth (and pay leveled XP). Death costs XP (never de-levels) and ejects
everyone to the portal, burning one of the run's portal uses (`-portals`,
default 3) — none left and the run is over: a new run starts on a fresh seed,
best-floor kept as the score, the character (level/XP/gear) surviving it all.
The portal starts at the floor-1 spawn, re-plants wherever you stand (P), and
walking into it travels to the hideout — a small safe world, floor 0 — for
one use; stepping back through is free. The HUD shows run · floor · portals ·
best; stairs and portal render in-world and are click-to-use.

Player skills are gems now (session 34): a fresh exile knows only a level-1
Fireball gem; uncut skill and support gems drop from kills carrying a
pre-rolled draft of three choices, and cutting is deterministic — cut a new
skill at the drop's level (1–20, from the dier's level), raise an existing
gem to it, or socket a support (more/less damage, extra projectiles, chain,
conversion, speed, mana). Sockets live on the gem (start 1, cap 4); the new
jeweller orb adds one. The Q/E/R/T bar is the gem list.

Under it: characters extract/inject across worlds (`sim/core/character.go` —
item IDs re-minted, sheet rebuilt, pools carried; zone-local state dropped),
and every transfer is a re-welcome on the same socket (welcome generation
tags acks so stale ones die with their world). All of session 14's game
loop still holds: click-to-move with pathing, the full item
flow, ailments, kiting archers. Run it:

```sh
go test ./...                                    # ~55 tests, all green
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
| Persistence: `World.Save`/`LoadWorld` (versioned JSON, content by string ID, bit-exact continuation), admin `POST /api/save`, `cmd/server -load`; descent instances wrap the world in a run envelope (`server/runsave.go`, own version gate) so loads resume mid-run — legacy bare-world files still load as fresh runs | `sim/core/save.go`, `sim/space/save.go`, `server/runsave.go` | done, tested |
| Actions (windup/recovery) + projectiles | `sim/skills` | done |
| Loot: per-table rarity weights, weighted affixes, group caps, per-slot affix pools (`AffixDef.Families`, DB() asserts 3+3 groups per family), rolled base implicits, starved-pool event | `sim/items` | done, tested |
| Currency: `Actor.Orbs` wallet (transmute/alch/chaos/jeweller), orbs bank straight to the killer (one loot draw per kill, rates ×2/×3 by dier rarity), `apply_orb` on bag items (transmute normal→magic, alch normal→rare, chaos rerolls rare — shared `fillAffixes`; jewellers go to gems via `add_socket`), durable (save v8, transfers), panel orb strip with apply-mode | `sim/items/loot.go`, `web/` | done, tested |
| Gems: players cast only from cut gems (`Actor.Gems`; `ActorDef.StartingGems` seeds Fireball, monsters keep `Def.Skills`); uncut gem items drop with a 3-distinct-choice draft rolled at drop time (loot stream; per-table permilles ×2/×3 by rarity), level = dier level capped 20; verbs `cut_skill` (new gem at drop level, replace at the 4-gem cap), `level_gem` (raise-to-drop-level), `cut_support` (tag-gated, socket-addressed, swap destroys), `add_socket` (jeweller, cap 4); supports fold into the socketed skill's queries only (more/less, added flat, cast/attack speed, mana mult, +projectiles fan, chain with LoS retarget, fire/lightning→cold conversion — the pipeline's conversion stage is live, converted portions scale by both types' mods); gem level scales the skill's base roll +10%/level and mana +5%/level; cast contexts bake at use time so in-flight projectiles keep their stats; durable (save v9, transfers, hashed), wire v16 (gem field group + welcome tables), client gem panel/cut dialog/dynamic skill bar | `sim/core/gems.go`, `sim/items/gems.go`, `sim/combat/pipeline.go`, `content/supports.go`, `web/` | done, tested (13 gem tests), verified live in the browser |
| Progression: XP on kill (scaled by monster level), quadratic curve, level cap 50, PerLevel growth mods under `LevelModSource`, ding heal, HUD level + XP bar | `sim/progress`, `core.Actor.SetLevel` | done, tested |
| Flasks: `ActorDef.Flasks` (buff IDs) + `Actor.FlaskCharges`, `use_flask` command (charges-gated, applies the flask's regen-burst buff, legal mid-swing), kills feed +10 capped 60 (same reward hook as XP), durable across transfers/saves (v7), charges group on the wire (v14), HUD vials on keys 1/2 | `sim/`, `content/`, `protocol/`, `web/` | done, tested |
| Passive forks: `PassiveDef` milestones (5/10, 3 forks each), `choose_passive` command (level-gated, one per milestone, permanent under `PassiveModSource`), durable across transfers (`Character.Passives`) and saves (v6), table in the welcome + chosen IDs on actors (protocol v13), client chooser card | `sim/core`, `content/`, `protocol/`, `web/` | done, tested |
| Character extract/inject: portable struct (def/level/XP/pools/gear), IDs re-minted at injection, sheet rebuilt, walkable-clamped | `sim/core/character.go` | done, tested |
| The descent: floor swap (build → extract → inject → re-welcome), run rules (portal economy, XP death penalty, run-over → new run), hideout, leveled+thickened packs, stairs/portal/run on the wire | `server/descent.go`, protocol v10 | done, unit + e2e tested, verified live in the browser |
| Monster rarity: `ScatterSpawnPack` rolls magic (1 mod) / rare (2 distinct mods) off RNGMap; `MonsterModDef` packages under `MonsterModSource`; XP ×3/×6, drop attempts 2/3; floor-scaled chances in `buildFloor`; rarity+mod names in the actor identity group (protocol v11), client rings + nameplates | `sim/sim.go`, `content/`, `protocol/`, `web/` | done, tested (incl. Go→net.js codec parity) |
| Equipment: 10 slots (weapon…belt), slot-addressed equip command (auto fallback), affix→sheet | `sim/items/equip.go` | done, tested |
| Inventory: pickup/unequip/drop_item, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, joins/leaves, send-rate decoupling, interest culling, binary deltas + acks, pause | `server/` | done, race-tested |
| Admin dashboard: observe (tick health, counts, bandwidth, events, world hash) + poke (pause/resume, spawn, kick), own port, embedded HTML | `server/admin.go` | done, tested; NO AUTH — localhost/tailnet only |
| Web client: canvas, input, terrain render (walls/floor), drag-drop inventory grid (icons, tooltips), delta decoding, tick-timeline interpolation, fade-in/out, cast/impact VFX + ailment rings, floating damage numbers (crit/self emphasis), hit flashes, death pops (rarity-scaled), PoE2-style HUD (life/mana globes, clickable skill bar with mana-gating, `SKILL_BAR` as the single keybind source) | `web/` | working, no build step |
| AI: behavior registry — `melee_chaser`, `ranged_kiter` (LoS-gated shooting, retreat band), `boss_brute` (stateless two-skill selection by range); territorial aggro: LoS/hearing acquisition, leash to `Actor.Home`, return-home (SaveVersion 4) | `sim/ai` | real, tested |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome, JSON snapshots, binary delta view codec | `protocol/` | done, tested |
| Content tables | `content/` | 10 skills (6 cuttable: fireball, frost_nova, spark, adrenaline, arc_bolt, bone_arrow), 10 support gems (`content/supports.go`), 7 actors, 32 affixes (tiered groups, per-slot pools), 9 bases (rolled implicits), 6 drop tables (each with gem permilles), 4 monster rarity mods, 4 buffs |
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
- A tick-0 view is never stored as a delta baseline: `baseTick 0` is the
  wire's keyframe sentinel and ack 0 is the client reset signal, and a
  freshly swapped world really is at tick 0 (`server.frameFor`;
  `TestDescentOverTheWire` caught the collision).
- Any welcome fully resets the client and the server's per-client encoder;
  welcome `gen` increments per re-welcome and acks must echo it. On a floor
  swap the actor-ID/gen/pending-queue cutover is one mutex section —
  commands tagged with old-world IDs must never drive whichever actor wears
  that ID in the new world.
- Characters transfer only durables (def/level/XP/pools/bag/gear/gems);
  position, action, buffs, DoTs — and `Home`, which re-anchors at the
  injection point — deliberately die with the zone. Life ≤ 0 at injection
  means "arrive refilled" — the death-respawn convention. A character with
  no gems (legacy saves) is re-granted its def's starting gems.
- Support modifiers never touch the actor's sheet: they fold into the
  socketed skill's stat queries at use/resolve time (`GemCtx`), and cast
  contexts bake at the command gate — an in-flight projectile keeps the
  stats it was fired with whatever happens to the gem. The uncut draft is
  rolled at drop time (exactly three loot-stream draws); cutting itself
  consumes no RNG. `content.Supports` and `ContentDB.Cuttable` are ordered
  tables — reordering is replay-relevant, same rule as affixes.

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
- Damage numbers and death pops anchor at the event-tick position and
  don't follow a moving target — invisible at current speeds.
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
- Actor collision is soft separation between monsters only (players
  neither push nor get pushed — body-blocking is a deliberate
  non-feature). Pairwise O(n²) per tick; spatial grid when density hurts.
- Aggro is LoS-gated with a hearing fallback (half aggro radius, through
  walls) — a monster that loses you at range walks home rather than
  haunting a corner. No aggro memory: break LoS beyond hearing range and
  a chaser forgets you instantly.
- AI re-issues its chase target every tick; the half-tile repath throttle
  in `CmdMove` keeps that cheap. A swarm in a maze could still make A* the
  hot path — measure before optimizing.
- Terrain travels as JSON rows in the welcome (~2KB at 48×48) — fine until
  maps get big or revealable (fog of war would change this).
- Kiter retreat picks from 5 fixed directions, no flee pathfinding — a
  cornered archer stands and fights.
- Affix tiers are shallow (one "greater" tier on a few groups) and pools
  don't scale with item/monster level yet — depth for later.
- Spawn-camp pressure is fixed twice over: territorial aggro (session
  16) stops packs converging on the portal room, and death arrivals get
  2.5s of portal grace (session 20) — a portal planted inside a
  territory no longer means an instant re-kill.
- The run is per-instance, designed single-player-first: any player's death
  ejects *everyone* to the portal, and portal/stairs travel moves the whole
  instance (co-op parties live and die together). One eject consumes one
  portal use however many died that tick.
- Numbers all open for tuning (Jake): death costs 1/5 of the level's XP
  requirement; packs gain +1 monster and +1 level per floor; monster XP
  scales linearly with level; hideout trips cost 1 use, returns free;
  rarity chances (magic 10% +2%/floor cap 30%, rare 2% +1%/floor cap 12%),
  XP multipliers (×3/×6), and drop attempts (2/3) per rarity; portal
  grace lasts 2.5s; flasks bank 60 charges, sips cost 30, kills pay 10;
  gem drop permilles per table (trash ~12–25 skill / 8–18 support, boss
  300/200, ×2/×3 by rarity), jeweller orb 40‰, gem damage +10%/level and
  mana +5%/level, support numbers in `content/supports.go`.
- Uncut skill gems drop at the dier's level, so `level_gem` only matters
  once floors (leveled packs) outpace your gems — floor 1 drops are all
  level 1. Deliberate: depth is how gems grow.
- Support display metadata (glyph colors, which skills aim at the cursor)
  is the client-side `SKILL_META` map — new cuttable skills should update
  it or accept the neutral default (aimed, gray).
- Cast-on-death portal still deliberately unshipped — it must carry an
  opportunity cost (now expressible: a gem socket), never free.

## Feature plan

The descent shipped (session 15). The natural queue, unchanged: a boss
with telegraphed multi-stage attacks at floor milestones (forces
deliberate action-model growth, RISKS.md #1 — design the state machine
first), then the character store + sessions (characters survive
disconnects; pulls connection ownership above the instance — DESIGN.md
§14 phase 2), then server hardening (replay log, per-client send queues)
when strangers connect. ROADMAP.md phase 2 (pack variety, juice) is the
fun-first counterweight to all of that.

## Session log

- **2026-07-02 (34)** — Skill & support gems (the loot-driven skill
  system; four design calls from Jake: gems-only skills, draft-of-3
  cutting, sockets start 1 / cap 4, level-to-drop-level). Sim: gem/
  support/uncut types in `core/gems.go`, verbs in `items/gems.go`, the
  reserved conversion stage went live (per-portion multipliers query
  with both source and destination type tags), projectile fans
  (hardcoded fixed-point rotation table), chain (retarget nearest
  unhit enemy, wall-LoS-gated), jeweller orb. Save v9, wire v16 (gem
  field group carries server-computed mana costs; welcome carries
  support/cuttable tables with precomputed legality), 13 new sim
  tests, both goldens re-recorded (gem loot draws shift the stream).
  Client: dynamic gem skill bar, gem panel row with socket pips, the
  cut dialog (draft → replace-at-cap / level-up / support-target
  flows), gem drop shafts. Verified live in Chrome over the binary
  wire: farm → shafted drops → cut (duplicate greyed) → socket GMP
  (Frost Nova gated "incompatible", mana ×1.35 on the bar) → 5-way
  fan (3 survive the corridor on screen; exact server-side check),
  and a mid-session death eject carried gems + uncut items across
  the world swap. Zero console errors.
- **2026-07-02 (33)** — WASD movement (client-only, the PoE2 GUI
  signature). A held-key set sends a short move command every 100ms
  toward the combined direction (~4 units ahead — the repath is cheap
  at that range); releasing the last key sends stop, window blur
  clears the set. Click-to-move coexists: both are just move commands,
  last input wins. Verified live: held S+D walks the spawn corridor.
- **2026-07-02 (32)** — Monster separation. New `skills.Separate` phase
  right after movement: overlapping monster pairs (closer than 80% of
  summed radii) ease apart at ≤0.06 u/tick each, wall-guarded, pure
  position math in slice order — no RNG. Players are untouched.
  Phase-order addition documented in sim.go; both goldens happened to
  stand (their monsters never overlap), pinned by the suite.
- **2026-07-02 (31)** — Run persistence. Descent saves wrap World.Save
  in a host-layer envelope (`runSave`: run/seed/floor/portal
  anchor+budget/best, own version gate) — `-load` resumes mid-run,
  including hideout visits; legacy bare-world files still load as
  floor 1 of a fresh run. Plain arenas keep the bare format. Closed
  the STATUS shortcut. Verified live: admin save → restart with -load
  → instance ticking with the full roster.
- **2026-07-02 (30)** — Audio (ROADMAP's stinger line, client-only).
  A tiny WebAudio synth — every cue is an enveloped oscillator, zero
  asset files: hit thuds (harder when it's you), crit accents, death
  pops (doubled for rares), a level-up arpeggio, orb chimes, drop
  shimmers, flask gulps, travel sweeps. Per-kind throttles keep a
  nova from machine-gunning; master gain 0.15; M toggles mute
  (persisted in localStorage); the context unlocks on first
  click/keypress per browser autoplay rules.
- **2026-07-01 (29)** — Equip-compare tooltips (client-only). Hovering
  a bag item appends "— equipped (slot) —" blocks for whatever its
  legal slots currently hold (both rings, for rings), PoE-style — the
  upgrade decision without cross-referencing the equipment row.
- (older sessions pruned — git history is the archive)
