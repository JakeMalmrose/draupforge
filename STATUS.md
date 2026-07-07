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

**Last updated: 2026-07-07** (session 82: the delve chart on `delve-chart` —
the descent is a PoE1-Delve-style node map now: 3-floor nodes, biome
clumps, clear-the-boss-to-travel, wire v28.)

## Where things stand

The game is public and multiplayer: https://nuc.tail4b8d48.ts.net (Tailscale
Funnel → the nuc; CI redeploys on every merge to main). `cmd/server` runs a
Lobby of instances. On connect you pick a character from your account's
roster — or claim your first name, or play as guest. A claim mints a secret
account token in an HttpOnly cookie; the token (never a name) is the auth.
One account holds up to 12 characters (names globally unique; a claim under
an existing cookie grows the roster instead of orphaning anything), the
stash is account-wide, and `?char=` on the WS picks who to play (default:
most recently played). Characters bank to the `-identities` JSON on
disconnect and every 30s. Guests are ephemeral. Every connection gets its own world and
run; party = instance — the F panel lists all online named players, an
accepted invite transfers you into the inviter's world via the floor-swap
machinery, and empty instances reap after 60s, doubling as reconnect grace.

The game is a game now: you descend — from home, across a map. Every run
starts in the hideout (floor 0, a small safe world derived from the
instance seed); its portal leads to the delve chart's entry node. The
chart (PoE1-Delve style, `server/delvemap.go`) is a node lattice derived
purely from the run seed: rows are depth, edges lead down/up/sideways,
biomes land as spatial clumps (blob-Voronoi, depth-weighted), and each
node is a 3-floor dungeon sharing one biome and one rolled mod set. Stairs
walk a node's floors; the third floor's set-piece (guardian; King/Warden
every 3rd row alternating; Tyrant every 10th) CLEARS the node when it
falls — the stairs then open the map (N reads it anytime) and you travel
to any neighbor of cleared ground or anywhere already visited: deeper,
sideways to hold a depth, or back up. Floors are whole fresh Worlds seeded
from (run seed, node, fin); packs grow and level with global depth
((row−1)·3+fin — the ladder/checkpoint/feat currency throughout). Death
costs XP (never de-levels) and ejects everyone to the portal anchor
(node+fin), burning one portal use (`-portals`, default 3) — none left and
the run is over: a new run starts back home on a fresh seed and a fresh
chart, best-floor kept as the score, the character surviving it all. The
portal re-plants wherever you stand (P); a trip home costs one use,
returning is free. The HUD shows run · floor (fin/3) · portals · best.

Player skills are gems (session 34): a fresh exile wakes holding one uncut
skill gem and picks its first skill from the draft (the dialog opens
itself); uncut skill/support gems drop from kills carrying a pre-rolled
draft of three choices — cut a new skill at the drop's level (1–20, from the
dier's level), raise an existing gem to it, or socket a support. Sockets
live on the gem (start 1, cap 4); the jeweller orb adds one. The Q/E/R/T
bar is the gem list. Under it all: characters extract/inject across worlds
(`sim/core/character.go`), and every transfer is a re-welcome on the same
socket. Run it:

```sh
go test ./...                                    # full suite, all green
go run ./cmd/headless -script scripts/slice.json # watch the fight as events
go run ./cmd/server -scenario scripts/arena.json # then open localhost:8080
                                                 # admin dashboard: localhost:9090
go run ./cmd/partybot -name Botty                # a friend to invite (F panel)
```

All foundational machinery from DESIGN.md is real, not stubbed:

