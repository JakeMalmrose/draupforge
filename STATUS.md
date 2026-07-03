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

**Last updated: 2026-07-02** (session 42: pick-3 UI — fresh exiles pick
their first skill from an uncut draft, centered draft-card dialog, gem
icons, vector actor models)

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
| Damage pipeline (incl. live conversion stage) + DoTs + regen | `sim/combat` | done, tested |
| Statuses: ignite/chill/shock ailments + content buffs (refresh-not-stack, pending-buff queue) | `sim/combat` | done, tested |
| Persistence: `World.Save`/`LoadWorld` (versioned JSON, bit-exact), admin `POST /api/save`, descent run envelope (v2) — `-load` refused in lobby mode (see shortcuts) | `sim/core/save.go`, `server/runsave.go` | done, tested; `-load` regressed |
| Actions (windup/recovery) + projectiles; skill feel: splash w/ falloff, wall bounce, heading wiggle, hitscan chains | `sim/skills` | done, tested |
| Loot: rarity weights, weighted affixes, group caps, per-slot pools (depth asserted at DB()), rolled implicits | `sim/items` | done, tested |
| Currency: orb wallet (transmute/alch/chaos/jeweller) banked straight to the killer; `apply_orb` crafts bag items | `sim/items`, `web/` | done, tested |
| Gems: cast only from cut gems; a fresh character spawns with `StartingUncut` draft-of-3 gems instead of a fixed starter; uncut drops carry a draft of 3 at the dier's level (cap 20); supports fold into the socketed skill's queries only (more/less, added flat, speed, mana, fans, chain, conversion); cast contexts bake at use; save v9 | `sim/core/gems.go`, `sim/items/gems.go`, `content/supports.go`, `web/` | done, tested, verified live |
| Progression: leveled XP on kill, quadratic curve, cap 50, PerLevel mods, ding heal | `sim/progress` | done, tested |
| Flasks: charge-gated regen-burst sips (keys 1/2), kills feed charges, durable (save v7) | `sim/`, `content/`, `web/` | done, tested |
| Passive forks: milestone choices at 5/10, permanent, durable (save v6), client chooser | `sim/core`, `content/`, `web/` | done, tested |
| Character extract/inject: durables only, item IDs re-minted, sheet rebuilt | `sim/core/character.go` | done, tested |
| The descent: hideout start (`Config.StartFloor`, 0 = home; hideout world derived from the instance seed), floor swaps, portal economy, XP death penalty, run-over → new run at home, leveled+thickened packs, stairs guardian every 3rd floor | `server/descent.go` | done, unit + e2e tested, verified live |
| Monster rarity: magic/rare rolls with mod packages, XP ×3/×6, extra drops, floor-scaled chances, rings + nameplates | `sim/sim.go`, `content/`, `web/` | done, tested |
| Equipment + inventory: 10 slots, slot-addressed equip, pickup/unequip/drop, capacity | `sim/items/equip.go` | done, tested |
| Server: TCP + WS transports, send-rate decoupling, interest culling, binary deltas + acks, pause | `server/` | done, race-tested |
| Identity: name claim mints a 32-byte cookie token; the token resumes the character (banked on disconnect + 30s flush); one session per name; guests skip it all | `server/identity.go` | done, tested |
| Lobby: many instances per process, party = instance, invite/leave transfers via floor-swap machinery, 60s empty reap = reconnect grace | `server/lobby.go`, `cmd/partybot` | done, race-tested, verified live |
| Hosting + CI/CD: public via Tailscale Funnel (`-addr "" -admin ""`); every push to main builds, swaps (prev kept), restarts, health-checks; `identities.json` never touched | `.github/workflows/deploy.yml` | done, verified e2e |
| Admin dashboard: observe (tick health, counts, events, hash, run line) + poke (pause, spawn, kick, save) + dev cheats (god/gem/orbs); lobby index at `/i/{id}/` | `server/admin.go` | done, tested; NO AUTH — on the nuc it binds loopback, tailnet-only via `tailscale serve` at http://nuc:9090 (see multiplayer.md) |
| Web client: canvas render, vector actor models (shaded per-archetype bodies, motion-derived facing), drag-drop inventory, delta decoding + tick-timeline interpolation, VFX/damage numbers/audio stingers, PoE2 HUD + gem bar (shared gem-icon SVGs), centered pick-3 draft dialog (auto-opens for gemless characters), WASD + click, minimap, join screen, party panel | `web/` | working, no build step |
| AI: behavior registry (`melee_chaser`, `ranged_kiter`, `boss_brute`); territorial aggro: LoS/hearing, leash to `Actor.Home`, return-home | `sim/ai` | real, tested |
| Phase order + command validation | `sim/sim.go` | done — this IS the determinism contract |
| Wire types: versioned welcome (v18), JSON snapshots, binary delta codec | `protocol/` | done, tested |
| Content tables | `content/` | 11 skills (6 cuttable), 10 supports, 7 actors, 32 affixes, 9 bases, 6 drop tables, 4 monster mods, 4 buffs |
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

- Leech, block, stun, ES recharge: absent. Chill doesn't slow an action
  already in flight (tick counts bind at use time); movement slows now.
- Corpses compact away at tick end — fine until on-corpse mechanics matter.
- Inventory is a flat ID-addressed bag, no stacking; bag arrangement is
  client-side presentation state and dies with the page.
- Client hand-mirrors: item-icon SVGs + `BASE_SLOTS` by base id, `SKILL_META`
  per cuttable skill — new content updates them or eats the fallback.
