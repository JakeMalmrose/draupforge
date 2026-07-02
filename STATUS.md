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

**Last updated: 2026-07-01** (session 29: equip-compare tooltips —
hovering a bag item shows what it would replace)

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

Under it: characters extract/inject across worlds (`sim/core/character.go` —
item IDs re-minted, sheet rebuilt, pools carried; zone-local state dropped),
and every transfer is a re-welcome on the same socket (welcome generation
tags acks so stale ones die with their world). All of session 14's game
loop still holds: click-to-move with pathing, Q/E/R/T skills, the full item
flow, ailments, kiting archers. Run it:

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
| Loot: per-table rarity weights, weighted affixes, group caps, per-slot affix pools (`AffixDef.Families`, DB() asserts 3+3 groups per family), rolled base implicits, starved-pool event | `sim/items` | done, tested |
| Currency: `Actor.Orbs` wallet (transmute/alch/chaos), orbs bank straight to the killer (one loot draw per kill, rates ×2/×3 by dier rarity), `apply_orb` on bag items (transmute normal→magic, alch normal→rare, chaos rerolls rare — shared `fillAffixes`), durable (save v8, transfers), wire v15, panel orb strip with apply-mode | `sim/items/loot.go`, `web/` | done, tested |
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
| Content tables | `content/` | 8 skills (fireball, frost_nova, spark, zombie_slam, bone_arrow, adrenaline, ghoul_claws, arc_bolt), 6 actors (player/zombie/archer/dummy/ghoul/mage), 32 affixes (tiered groups, per-slot pools), 9 bases (one per slot family, each with a rolled implicit), 5 drop tables, 4 monster rarity mods, 2 buffs |
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
- Characters transfer only durables (def/level/XP/pools/bag/gear); position,
  action, buffs, DoTs — and `Home`, which re-anchors at the injection
  point — deliberately die with the zone. Life ≤ 0 at injection
  means "arrive refilled" — the death-respawn convention.

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
- No actor-actor collision (archers can stack on one tile).
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
- Run state (floor, portals, run seed) is host-layer and NOT in World.Save:
  `-load` resumes the world as floor 1 of a fresh run. Fine until runs are
  worth persisting.
- The run is per-instance, designed single-player-first: any player's death
  ejects *everyone* to the portal, and portal/stairs travel moves the whole
  instance (co-op parties live and die together). One eject consumes one
  portal use however many died that tick.
- Numbers all open for tuning (Jake): death costs 1/5 of the level's XP
  requirement; packs gain +1 monster and +1 level per floor; monster XP
  scales linearly with level; hideout trips cost 1 use, returns free;
  rarity chances (magic 10% +2%/floor cap 30%, rare 2% +1%/floor cap 12%),
  XP multipliers (×3/×6), and drop attempts (2/3) per rarity; portal
  grace lasts 2.5s; flasks bank 60 charges, sips cost 30, kills pay 10.
- Cast-on-death portal still deliberately unshipped — it must carry an
  opportunity cost (a gem slot once gems exist), never free.

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

- **2026-07-01 (29)** — Equip-compare tooltips (client-only). Hovering
  a bag item appends "— equipped (slot) —" blocks for whatever its
  legal slots currently hold (both rings, for rings), PoE-style — the
  upgrade decision without cross-referencing the equipment row.
- **2026-07-01 (28)** — Minimap (client-only). Terrain baked once per
  welcome onto an offscreen canvas (3px/tile), live dots each frame:
  self white, other players blue, monsters red (rarity-colored for
  elites), stairs gold, portal cyan. Entities only show in interest
  range — the radar shows what you sense; stairs/portal are floor
  knowledge and always show. Kills the wander-for-stairs problem.
- **2026-07-01 (27)** — Currency crafting-lite (loot gets a use beyond
  equip-or-ignore). Three PoE1 orbs in an `Actor.Orbs` wallet:
  transmutation (normal→magic), alchemy (normal→rare), chaos (reroll a
  rare) — all through the shared `fillAffixes` (extracted from
  RollItem), so per-slot pools and group caps apply to crafts too.
  Orbs bank straight to the killer on kills (own spin: no ground
  pickup friction) — one loot-stream draw per eligible kill, rates
  90/30/15‰ scaled ×2/×3 by dier rarity; slice golden re-recorded
  (dungeon trace has no kills, stood). apply_orb is a bag-item verb
  (equipped items excluded — their mods live on the sheet). Wallet is
  durable: Character.Orbs, SaveVersion 8, conditional hash. Wire v15:
  wallet field group + orb command. Client: panel currency strip —
  click an orb to arm it, click a bag item to craft; orb finds and
  crafts narrate in the log.
