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

**Last updated: 2026-07-06** (session 71: character deletion — a hideout
reset button + POST /api/forget frees the name for a fresh claim)

## Where things stand

The game is public and multiplayer: https://nuc.tail4b8d48.ts.net (Tailscale
Funnel → the nuc; CI redeploys on every merge to main). `cmd/server` runs a
Lobby of instances. On connect you claim a name or play as guest: a claim
mints a secret token in an HttpOnly cookie, and the token (never the name)
resumes your character — banked to the `-identities` JSON on disconnect and
every 30s. Guests are ephemeral. Every connection gets its own world and
run; party = instance — the F panel lists all online named players, an
accepted invite transfers you into the inviter's world via the floor-swap
machinery, and empty instances reap after 60s, doubling as reconnect grace.

The game is a game now: you descend — from home. Every run starts in the
hideout (floor 0, a small safe world derived from the instance seed); its
portal leads down to floor 1, anchoring there on first use. Each boot rolls a fresh world seed (logged — `-seed` pins it to
reproduce a session), and every instance derives its dungeons from that one
number. Floors are whole fresh Worlds seeded from (run seed, floor index);
stairs at the far end take everyone a level deeper; packs grow and level
with depth, a rare guardian holds the stairs every 3rd floor. Death costs XP
(never de-levels) and ejects everyone to the portal, burning one portal use
(`-portals`, default 3) — none left and the run is over: a new run starts
back home on a fresh seed, best-floor kept as the score, the character
(level/XP/gear/gems) surviving it all. The portal re-plants wherever you
stand (P); a trip home costs one use, returning is free. The HUD shows run ·
floor · portals · best.

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
| Statuses: ignite/chill/shock ailments + content buffs (refresh-not-stack, pending-buff queue) | `sim/combat` | done, tested |
| Persistence: `World.Save`/`LoadWorld` (versioned JSON, bit-exact), admin `POST /api/save`, descent run envelope (v2); `-load` under the lobby seeds the first instance (validated at boot) | `sim/core/save.go`, `server/runsave.go`, `server/lobby.go` | done, tested, verified e2e |
| Actions (windup/recovery) + projectiles; skill feel: splash w/ falloff, wall bounce, heading wiggle, hitscan chains | `sim/skills` | done, tested |
| Staged skills (DESIGN §15): stage sequences w/ per-stage locked aims, blast/ring effects, telegraphs on the wire (v19); telegraphed blasts skip evasion; the Barrow King (boss_king AI, floors %5, rare, stateless enrage <50%) + boss bar + ground-telegraph rendering | `sim/skills`, `sim/ai`, `content/`, `server/descent.go`, `web/` | done, tested, verified live |
| Loot: rarity weights, weighted affixes, group caps, per-slot pools, rolled implicits; item level (= dier's level, floor-scaled) gates affix tiers (ILvl-gated greater/grand tiers) — deeper drops roll strictly better; base tiers always fill (DB() asserts ILvl-0 depth); save v13, wire v21, ilvl on the tooltip | `sim/items`, `content/`, `web/` | done, tested, verified live |
| Uniques: fixed-identity chase items (4 in content) with shape stats nothing else rolls (ExtraProjectiles/ExtraChains — the skill system reads them off the sheet); UniquePermille per table, orange styling + authored mod lines on the wire (v20); orbs refuse them; save v11 | `sim/items`, `sim/stats`, `content/`, `web/` | done, tested, verified live |
| Currency: orb wallet (transmute/alch/chaos/jeweller) banked straight to the killer; `apply_orb` crafts bag items | `sim/items`, `web/` | done, tested |
| Gems: cast only from cut gems; a fresh character spawns with `StartingUncut` draft-of-3 gems instead of a fixed starter; uncut drops carry a draft of 3 at the dier's level (cap 20); supports fold into the socketed skill's queries only (more/less, added flat, speed, mana, fans, chain, conversion); cast contexts bake at use; save v9 | `sim/core/gems.go`, `sim/items/gems.go`, `content/supports.go`, `web/` | done, tested, verified live |
| Progression: leveled XP on kill, quadratic curve, cap 50, PerLevel mods, ding heal | `sim/progress` | done, tested |
| Flasks: charge-gated regen-burst sips (keys 1/2), kills feed charges, durable (save v7) | `sim/`, `content/`, `web/` | done, tested |
| Passive forks: milestone choices at 5/10, permanent, durable (save v6), client chooser | `sim/core`, `content/`, `web/` | done, tested |
| Character extract/inject: durables only, item IDs re-minted, sheet rebuilt | `sim/core/character.go` | done, tested |
| The descent: hideout start (`Config.StartFloor`, 0 = home; hideout world derived from the instance seed), floor swaps, portal economy, XP death penalty, run-over → new run at home, leveled+thickened packs, stairs guardian every 3rd floor | `server/descent.go` | done, unit + e2e tested, verified live |
| Monster rarity: magic/rare rolls with mod packages, XP ×3/×6, extra drops, floor-scaled chances, rings + nameplates | `sim/sim.go`, `content/`, `web/` | done, tested |
| Equipment + inventory: 10 slots, slot-addressed equip, pickup/unequip/drop, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, send-rate decoupling, interest culling, binary deltas + acks, pause, per-client send queues (a stalled socket dies alone; the tick never blocks on I/O) | `server/` | done, race-tested |
| Replay log: `-replaydir` records every world as a segment (World.Save header + NDJSON command lines); host surgery (joins/swaps/grace/admin/stash) rotates segments so each spans a pure Step stretch; `cmd/headless -replay` re-executes bit-exact | `server/replay.go`, `cmd/headless` | done, tested, verified e2e |
| Identity: name claim mints a 32-byte cookie token; the token resumes the character (banked on disconnect + 30s flush); one session per name; guests skip it all | `server/identity.go` | done, tested |
| Stash: per-identity hideout bank (60 items, durable CharItem form on the identity); stash_put/stash_take verbs, hideout-only, processed at the host layer between ticks; drag between bag and stash in the panel | `server/stash.go`, `server/identity.go`, `web/` | done, tested, verified live |
| Lobby: many instances per process, party = instance, invite/leave transfers via floor-swap machinery, 60s empty reap = reconnect grace | `server/lobby.go`, `cmd/partybot` | done, race-tested, verified live |
| Hosting + CI/CD: public via Tailscale Funnel; every push to main builds, swaps (prev kept), restarts, health-checks; `identities.json` never touched. CI gates every PR and main push: `go vet`, race-tested suite, JS syntax | `.github/workflows/deploy.yml`, `ci.yml` | done, verified e2e |
| Admin dashboard: observe (tick health, counts, events, hash, run line) + poke (pause, spawn, kick, save) + dev cheats (god/gem/orbs); lobby index at `/i/{id}/` | `server/admin.go` | done, tested; NO AUTH — on the nuc it binds loopback, tailnet-only via `tailscale serve` at http://nuc:9090 (see multiplayer.md) |
| Web client: canvas render, vector actor models (shaded per-archetype bodies, motion-derived facing), drag-drop inventory, delta decoding + tick-timeline interpolation, VFX/damage numbers/audio stingers, PoE2 HUD + gem bar (shared gem-icon SVGs), centered pick-3 draft dialog (auto-opens for gemless characters), WASD + click, minimap, join screen, party panel | `web/` | working, no build step |
| AI: behavior registry (`melee_chaser`, `ranged_kiter`, `boss_brute`, `boss_king`); territorial aggro: LoS/hearing, leash to `Actor.Home`, return-home | `sim/ai` | real, tested |
| Spawn queue (RISKS #2): `QueueSpawn`/`DrainSpawns` at a fixed phase — deterministic IDs, birth-tick immunity, save-refused when pending; on-death adds (`ActorDef.DeathSpawn*`, the Carrion Husk splits into ghouls) | `sim/core/world.go`, `sim/sim.go`, `content/` | done, tested, verified live |
| Minions: `Actor.Owner` (zone-local, saved+hashed), kill attribution up the chain (`World.CreditFor` — XP/flasks/orbs pay the summoner), `minion_melee` heel AI (mobile leash on the owner), `SkillSummon` w/ cap-despawns-oldest; Summon Skeleton cuttable gem (cap 3, minions at gem level); save v12 | `sim/core`, `sim/ai`, `sim/skills`, `content/`, `web/` | done, tested, verified live |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome (v18), JSON snapshots, binary delta codec | `protocol/` | done, tested |
| Content tables | `content/` | 18 skills (8 cuttable, 4 staged), 13 supports, 12 actors (3 bosses), 34 affixes (ILvl-tiered), 9 bases, 5 uniques, 8 drop tables, 4 monster mods, 4 buffs |
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
- The token, never the name, is the auth. One session per name; guests get
  no cookie and no store entry.

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
- Claiming a new name under an existing cookie orphans the old identity
  (no rename/list — but the hideout's delete button + `/api/forget` free a
  name on purpose). `identities.json` is one plaintext blob, tokens
  included.
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
  hideout is 16×12.

## Feature plan

The descent shipped (session 15); the character store + sessions shipped
(37–38 — DESIGN §14 is fully real); the telegraphed multi-stage boss shipped
(45 — staged skills, DESIGN §15); `-load` works under the lobby again (46);
per-client send queues shipped (47); the stash shipped (48); origin checks
and rate limiting shipped (49); uniques shipped (50) — ROADMAP's phases
are all ✅ now; the spawn queue paid down RISKS #2 (51), minions landed on
it (52), and the replay log closed the hardening list (54). The queue is
pure content and tuning now: more skills/uniques/bosses/floors as appetite
dictates, and Jake's balance pass over the numbers.

## Session log

- **2026-07-06 (71)** — Character deletion: the reset lever a bricked
  character needed (its name was squatted forever). `IdentityStore.Delete`
  removes the identity outright — character, stash, name reservation —
  and `POST /api/forget` (cookie-authed) drives it: store entry first,
  THEN the live session is kicked (lobby `online` index / instance client
  scan), so the dying session's Disconnect/Bank no-op on the gone token
  instead of resurrecting it; the cookie expires in the response. Client:
  a "delete this character" button in the hideout panel (named + floor 0,
  same gating as the stash) behind a real confirmation overlay, then a
  clean reload to the join screen with the name claimable again. Pinned
  at three layers: pure store (resurrection resistance), instance wire,
  and lobby wire (kick + fresh re-claim).
- **2026-07-06 (70)** — The balance patch, two fronts from playtesting.
  (1) Anti-shotgun: one cast damages each target at most once. A real fan
  (n > 1) shares a `Projectile.Volley` id; a target the volley already
  damaged is flown past at collision time (extra projectiles reach what's
  behind) and any sibling hit — direct or splash — drops at the top of
  the pipeline before any roll, reading only hashed state so RNG
  consumption stays deterministic. The volley is spent only when damage
  lands (evaded/blocked siblings don't count); memory is a 4-slot ring on
  the actor (`RecentVolleys`, saved v16 + conditionally hashed). Pinned:
  a GMP fan hits once, a second cast hits again, solo casts carry no
  volley. (2) Defensive gear trim ~25%: life/armour/evasion/ES flats,
  all resist tiers, regen, leech (2–5%→1–3%), block suffix and the
  shield's block implicit all shaved — "invincible-adjacent after
  non-normal gear" was the note. Offense untouched. Goldens re-recorded.
- **2026-07-06 (69)** — Two new minion skills on the session-52 machinery.
  Summon Marksman: the first ranged minion (cap 2) — `minion_ranged` AI
  shoots bone arrows from PreferredRange with rangedKiter's firing logic
  under minionMelee's owner-anchored leash (shared `minionTarget`/
  `liveOwner` helpers). Summon Raging Spirit: the first short-lived minion
  (cap 5, 8s) — new `Actor.LifespanTicks` counts down in Upkeep and expiry
  is a quiet despawn (no death/loot/XP, like a cap despawn); plumbed
  through `PendingSpawn.Lifespan` from `SkillDef.SummonTTL`. Save v15,
  conditional hash. Cuttable pool grew → goldens re-recorded. Pinned:
  spirit expires quietly with a real 8s step loop, marksman fires from
  range instead of charging. Client: bow-armed skeleton + flaming-skull
  models, gem metadata.
- **2026-07-06 (68)** — Light radius. A new `light_radius` suffix
  (+0.5–1.5u in 0.5 steps, helmet/amulet/ring) reaches further into the
  fog of war, and the base lit circle grew 9→10 units. The trick: fog is
  pure client presentation, so the client sums equipped rolls off the
  equipment snaps it already receives (`updateLightRadius` per view) —
  no sim query, no wire change; `stats.LightRadius` exists only because
  the affix machinery mods a sheet. Appended last in the ordered affix
  table; goldens didn't move.
- **2026-07-06 (67)** — The character sheet (C): finally a way to see your
  stats. Server-computed — `sim.BuildSheet` evaluates stat lines off the
  live sheet (pools, defenses, resists, crit, regen, leech) plus per-gem
  combat numbers: nominal average hit via the new `combat.NominalHit`
  (mirrors rollDamage step for step with roll averages instead of RNG —
  pure, pinned read-only by `TestBuildSheetReadOnly`), cast time at
  current speed, DPS, mana cost, fan/chain shape after supports and gear.
  Flow is the stash pattern: a host-layer "sheet" verb, answered after the
  tick with a "sheet" JSON frame (wire v22, binary codec untouched); the
  C panel re-requests on an 800ms pulse while open, two tabs (Character /
  Gems). Goldens unmoved.
- **2026-07-06 (66)** — Loot ergonomics. Sim: multiple drops from one death
  scatter onto a ~1.1u eight-point ring around the corpse (widening per
  lap, clamped to walkable like queued spawns, first drop on the corpse) —
  pure data, zero RNG draws, so the loot stream is untouched; pinned by
  `TestDropsScatter`, goldens re-recorded (drop positions moved). Client:
  ground labels lay out as a climbing column instead of overprinting
  (bottom-most keeps its spot), each laid-out label is itself a click
  target for its exact item, and bare-ground clicks resolve to the nearest
  drop instead of first-found — together the fix for "the boss died on the
  stairs and clicking the pile descends".
- **2026-07-06 (65)** — Entity separation widened: the de-overlap pass now
  includes player-owned minions (anything with `Owner != 0`) alongside
  monsters, so a skeleton army reads as an army instead of a single-file
  clump; actual players still never push or get pushed. `overlapFraction`
  800→900 milli — packs stand a shade wider. First direct unit tests for
  `Separate` (monsters ease apart, minions ease apart, players never move);
  goldens unmoved (their scenarios never converge below the threshold).
- **2026-07-06 (64)** — Item presentation. Three fronts: (1) affix/implicit
  rolls quantize in the engine — `AffixDef.Step`/`ImplicitDef.Step` snap
  every roll onto a Min + k·Step lattice (whole percents for resists and
  increased-stats, whole points for flat life/armour, 0.1 steps for the
  regen/move-speed oddballs), uniform across steps and still exactly one
  RNG draw so the loot stream never shifts; pinned by
  `TestRollValuesLandOnStep`, goldens re-recorded (values moved). (2) The
  client formats mod lines semantically — `modLine`/`MOD_FMT` turn
  "fire_resistance: 0.17" into "+17% to fire resistance" for every content
  affix and implicit, with a magnitude-guessing fallback for ids newer than
  the client. (3) Rarity honesty: normal ground labels drop the gold tint
  (grey-white now — gold was reading as rare), rare/unique/gem labels go
  bold, and uniques finally throw the tallest light shaft (they had none).
  Boots icon redrawn as an actual boot. Verified live: dummy farm, tooltips
  checked across normal/rare/unique + a synthetic all-affix rare, zero
  console errors.
- **2026-07-03 (63)** — Audio juice: the three mechanic events that had
  visuals but no sound (block, stun, spawn) get SFX in `sfxForEvent`, and
  getting stunned yourself now rattles the screen at 1.5× the hit-shake —
  the loss of control should feel like a jolt. Pure client, no wire or sim
  changes. Verified live in Chrome with an sfx spy: mages stunning the
  player (sound + shake), a fireball stunning a carrion husk, the husk's
  death-burst ghouls tripping the spawn sting; zero console errors.
- **2026-07-03 (62)** — Support-gem pass: three new supports, each opening
  a build direction, all on the existing GemCtx fold (zero engine risk).
  Ruthless (40% more melee damage, melee-gated) is the payoff Sweep and the
  melee attacks wanted; Immolate (adds 8 fire + 25% more fire) is the fire/
  ignite specialist, type-tagged so it only lifts the fire portion;
  Cannonade (+1 projectile, 15% MORE damage, steep mana) is the aggressive
  fan that doesn't pay LMP/GMP's damage penalty. Appended to the support
  table so draft indices don't shift — goldens untouched. Pinned: the fold
  math (Immolate exceeds a plain ×1.25 from its flat add, Cannonade is
  exactly ×1.15 and fires two projectiles) and the melee gate (Ruthless
  refused on a projectile skill, accepted on a melee one).
- **2026-07-03 (61)** — Stun: the capstone combat mechanic, and the
  deliberate action-model interrupt RISKS #1 anticipated. A hit dealing
  ≥15% of the target's max life clears its current action and locks it out
  for 0.3s, followed by a 0.5s re-stun immunity tail — both folded into one
  `Actor.StunTicks` countdown (Stunned = ticks past the immunity window),
  decremented in Upkeep so the command validator drops a stunned actor's
  commands the same tick. Deterministic threshold, no RNG. `ActorDef.
  StunImmune` on all three bosses keeps a lucky crit from cancelling a
  telegraphed set-piece — their huge life pools would resist most stuns
  anyway, but the flag makes it certain. Save v14 (StunTicks + last
  session's Recharge), conditionally hashed; goldens re-recorded (stun
  now lands on dungeon monsters). Client: circling stun-stars over the
  reeling target + a log line. Verified in the real sim: fireballs stun
  zombies, the Grave Tyrant shrugs them off.
- **2026-07-03 (60)** — Energy shield recharge — the defensive trio (leech,
  block, recharge) is complete, and ES gear finally does more than sit
  there. ES refills in combat Upkeep at 20% of max per second, but only
  after 2 seconds without taking damage: any hit or DoT tick calls MarkHit
  and resets the delay. New zone-local actor state `RechargeDelay` (saved,
  conditionally hashed, never transferred — a fresh zone starts clear).
  No RNG, but the new hashed field shifted the goldens (re-recorded). ES
  already flows to the client as a pool, so it renders the climb for free.
  Pinned: the delay holds ES, then it recharges to max, and a hit resets
  it; verified in the real Step() loop with an ES chest equipped.
- **2026-07-03 (59)** — Block: the counterplay layer, and the shield's new
  reason to exist. A `Block` sheet stat — a defender's chance to negate an
  entire hit — rolled in the pipeline right after evasion, before any
  damage (`EvBlock`, 75% cap so it's never a certainty). Conditional
  consumption like the ailments (only defenders with Block > 0 draw),
  pinned by `TestBlockRNGConsumption` — old replays stay byte-stable.
  Content: the wooden shield's implicit is now 15–25% block (was flat
  armour — block IS the shield now), and a new offhand block suffix stacks
  5–12% more (ILvl 6). Client: a steel-blue parry-arc spark on the blocker
  and a "blocked" log line. Goldens re-recorded (the shield implicit
  changed). Verified with a probe: a 25%-block shield turned ~31% of real
  zombie swings through the full sim.
- (sessions 38–58 pruned — git history is the archive)