- Named characters persist (identity store); guests are ephemeral by design.
  The admin port has no auth (public deploy disables it); a slow client can
  stall its instance's tick up to 1s (no per-client send queues) — real now
  that strangers can reach the public URL.
- `-load` is refused in lobby mode ("run saves predate parties"): `/api/save`
  still writes envelopes (v2) nothing can load. Regressed session 31's
  feature when the lobby shipped — on the list.
- Claiming a new name under an existing cookie orphans the old identity (no
  rename/list). `identities.json` is one plaintext blob, tokens included.
- No client prediction — input feels its latency. Prediction is what would
  justify compiling sim/ to wasm (DESIGN §13's optional layer).
- WS accepts any origin (the public Funnel deploy inherits it); static files
  come from -web at runtime. Live play is not replay-deterministic (network
  timing decides arrival ticks); a replay log would fix that — cheap when
  wanted.
- Collision is soft separation between monsters only (players never push or
  get pushed); pairwise O(n²). Aggro is LoS + hearing with no memory; AI
  re-issues its chase target every tick (repath throttle keeps it cheap);
  kiter retreat picks from 5 fixed directions — a cornered archer fights.
- Terrain travels as JSON rows in the welcome (~2KB at 48×48) — fine until
  fog of war. Affix tiers are shallow and pools don't scale with item level.
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
(37–38 — DESIGN §14 is fully real). The queue: a boss with telegraphed
multi-stage attacks at floor milestones (forces deliberate action-model
growth, RISKS.md #1 — design the state machine first); run saves under the
lobby (un-regress `-load`); then server hardening (replay log, per-client
send queues, rate limiting) — strangers *can* connect now. ROADMAP phase 4's
remainder (town hub, stash, uniques) is the fun-first counterweight.

## Session log

- **2026-07-02 (43)** — Admin portal on the tailnet. Ops: the nuc's
  dashboard binds `127.0.0.1:9090` (systemd drop-in) and `tailscale
  serve --http=9090` proxies it tailnet-only at http://nuc:9090 —
  serve, never funnel; plain HTTP on purpose (WireGuard already
  encrypts, and an HTTPS serve cert can't cover the short name — the
  "secure connection failed" Jake hit). Code: the lobby admin index
  grew up from a bare `<ul>` — styled landing page, sorted instance
  table with client/party columns, an empty-state line explaining the
  60s reap, 5s auto-refresh, gem favicon (+ 204 for /favicon.ico).
  `TestLobbyAdminIndex` pins it.
- **2026-07-02 (42)** — Pick-3 UI + first-skill choice (Jake: "the pick-3
  is whelming; start with a pick-3 instead of fireball"). Sim:
  `ActorDef.StartingUncut` — a fresh player spawns holding one level-1
  uncut skill gem (3 loot draws in `sim.Spawn`; injection never grants)
  instead of a cut Fireball; goldens re-recorded (spawn draws shift the
  loot stream), scenario tests grant their skills explicitly. Hideout
  seed now derives from the instance seed — a pinned hideout was dealing
  every new exile the identical draft (caught live in verification).
  Client: the cut dialog is a centered modal (scrim, Escape/outside-click
  closes) with three big draft cards — shared faceted gem-icon SVGs,
  kind tags, flavor text from `SKILL_META`, owned-gating — and it opens
  itself for a gemless character (once per welcome). Gem icons replaced
  the dots/gradients in the skill bar and gem panel. Actors are vector
  models now: shaded spheres with motion-derived facing, walk bob,
  shadows, and per-archetype accessories (zombie arms, ghoul claws,
  archer bow, mage hood+staff, colossus horns+ribs, player head+sword)
  — rarity rings, flashes, and telegraphs unchanged. Verified live in
  Chrome twice over (two different drafts, Bone Arrow and Arc runs, arc
  hits logged, death eject intact, zero console errors).
- **2026-07-02 (41)** — World runover. Runs start in the hideout: fresh
  instances and post-run-over restarts wake at floor 0 — the same home
  every session (`hideoutSeed`-pinned) — with the portal leading to
  floor 1 (anchor lands on the floor's spawn at first use;
  `Config.StartFloor` lets tests/dev start directly on a floor). World
  seeds roll from OS entropy when 0 (the new `-seed` default), logged
  for reproduction; the lobby rolls once, instances derive. Run envelope
  v2 (portal-placed flag). All tests pin seeds now. Verified over the
  TCP wire: two boots → different seeds and different floor-1 dungeons,
  `-seed 42` twice → identical ones, hideout identical across all,
  hideout→floor-1 hop free. Also: pruned this file back toward budget,
  killed the stale "conversion is identity" shortcut, fixed stale
  comments (admin.go header, cmd/server doc, orphan-reclaim).
- **2026-07-02 (40)** — Docs true-up. STATUS.md's body caught up with
  sessions 36–39 (which had only logged themselves); README/DESIGN §14/
  RISKS/ROADMAP/multiplayer.md/SHOWCASE aligned with main. No code.
- **2026-07-02 (39)** — CI/CD. Every push to main auto-deploys the nuc
  (self-hosted runner: build, swap with `playserver.prev` rollback, sync
  `web/`+`scripts/`, restart, health-check; `identities.json` untouched).
  Merging to main IS deploying — never deploy by hand.
- **2026-07-02 (38)** — Parties + friends. `server/lobby.go`: a Lobby of
  instances — party = instance, invites transfer via the floor-swap
  machinery, leave moves you out; F panel lists online named players;
  60s empty-instance reap doubles as reconnect grace. `cmd/partybot`
  auto-accepts invites. Gems (v16) + parties (v17) merged as wire v18.
- (older sessions pruned — git history is the archive)