- **2026-07-01 (26)** — The floor guardian. `bone_colossus`: a slow
  1.1-radius heavyweight with two heavily telegraphed attacks —
  `colossus_slam` (1.2s windup, 3.5-radius nova) up close,
  `bone_volley` (0.8s draw, fat slow projectile) at range — picked
  statelessly by distance (`boss_brute`), so it fits the
  one-action-at-a-time model without touching RISKS #1; the real
  multi-stage action redesign stays a deliberate future session.
  `SpawnRareLeveled` guarantees it spawns rare with 2 mods, so the
  x6 XP / 3-drop-attempt rarity hooks come along free, plus an
  always-drops rare-heavy `boss_drops` table. `buildFloor` parks one
  on the stairs every 3rd floor, two levels hot, leashed tight (14 <
  aggro 18 — a guardian guards, it doesn't chase). Goldens untouched.
- **2026-07-01 (25)** — Flasks (the missing PoE1 recovery verb). Player
  carries a life and a mana flask (`ActorDef.Flasks` names their
  buffs): a sip costs 30 of 60 charges and applies a 4s regen burst
  (life 25/s, mana 15/s) through the existing buff machinery — no new
  effect mechanism, only charges are new state. Kills feed +10 to
  every flask (the AwardXP loop — same reward hook), charges are
  durable (Character.FlaskCharges, SaveVersion 7, hashed when
  present — goldens re-recorded since players now carry charges).
  Wire v14: charges ride their own actor field group. HUD: two vials
  beside the life globe, keys 1/2, drained-graying under one sip.
  Numbers (60/30/10, burst rates) open for tuning.
- **2026-07-01 (24)** — PoE2-style HUD (client-only; the first slice of
  Jake's "PoE2 GUI, PoE1 mechanics" direction). Life/mana globes with
  rising-liquid fills and glassy highlights flank a clickable skill bar
  (Q/E/R/T slots: colored glyphs, names, key badges, cast flash,
  drained-graying when mana runs short — costs mirrored client-side
  like BASE_SLOTS); thin XP strip between. SKILL_BAR is now the single
  source for skill keybinds (keydown and clicks share castSlot).
  Renamed the bar's class to .skill-slot after catching a collision
  with the inventory grid's .slot. Verified live: globes drain/refill,
  slots gray at low mana, inventory panel unharmed.
- **2026-07-01 (23)** — Passive forks (ROADMAP phase 3's
  "ascendancy-lite"). `PassiveDef` (6 defs: milestones 5 and 10, three
  forks each — tank/precision/caster at 5, damage/mobility/spellcaster
  at 10); `choose_passive` through the normal command gate (level ≥
  milestone, one pick per milestone, permanent, no RNG); mods live
  under `PassiveModSource` (bit 63+60 — fourth disjoint shared-source
  space). Durable character state: transfers (`Character.Passives`),
  SaveVersion 6, conditionally hashed (goldens stand). Wire v13:
  passive table rides the welcome, chosen IDs are their own actor
  field group. Client: a PoE-ish chooser card appears while a
  milestone is unlocked+untaken, clears only when the server confirms
  the pick. Verified over a live server: table arrives, card renders
  through the real view pipeline, click sends the command, level gate
  drops it for an under-leveled actor.
- **2026-07-01 (22)** — Loot juice, client-only. Magic/rare drops throw
  pulsing light shafts (rare taller than magic) so a rare kill's triple
  drop reads across the room; every drop lands with a ground ring (tiny
  for normal, wide + colored for magic/rare) off the EvDrop event; drop
  nameplates take the rarity color. Verified live: staged all three
  rarities side by side in the browser.
- **2026-07-01 (21)** — Two new archetypes (ROADMAP phase 2 pack
  variety), pure content + existing machinery. Grave ghoul: fast
  (5.5 u/s — outruns a bootless player), fragile, quick shallow claws;
  exists to force target priority. Skeleton mage: tanky caster backline,
  arc_bolt (0.6s windup lightning bolt, 35% shock, spark's wild-roll
  identity but harder-hitting). Own drop tables (ghouls drop rarely and
  lean quick gear; mages lean jewelry with real rarity odds). Client:
  per-def body colors (DEF_COLORS), arc_bolt/claws projectile + impact
  palettes. arena.json packs now mix 5 archetypes. Goldens untouched.
- **2026-07-01 (20)** — Eject grace. `portal_grace` BuffDef (DamageTaken
  overridden to 0 for 2.5s — zeroed hits also starve ailments, whose
  magnitudes scale off dealt damage); the host grants it to every client
  actor after a death eject or run-over arrival via `combat.ApplyBuff`
  between ticks (no RNG, no pending state — safe at the swap seam).
  Voluntary travel (stairs, portal trips) stays unshielded. The client's
  existing buff ring shows it. Server test pins: buff present after
  eject, slam bounces during grace, hurts after expiry. Goldens
  untouched.
- **2026-07-01 (19)** — Per-slot affix pools (ROADMAP phase 3's "an item
  is exciting when it's good *for me*"). `AffixDef.Families` (nil = any)
  + `AllowedOn`; `pickAffix` filters by the base's slot family. All 32
  affixes tagged PoE-flavored: damage on weapons (+rings flat), defences
  on armour, move speed on boots alone, crit/cast on weapon+amulet.
  content.DB() panics if any family's pool can't fill a rare (3+3
  distinct groups). Slice golden re-recorded (filtered pools shift
  weighted rolls); dungeon golden unchanged. New test: 150 rare rolls
  per base — every affix legal for its slot, boots do roll move speed.
- **2026-07-01 (18)** — Combat juice (ROADMAP's cross-cutting slice).
  Hit events now carry `Crit` (sim → EventSnap → binary wire, protocol
  v12, net.js in lockstep; headless prints " CRIT"). Client: floating
  damage numbers (crit = big/gold/lingers, damage-on-you = red, chip
  damage keeps a decimal), white hit flashes (`flashes` map consulted
  against the interp clock), death pops (rarity-colored, bigger for
  magic/rare), all on the existing server-timeline effect system; dead
  entities' positions resolve via the previous view. Verified live in
  Chrome: real fight (crit line in the log over the binary wire), all
  four elements on screen, zero console errors.
- **2026-07-01 (17)** — Magic/rare monsters. `MonsterModDef` (4 mods:
  Fleet/Brawny/Deadly/Stalwart) as permanent sheet packages under
  `MonsterModSource` (bits 63+61); `ScatterSpawnPack` rolls rarity + mods
  off RNGMap — zero chances are stream-identical to `ScatterSpawnLeveled`
  (test-pinned), so goldens stand un-re-recorded, and rarity hashes only
  when rolled (nil-grid trick). Rare pays ×6 XP and 3 drop attempts,
  magic ×3 and 2. `buildFloor` scales chances with depth. Wire: rarity +
  mod names in the actor identity group, protocol v11 + net.js; client
  draws rarity rings and colored nameplates. SaveVersion 5. Verified
  live: 60-zombie world spawned 5 magic + 1 rare over the TCP wire, and
  a Go-encoded frame decodes correctly through net.js in node.
- **2026-07-01 (16)** — Territorial aggro (the spawn-camp fix). Monsters
  now acquire targets by line of sight, or hearing (AggroRadius/2)
  through walls; a leashed monster (`ActorDef.LeashRadius`, zombie 20 /
  archer 24) only engages enemies within leash of its `Actor.Home` and
  walks home otherwise — stateless, so no boundary oscillation. Home is
  set at spawn/injection, saved (SaveVersion 4) and hashed; goldens
  re-recorded (hash layout + dungeon AI behavior). Death-eject no longer
  lands in a permanently converged pack: the pack can't hear you through
  walls at range, and whatever chased you goes home.
- **2026-07-01 (15)** — The descent + hideout, e2e. Sim side stayed thin
  and golden-neutral: `core.Character` extract/inject (IDs re-minted,
  sheet rebuilt, pools carried, Life≤0 = arrive refilled),
  `Actor.AddItemMods` shared with equip, `SpawnLeveled`/
  `ScatterSpawnLeveled`, XP × monster level. Everything else is host
  layer (`server/descent.go`): per-floor world builds off derived seeds,
  stairs = farthest walkable tile, swap = extract → inject → re-welcome
  with an atomic actor/gen/pending cutover, death→portal-eject→run-over
  chain, plant/enter portal verbs riding the transport like acks,
  hideout as floor 0. Protocol v10 (welcome gen/stairs/run, "run"
  frames, gen-tagged acks) — no binary format change. Two real bugs
  found by the new e2e suite, both fixed: tick-0 views could become
  delta baselines (baseTick-0 keyframe sentinel collision), and
  commands decoded mid-swap could drive the old entity ID's new owner.
  Client: full reset on any welcome, stairs/portal rendering +
  click-to-use, P to plant, run HUD. Verified live in the browser:
  descend, plant, hideout round trip, death eject, run over — and the
  spawn-camp death spiral (see shortcuts).
- **2026-06-12 (14, a–c)** — Loot 2.0 (rolled implicits, 32-affix pool,
  per-actor drop tables; protocol v8, SaveVersion 2) and XP/levels
  (`sim/progress`, quadratic curve, PerLevel growth, ding heal; protocol
  v9, SaveVersion 3, goldens re-recorded); DESIGN.md §14 settled the
  character/zone/instance separation the descent was then built on.
- (older sessions pruned — git history is the archive)