| System | Where | State |
|--------|-------|-------|
| Fixed-point math (no floats in sim) | `sim/fixmath` | done, tested |
| Space: tile grid (clearance-eroded walkability), DDA raycast, deterministic A* + smoothing, rooms-and-corridors mapgen off RNGMap | `sim/space` | done, tested |
| Stat algebra (flat/inc/more/override + tags) | `sim/stats` | done, tested, memoized |
| World/Actor/Hit/defs, RNG, state hashing | `sim/core` | done |
| Damage pipeline: conversion, DoTs, regen; the full mechanics set — leech, block, ES recharge, and stun (a hit ≥15% max life clears the action; `Actor.StunTicks` = 0.3s lock + 0.5s immunity, saved+hashed, gated in the command validator; `ActorDef.StunImmune` on bosses) | `sim/combat`, `sim/sim.go` | done, tested |
| Statuses: ignite/chill/shock/bleed/poison ailments + content buffs (refresh-not-stack, pending-buff queue); bleed (phys DoT, strongest wins) and poison (chaos DoT off the phys+chaos portion, the one ailment that STACKS — every application is its own instance, 2s self-cap) roll only when the attacker has any chance (skill base + sheet + support fold) — old replays stay byte-stable | `sim/combat` | done, tested, verified live |
| Persistence: `World.Save`/`LoadWorld` (versioned JSON, bit-exact), admin `POST /api/save`, descent run envelope (v2); `-load` under the lobby seeds the first instance (validated at boot) | `sim/core/save.go`, `server/runsave.go`, `server/lobby.go` | done, tested, verified e2e |
| Actions (windup/recovery) + projectiles; skill feel: splash w/ falloff, wall bounce, heading wiggle, hitscan chains | `sim/skills` | done, tested |
| Staged skills (DESIGN §15): stage sequences w/ per-stage locked aims, blast/ring effects, telegraphs on the wire (v19); telegraphed blasts skip evasion; the Barrow King (boss_king AI, floors %5, rare, stateless enrage <50%) + boss bar + ground-telegraph rendering | `sim/skills`, `sim/ai`, `content/`, `server/descent.go`, `web/` | done, tested, verified live |
| Loot: rarity weights, weighted affixes, group caps, per-slot pools, rolled implicits; item level (= dier's level, floor-scaled) gates affix tiers (ILvl-gated greater/grand tiers) — deeper drops roll strictly better; base tiers always fill (DB() asserts ILvl-0 depth); save v13, wire v21, ilvl on the tooltip | `sim/items`, `content/`, `web/` | done, tested, verified live |
| Uniques: fixed-identity chase items (4 in content) with shape stats nothing else rolls (ExtraProjectiles/ExtraChains — the skill system reads them off the sheet); UniquePermille per table, orange styling + authored mod lines on the wire (v20); orbs refuse them; save v11 | `sim/items`, `sim/stats`, `content/`, `web/` | done, tested, verified live |
| Currency: 8-orb wallet (transmute/alch/chaos/jeweller + regal/exalt/annulment/scouring) banked straight to the killer; `apply_orb` crafts bag items (regal keeps affixes + adds, exalt adds to a rare with room, annul strips one at random, scour wipes to normal; uniques refuse everything); the Forge: `forge_melt` pays shards by rarity (uncut gems flat 5), `forge_buy` sells orbs at fixed shard prices — travels with you, no hideout gate; shards durable (save v19, wire v25) | `sim/items`, `sim/core`, `web/` | done, tested, verified live |
| Gems: cast only from cut gems; a fresh character spawns with `StartingUncut` draft-of-3 gems instead of a fixed starter; uncut drops carry a draft of 3 at the dier's level (cap 20); supports fold into the socketed skill's queries only (more/less, added flat, speed, mana, fans, chain, conversion); cast contexts bake at use; save v9 | `sim/core/gems.go`, `sim/items/gems.go`, `content/supports.go`, `web/` | done, tested, verified live |
| Channelling + cooldowns: `ChannelTicks` skills hold in `PhaseChannel` after the windup's first shot, repeating the effect every use-time-bound interval while `ChannelMana` feeds it; a channel is NOT a commitment — its owner's next move/skill/stop command breaks it (stun too, free); `Actor.Cooldowns` (save v18, conditionally hashed, zone-local — never transfers) gates `CooldownTicks` skills at the command gate; `SkillBlink` teleports range-clamped + wall-honest (far-to-near ClearLine sampling); wire v24 GemSnap.Cd + "channel:" action string; the bar dims and counts down | `sim/skills`, `sim/sim.go`, `sim/core`, `content/`, `web/` | done, tested, verified live |
| Curses: hex BuffDefs (`Curse` flag) on the ordinary buff machinery — SkillCurse casts hex every hostile within AoERadius of the aim (clamped to Range, no RNG, no hits); one curse per target, newest evicts; negative resistance now amplifies (hit + DoT mitigation gates read `res != 0` — inert for pre-curse content); AilCursed wire bit (64), EvCurse; the bone hexer is the support monster that hexes YOU | `sim/combat/buff.go`, `sim/skills`, `content/`, `web/` | done, tested, verified live |
| Auras: SkillAura toggle gems — while on, AuraMods sit on the caster's sheet AND every owned minion's (no radius, by design), max mana reserved via More(-Reserve); event-driven application (toggle, DrainSpawns, injection — no per-tick scans); AuraOn is durable gem state (save v17, hashed, transfers; wire v23 carries it for the bar); gem replace/level keep applied mods honest; +5%/gem-level effect | `sim/core/aura.go`, `sim/skills`, `content/`, `web/` | done, tested, verified live |
| Progression: leveled XP on kill, quadratic curve, cap 50, PerLevel mods, ding heal | `sim/progress` | done, tested |
| Flasks: charge-gated regen-burst sips (keys 1/2), kills feed charges, durable (save v7) | `sim/`, `content/`, `web/` | done, tested |
| Passive forks: a pick-3 milestone every 5 levels to the cap (5–50, 30 passives in themed tiers: procs, defense, tempo, shape (+1 minion/projectile/chain at 30), sustain, elements, gambles, More-capstones at 50), permanent, durable, client chooser; DB() asserts the ladder has no missing rung | `sim/core`, `content/`, `web/` | done, tested |
| Character extract/inject: durables only, item IDs re-minted, sheet rebuilt | `sim/core/character.go` | done, tested |
| The descent: hideout start (`Config.StartFloor`, 0 = home; >0 maps to the chart's trunk), node floor swaps, portal economy (anchor = node+fin), XP death penalty, run-over → new run at home, leveled+thickened packs, per-node set-piece cadence | `server/descent.go` | done, unit + e2e tested, verified live |
| The delve chart: run-seed-pure node lattice (rows deep, 7 cols, guaranteed up/down connectivity, lateral corridors), biome blob-Voronoi clumps, per-node mods (+20% juiced), visited/cleared bookkeeping, fog-of-reveal snapshots, travel validation (stairs + cleared-frontier) | `server/delvemap.go`, `web/` (N panel, SVG map) | done, tested, verified live (wire v28) |
| Monster rarity: magic/rare rolls with mod packages, XP ×3/×6, extra drops, floor-scaled chances, rings + nameplates | `sim/sim.go`, `content/`, `web/` | done, tested |
| Equipment + inventory: 10 slots, slot-addressed equip, pickup/unequip/drop, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, send-rate decoupling, interest culling, binary deltas + acks, pause, per-client send queues (a stalled socket dies alone; the tick never blocks on I/O) | `server/` | done, race-tested |
| Replay log: `-replaydir` records every world as a segment (World.Save header + NDJSON command lines); host surgery (joins/swaps/grace/admin/stash) rotates segments so each spans a pure Step stretch; `cmd/headless -replay` re-executes bit-exact | `server/replay.go`, `cmd/headless` | done, tested, verified e2e |
| Identity: account roster (file v2, v1 auto-migrates) — one 32-byte cookie token, up to 12 named characters; `?char=` selects at the WS door, banks land on the slot in play (tracked by name, so a mid-session delete banks nowhere); one session per account; per-character delete via /api/forget (last one takes the account); character-select join screen | `server/identity.go`, `web/` | done, tested, verified live |
| Stash: account-wide hideout bank (240 items as four 60-slot client tabs, durable CharItem form on the identity, shared by every character — the alt loop); stash_put/stash_take verbs, hideout-only, processed at the host layer between ticks; drag between bag and stash in the panel | `server/stash.go`, `server/identity.go`, `web/` | done, tested, verified live |
| Lobby: many instances per process, party = instance, invite/leave transfers via floor-swap machinery, 60s empty reap = reconnect grace | `server/lobby.go`, `cmd/partybot` | done, race-tested, verified live |
| The Living Descent (ROADMAP v2 Track 2): biomes (cave mapgen kind, palettes/drones — now chart clumps, not depth bands), floor mods (now node-scoped; the v27 route/chamber chart superseded by the delve chart), account checkpoints w/ level-gated deep starts, per-character ladder w/ builds + death recap, hardcore (memorials) + SSF modes, 8 feats + hideout trophies, instance chat + map pings, stash tabs + settings panel | `server/biome.go`, `server/floormod.go`, `server/ladder.go`, `server/feats.go`, `server/chat.go`, `sim/space/mapgen_caves.go`, `web/` | done, tested, verified live |
| Hosting + CI/CD: public via Tailscale Funnel; every push to main builds, swaps (prev kept), restarts, health-checks; `identities.json` never touched. CI gates every PR and main push: `go vet`, race-tested suite, JS syntax | `.github/workflows/deploy.yml`, `ci.yml` | done, verified e2e |
| Admin dashboard: observe (tick health, counts, events, hash, run line) + poke (pause, spawn, kick, save) + dev cheats (god/gem/orbs); lobby index at `/i/{id}/` | `server/admin.go` | done, tested; NO AUTH — on the nuc it binds loopback, tailnet-only via `tailscale serve` at http://nuc:9090 (see multiplayer.md) |
| Web client: canvas render, vector actor models (shaded per-archetype bodies, motion-derived facing), drag-drop inventory, delta decoding + tick-timeline interpolation, VFX/damage numbers/audio stingers, PoE2 HUD + gem bar (shared gem-icon SVGs), centered pick-3 draft dialog (auto-opens for gemless characters), WASD + click, minimap, join screen, party panel | `web/` | working, no build step |
| AI: behavior registry (`melee_chaser`, `ranged_kiter`, `boss_brute`, `boss_king`); territorial aggro: LoS/hearing, leash to `Actor.Home`, return-home | `sim/ai` | real, tested |
| Spawn queue (RISKS #2): `QueueSpawn`/`DrainSpawns` at a fixed phase — deterministic IDs, birth-tick immunity, save-refused when pending; on-death adds (`ActorDef.DeathSpawn*`, the Carrion Husk splits into ghouls) | `sim/core/world.go`, `sim/sim.go`, `content/` | done, tested, verified live |
| Minions: `Actor.Owner` (zone-local, saved+hashed), kill attribution up the chain (`World.CreditFor` — XP/flasks/orbs pay the summoner), `minion_melee` heel AI (mobile leash on the owner), `SkillSummon` w/ cap-despawns-oldest; Summon Skeleton cuttable gem (cap 3, minions at gem level); save v12 | `sim/core`, `sim/ai`, `sim/skills`, `content/`, `web/` | done, tested, verified live |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome (v18), JSON snapshots, binary delta codec | `protocol/` | done, tested |
| Content tables | `content/` | 29 skills (14 cuttable incl. 2 auras + 2 curses + channel + blink, 5 staged), 20 supports (incl. crit pair on the new pipeline fold), 14 actors (4 bosses), 36 affixes (ILvl-tiered), 9 bases, 12 uniques, 8 drop tables, 4 monster mods + 7 floor-mod packages, 8 buffs (4 curses) |
| Debug client + determinism/golden replay tests | `cmd/headless`, `sim/sim_test.go` | done |

## Invariants the code currently honors (don't break casually)

- No floats, no wall-clock, no unseeded randomness inside `sim/`. Randomness
  enters the stack exactly once: `Config.Seed == 0` rolls from OS entropy at
  the host edge (logged); below that, everything is deterministic in the seed.
- Sim logic never iterates a Go map (random order). Maps are lookup-only.
- All RNG flows through the world's four named streams (combat/loot/ai/map);
  consumption order is part of replay compatibility. Conditional consumption
  (shock rolls only on lightning; chill and buffs consume none) is pinned by
  `TestAilmentRNGConsumption`-style tests — every new proc needs one.
- Data types live in `sim/core`; leaf packages hold logic and import core;
  only root `sim` knows the phase order. `protocol/` imports nothing.
- Per-damage-type stat queries REPLACE the damage-type tag in the context
  (`damageTypeTags` in `sim/combat/pipeline.go`) — added-fire must never
  leak into other types' rolls (session 1's bug).
- Sheet-mod source spaces are disjoint: item IDs, ailments (high bit),
  buffs (top two bits + FNV of buff ID; DB() panics on collision), per-level
  growth (bit 62), passives (bits 63+60), monster mods (bits 63+61).
- Saves are durable state: any new world state ships save support (+
  `SaveVersion` bump on shape changes) AND `World.Hash` coverage in the same
  commit. Save only at tick boundaries; `World.Save` refuses pending state.
- Golden replays: behavior changes fail `TestGoldenReplay` and/or
  `TestGoldenDungeon`. If intentional, re-record both
  (`DRAUPFORGE_UPDATE_GOLDEN=1 go test ./sim/`) and commit the testdata.
- Terrain (`World.Grid`) is immutable once set, installed before any spawn.
  Nil grid = the v1 open plane, bit-exact with pre-terrain behavior. One
  shared clearance radius; paths compute at command time and are followed
  blind. Mapgen consumes only RNGMap.
- `protocol/binary.go` and `web/net.js` are a hand-maintained codec pair.
  Any wire change updates both AND bumps `protocol.Version` — stale clients
  fail loudly at the welcome.
- A tick-0 view is never stored as a delta baseline: `baseTick 0` is the
  keyframe sentinel, ack 0 the client reset, and a freshly swapped world
  really is at tick 0.
- Any welcome fully resets the client and the server's per-client encoder;
  welcome `gen` increments per re-welcome and acks must echo it. On a swap
  the actor-ID/gen/pending-queue cutover is one mutex section.
- Characters transfer only durables (def/level/XP/pools/bag/gear/gems);
  position, action, buffs, DoTs, `Home` die with the zone. Life ≤ 0 at
  injection = "arrive refilled". Injection never grants: a character
  arrives with exactly what it recorded (`StartingUncut` fires only in
  `sim.Spawn`, consuming three loot draws per gem at spawn time).
- Support modifiers never touch the actor's sheet — they fold into the
  socketed skill's queries (`GemCtx`); cast contexts bake at the command
  gate. The uncut draft is rolled at drop time (three loot draws); cutting
  consumes no RNG. `content.Supports`/`Cuttable` are ordered tables —
  reordering is replay-relevant, same rule as affixes.
- Lock ordering above the sim: `lobby.mu` → `instance.mu` → (never both
  with) `client.mu`, and `lobby.mu` → `IdentityStore.mu`. The lobby never
  touches a world. Never call the identity store holding an instance mutex.
- The token, never the name, is the auth. One session per account,
  whichever character it plays; guests get no cookie and no store entry.

Structural risks live in `RISKS.md` — read it before building anything
load-bearing (top entry: the action model is one-thing-at-a-time).

## Known shortcuts (deliberate, fine for now)

- The combat mechanics are complete: life leech (58), block (59), ES
  recharge (60), stun (61: a hit ≥15% of max life interrupts the action,
  0.3s lock + 0.5s re-stun immunity, bosses immune). Chill doesn't slow an
  action
  already in flight (tick counts bind at use time); movement slows now.
- Corpses compact away at tick end — fine until on-corpse mechanics matter.
- Inventory is a flat ID-addressed bag, no stacking; bag arrangement is
  client-side presentation state and dies with the page.
- Client hand-mirrors: item-icon SVGs + `BASE_SLOTS` by base id, `SKILL_META`
  per cuttable skill — new content updates them or eats the fallback.
- Named characters persist (identity store); guests are ephemeral by design.
  The admin port has no auth (public deploy disables it).
- `-load` under the lobby seeds only the *first* instance created (whoever
  connects first resumes the run); a lobby has no way to aim a save at a
  particular player. Fine for the single-operator rollback story it serves.
- No character rename; deletes (hideout button, select-screen ×, both via
  `/api/forget`) free names on purpose — deleting the last character takes
  the account and its stash with it. `identities.json` is one plaintext
  blob, tokens included.
- No client prediction — input feels its latency. Prediction is what would
  justify compiling sim/ to wasm (DESIGN §13's optional layer).
- Static files come from -web at runtime.
- Collision is soft separation between monsters and player-owned minions
  (actual players never push or get pushed); pairwise O(n²). Aggro is LoS + hearing with no memory; AI
  re-issues its chase target every tick (repath throttle keeps it cheap);
  kiter retreat picks from 5 fixed directions — a cornered archer fights.
- Terrain travels as JSON rows in the welcome (~2KB at 48×48); fog of war
  is client-side reveal tracking over it — the server still sends the whole
  map, so a determined cheater can read the layout (not the monsters: those
  are interest-culled server-side at 60u).
- The run is per-instance, single-player-first: any death ejects everyone,
  travel moves the whole instance (parties live and die together); one eject
  consumes one portal use however many died that tick.
- Uncut gems drop at the dier's level, so `level_gem` only matters once
  floors outpace your gems — deliberate: depth is how gems grow.
- Cast-on-death portal still deliberately unshipped — it must carry an
  opportunity cost (now expressible: a gem socket), never free.
- Numbers all open for tuning (Jake): death costs 1/5 of the level's XP
  requirement; packs +1 monster and +1 level per floor; rarity chances
  (magic 10% +2%/floor cap 30%, rare 2% +1%/floor cap 12%), XP ×3/×6, drop
  attempts 2/3 by rarity; portal grace 2.5s; flasks 60 cap / 30 sip / 10 per
  kill; gem permilles per table (boss 450/300‰, ×2/×3 by rarity), jeweller
  40‰, gem +10% dmg and +5% mana per level, support numbers in
  `content/supports.go`; projectile ranges TTL-derived (fireball ~14u,
  arrows ~16–17u, spark 1.5s bouncing, splash 2u, arc 12u reach / 2 chains);
  hideout is 16×12; bleed 35% of the phys hit as dps for 6s (ghoul claws
  25% chance, bleed_chance affix 5–10%, Rupture support 35% + 10% more
  phys); poison 30% of phys+chaos as dps for 2s, stacking (putrid slam
  30%, poison_chance affix 5–10%, Envenom support 40% + 4 flat chaos);
  auras reserve 35% each (multiplicative), Anger +5 flat fire / Determination
  50% inc armour, aura effect +5% per gem level; curses 8s (flammability
  -25% fire res, enfeeble 20% less dealt, weakness +20% taken), player
  curse AoE 3u / range 10u, hexer hex 2.5u / 9u on a slow 1.5s cycle;
  incinerate 5 gouts/s at 2 mana each (4 up front), 4–7 fire ~5.6u reach;
  blink 7u range, 3s cooldown, 8 mana; melt pays 1/3/8/20 by rarity (gems
  5), orb prices in shards: transmute 4, scour 6, jeweller 15, regal 20,
  alch/chaos 25, annul 30, exalt 80; new orb drop permilles
  regal 12 / exalt 4 / annul 10 / scour 25.

## Feature plan

The descent shipped (session 15); the character store + sessions shipped
(37–38 — DESIGN §14 is fully real); the telegraphed multi-stage boss shipped
(45 — staged skills, DESIGN §15); `-load` works under the lobby again (46);
per-client send queues shipped (47); the stash shipped (48); origin checks
and rate limiting shipped (49); uniques shipped (50) — roadmap one's phases
are all ✅ now; the spawn queue paid down RISKS #2 (51), minions landed on
it (52), and the replay log closed the hardening list (54). The next
chapter is **ROADMAP.md v2** (rewritten session 72): two concurrently
developable tracks — Track 1 "Buildcraft" (sim/content build depth:
bleed/poison, auras, curses, channelling + movement, crafting + the Forge,
passive growth) and Track 2 "The Living Descent" (server/web world +
account meta: character roster + shared stash, biomes, depth-scaled floor
mods with delve-chart route choice, account-wide checkpoints,
build-visible ladders + death recap, hardcore/SSF modes, feats + hideout
trophies, chat, shell polish) — sliced along the sim-behavior vs.
wire/shell seam. Its seam contract governs parallel work; read it before
starting either track. **Both tracks shipped**: Track 1 merged to main
(PR #62); Track 2 shipped whole (items 1–9, one commit each on
`track2/account-roster`, PR #63, rebased over Track 1 — its wire bumps
renumbered to v26/v27 past Track 1's v25). Jake's balance pass over the
numbers stays open — the new floor-mod numbers (RarityPm/PacksPct in
`server/floormod.go`), the deep-start portal trade (flat −1), and the
chat bucket join the list.

## Session log

- **2026-07-07 (82)** — The delve chart (`delve-chart`): descent becomes a
  PoE1-Delve-style node map, per Jake's spec (visual up/down/sideways
  navigation, biomes as clumps, 3-floor nodes, boss gates the next pick).
  `server/delvemap.go`: a lattice pure in the run seed — 3–5 nodes per
  row (7 cols, row 1 forces the entry), every row guaranteed reachable
  and every node given a way down, ~65% lateral corridors; biomes via
  depth-weighted jittered blob centers + Voronoi (rows 1–2 pinned crypt);
  node mods (all 3 floors share them) on the old floorModTable at the old
  depth bands, +1 on a 20% "juiced" roll. Instance state: (node, fin) +
  visited/cleared sets replace (floor, route, chamber); `floor` stays as
  the derived global depth ((row−1)·3+fin), so ladder/checkpoints/feats/
  ilvl scaling are untouched; run-save envelope v3 carries it all
  (visited/cleared serialized in lattice order — no map iteration). Every
  node's fin-3 stakes a set-piece (guardian / King‑Warden every 3rd row /
  Tyrant every 10th); its death event clears the node, marks checkpoints
  (Warden now included — it never checkpointed before), and broadcasts
  the grown chart. Stairs: fins 1–2 instant, fin 3 answers a travel-mode
  "delve" frame; travel picks (`travel` verb, Row/Col on Command) are
  validated at the stairs against visited ∪ cleared-frontier. Deep starts
  map stored global-floor checkpoints onto the fresh chart's trunk row.
  Wire v28: DelveSnap fog (visited + neighbors full, ring-2 veiled
  silhouettes), RunSnap row/col/fin/cleared. Client: SVG chart panel
  (N / Esc-menu; biome-colored nodes, jittered lattice, cleared ✓, boss
  glyphs, reward pips, pulsing frontier, hover info, click travels),
  chart dialog kept for portal deep starts, HUD shows fin/3. Old chart
  machinery (chartOffers/modCountAt/biome bands) deleted. Sim untouched,
  goldens byte-stable; suite green. Verified live in Chrome: guest run —
  portal → node 1:3, fins walked, guardian burned down (admin god+gem),
  `cleared` on the run snap, travel-mode map with 3 pulsing exits, travel
  to 2:4 (floor 4, Iron-boned, reveal grown), death/eject/run-over path
  exercised, zero console errors. Balance knobs now in play: row bands
  for mod counts, 20% juice, 45%/65% edge odds, biome weights.
- **2026-07-07 (81)** — UI shell pass (`ui-polish`): the escape menu —
  Esc opens a centered menu of every panel (inventory, character sheet,
  party, ladder, settings, exit-to-character-select) plus the full
  controls reference; the Esc cascade closes dialogs first, then any
  open panels, then toggles the menu. Panel slots (right: inventory /
  character sheet, center: ladder / settings / menu, left: social):
  opening a pane evicts its slot-mate, so panels never stack on each
  other (the sheet used to render inside the inventory frame). Tab
  joins I as the inventory toggle. The ever-growing bottom help line —
  by now underlining the entire HUD — shrank to "press Esc for menu &
  controls". Join screen: the `#join input` width rule was inflating
  the hardcore/SSF checkboxes into invisible 240px boxes (now inline),
  backdrop near-opaque so the HUD doesn't bleed through. The 8-orb
  currency strip wraps instead of clipping at the panel edge. Pure
  web/ presentation — no wire, no sim, suite untouched.
- **2026-07-07 (80)** — The content pass, and **Track 1 is complete**.
  Crit joins the support fold (rollCrit composes CritChance/CritMulti
  through GemCtx like the damage queries — one draw always, so streams
  never shift; behavior-identical for pre-existing content). Five
  supports → 20: Brutality (30% more phys), Elemental Focus (20% more per
  element, three type-tagged Mores), Increased Critical Strikes, Deadly
  Precision (+40% multi), Toxic Coating (poison chance + more chaos).
  Seven uniques → 12, one celebrating each track mechanic: The Hungering
  Edge (bleed+leech sword, slower), Pyre Tongue (the incinerate stick,
  weaker phys), Emberheart (fire vest, cold-cursed), Vilethorn Fists
  (poison gloves, less life), Crown of the Hexbinder (curse-caster hat),
  Gravecaller's Girdle (+1 minions belt, slower), Band of the Patient
  Hunter (crit ring, slower everything). And the set-piece: the Ashen
  Warden (boss_king AI, stun-immune, 50% fire res) — hexes your fire res
  from range (hex_of_embers), commits to a telegraphed two-stage pyre
  slam up close, and channels flame gouts when enraged until his mana
  pool runs dry; boss floors now alternate King (5, 25…) / Warden
  (15, 35…) with the floor-15 descent test updated. Goldens byte-stable
  through all of it. Verified live: the warden hexed a TCP client
  through his real AI (ail:64, curse events cycling).
- **2026-07-06 (79)** — The passive ladder reaches the cap: Track 1 item
  6, pure content on the existing milestone machinery. Eight new rungs
  (15–50) × 3 forks = 24 passives in themed tiers: procs at 15 (bleed/
  poison/shock chance), walls at 20, tempo at 25, the SHAPE tier at 30
  (+1 minion cap / +1 projectile / +1 chain — the stats uniques used to
  own, now a build-defining level choice), sustain at 35, elements at 40,
  gambles at 45 (glass cannon takes +10% damage taken; iron will gives
  -8%), and More-multiplier capstones at 50 (attack/spell/life). DB()
  now asserts every fifth level to 50 has a fork. The chooser needed
  nothing — the passive table rides the welcome. Goldens untouched (no
  RNG shape changed). Pinned by a ladder-walk test: a level-50 character
  takes all 10 milestones, spent milestones refuse seconds.
- **2026-07-06 (78)** — The Forge + the crafting ladder's missing half:
  Track 1 item 5. Four orbs join the wallet (OrbKind 4→8; the wallet
  array widens, old saves shorter-copy fine): regal (magic→rare KEEPING
  its affixes + one more — the new `addAffixes` seeds group/kind state
  from what's rolled), exalt (add to a rare below the 6-affix cap),
  annulment (remove one at random — a loot draw), scouring (wipe to
  normal, no RNG); uniques now refuse orbs explicitly. The Forge:
  `forge_melt` melts a bag item into shards (by rarity; uncut gems flat
  5), `forge_buy` exchanges shards for orbs at fixed prices — sim
  commands, deliberately NOT hideout-gated: the Forge travels with you,
  melting trash mid-run to free bag space is the "no drop is worthless"
  payoff where the decision happens. `Actor.Shards` is a plain-int
  durable wallet (save v19, conditional hash, transfers). Wire v25:
  ActorSnap.Shards rides the orb field group. Client: 8-orb strip with
  shift-click-to-buy, an armable forge hammer (click bag items to melt —
  stays armed for batch melting, no confirm modal: arming IS the
  two-step), EvForge log lines. New orb drop bands appended to the
  single-draw table — goldens happened to stay byte-stable (no golden
  kill rolled the new window). Verified live over the wire: melted the
  starting uncut gem (5 shards), bought a transmutation (1 left),
  8-slot wallet + both forge events on the snapshot.
- (sessions 38–77 pruned — git history is the archive)
