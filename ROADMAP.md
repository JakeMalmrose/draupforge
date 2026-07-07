# Roadmap v2 — two tracks to "can't put it down"

The first roadmap shipped whole: the descent, gems, passives, per-slot
affixes, currency, rarity packs, juice, uniques, the stash, and the public
multiplayer server — all ✅ by session 50, with the hardening list (send
queues, replay log, rate limits) closed by 54. Its text is in git history.
Same ethos here: this is **not** the risk register (`RISKS.md`) and it
deliberately ignores structural debt. One question only: *what gets a player
to "just one more run" fastest?*

**New this time: two tracks, developed concurrently.** The codebase has a
natural seam — sim behavior vs. world/meta shell — and the tracks are sliced
along it so two sessions in two worktrees can run in parallel without
stepping on each other. The seam contract at the bottom is what makes that
safe; read it before starting work on either track.

## Where the fun currently runs out

The loop works: kill → loot → build → descend → die → improve. What it lacks
is **variety and memory**. After a few runs you've seen all twelve monsters;
every floor is the same grey rooms-and-corridors; a "build" is one of eight
skills plus whichever supports dropped; and when a run ends, nothing outside
your own head marks that it happened. Two pulls are missing, one per track:

- **"I want to try THAT next"** — build variety deep enough that rerolling
  is the point (Track 1).
- **"I want to see what's down there — and I want the ladder to know I
  did"** — a world with identity, a score that outlives the run, and an
  account that makes the *next* character a temptation (Track 2).

Each numbered item below is a 1–3 session feature. Ship them thin and
end-to-end, in order — a feature is done when it's *fun in the browser*,
not when its sim test passes.

## Track 1 — Buildcraft (the character deepens)

Owns `sim/` behavior and `content/`. The PoE1-mechanics north star applies:
every mechanic below is a proven build language, imported thin.

1. **Bleed + poison.** Physical and chaos DoT ailments alongside
   ignite/chill/shock — two whole build archetypes in one mechanic. Bleed:
   phys-hit proc, one instance, refresh-not-stack (the ignite model).
   Poison is the deliberate design decision: PoE1 poison *stacks*, and
   stacking instances is new machinery — design it first, don't bolt it on.
   Chance-to-bleed/poison affixes and supports ride along. Every new proc
   needs its RNG-consumption pinning test (the invariant).
2. **Auras.** Toggled reservation gems: reserve a chunk of max mana, grant a
   persistent sheet mod. Self + minions first (the summoner synergy is
   free), party-wide radius second — that's the multiplayer payoff. Opens
   the classic tension: reserved mana vs. cast budget.
3. **Curses.** Hex debuffs on the existing buff machinery (refresh-not-stack,
   pending-buff queue), one-curse cap on the target. Two gems to start —
   an offensive lever (flammability-like) and a defensive one
   (enfeeble-like) — plus a monster that curses *you*.
4. **Channelling + a movement skill.** The action model's next deliberate
   growth (RISKS #1 — design first, don't accrete): a held channel that
   repeats its effect stage while fed mana, and a cooldown-gated dash.
   Both are new command-model verbs, and cooldowns are new saved+hashed
   actor state. One channelled beam and one blink ship the machinery.
5. **Crafting depth + the Forge.** Regal, exalt, annulment, scouring orbs —
   the missing half of the crafting ladder. And the vendor draupforge was
   named for: melt unwanted items into orb shards at a hideout forge
   (stash-pattern host verb, new hideout panel). Suddenly no drop is
   worthless, and "pick it up?" has an answer.
6. **Passive growth.** Milestone forks every 5 levels to the cap (15, 20,
   25, ...), same pick-3 format, each a real identity choice. This is the
   scaffold ascendancy-lite classes hang off later — don't build classes
   yet, just leave room.
7. **Content riding it all.** Every mechanic above ships with at least one
   support and one unique that celebrates it (aura uniques, a poison
   dagger, a channelling stick). Rough targets: ~16 cuttable skills, ~20
   supports, ~12 uniques, and one new staged boss that curses and channels
   at you — the mechanics teach best when a monster uses them first.

## Track 2 — The Living Descent (the world and the account deepen)

Owns `server/`, `web/`, and the wire. Sim changes on this track are
additive-only (see the seam contract). The guardrail on everything below:
this is an ARPG, not a roguelite — characters are permanent, account
progression is strictly additive, nothing seasonal, nothing resets. The
reroll must always be a temptation, never a punishment.

1. **Account roster + shared stash.** Today one cookie = one name = one
   character, and claiming a new name orphans the old — rolling an alt is
   an act of self-destruction. Fix that first: multiple characters under
   one identity token, the join screen becomes a character select, the
   stash becomes account-wide. This creates the genre's alt loop — the
   build-defining drop your current character can't use becomes the *seed
   of the next one* — and every item below stands on it. Pure identity
   store + lobby + join screen work; the sim never notices.
2. **Biomes.** Depth bands with identity: a monster-roster mix, a palette,
   ambient audio, and a mapgen flavor per band — crypt (the current look),
   caves, something colder further down. Biome selection lives at the
   descent layer (`server/descent.go`); rosters are content-table appends;
   new cave-shaped mapgen lands as a *new* map kind so existing goldens
   stand; the welcome carries a biome id and the client tints per biome.
   Variety is the loudest complaint about the world itself.
3. **Floor modifiers + the descent chart.** Floors roll named, depth-scaled
   mods — extra packs, fast monsters, monsters leech, bonus rarity — and
   you read them *before* you commit: the stairs offer a delve-chart
   choice of routes down, each exit with visible mods and reward weights.
   Side chambers hold your depth instead of descending, with stacked mods
   and juiced rewards — farming a comfortable depth becomes a strategy,
   not a failure to push. Mods apply as spawn-time sheet mods on the
   monster-mod machinery; shrines (touchable altars, timed buffs via the
   pending-buff queue) ride along as the in-floor counterpart.
4. **Depth checkpoints — account-wide, level-gated.** Guardians beaten
   unlock deep starts for *every* character on the account, gated by
   character level so a fresh alt still levels, just skips the shallow
   slog once it's earned. Starting deep trades portal count so "from the
   top" stays a real choice. Alts compound instead of restarting.
5. **The ladder + the death recap.** Best-floor per character, persisted —
   and the ladder entry IS the build: skills, supports, uniques, straight
   off the session-67 sheet machinery. "Floor 52 on a poison dagger" is
   the best build-experimentation ad the game can run. On death: what hit
   you, for how much, on which floor, under which mods — the recap turns
   frustration into a build lesson.
6. **Hardcore + SSF at character creation.** Voluntary, permanent flags:
   hardcore — one death ends the character (the forget machinery exists;
   the fallen earn a memorial row in the hideout) — and solo-self-found —
   no shared stash, no parties. Each mode gets its own ladder board. The
   strongest non-seasonal reroll driver the genre has found: the
   constraint is the content, and it never expires.
7. **Feats + hideout trophies.** Account-wide achievements on deterministic
   triggers — "Barrow King, untouched", "floor 20, minions only", "floor
   30 hardcore" — paying out trophies and hideout dressing. Some feats are
   only reachable by builds you haven't played yet; the hideout becomes
   the account's history made visible.
8. **Chat.** Party chat minimum — a JSON frame relayed at the host layer,
   a chat line in the client. The game is multiplayer; the players are
   mute. Ping-a-map-spot rides the same frame if it's cheap.
9. **Hideout & shell polish.** Stash tabs (60 slots → tabbed), a settings
   panel (volume, keybinds, damage-number toggle), minimap affordances,
   hideout dressing. The meta-surface polish pass the join screen and
   hideout have earned.

## The seam contract (what makes concurrent work safe)

Both tracks branch off `main`, keep PRs small, and merge on green — parked
long-lived branches are how concurrent development dies. Merging deploys;
the deploy is the point.

- **Track 1 owns sim behavior**: golden re-records, `SaveVersion` bumps,
  `World.Hash` coverage, RNG-pinning tests. Its client work is append-only
  at the established extension points (`SKILL_META`, icon SVGs, `MOD_FMT`,
  buff indicators) — no panel or layout restructuring.
- **Track 2 owns the wire and the shell**: `protocol.Version`, the
  `protocol/binary.go` ↔ `web/net.js` codec pair, `server/`, and `web/`
  structure. Its sim changes are **additive only** — new map kinds, new def
  fields, new entities no existing scenario contains. Existing goldens stay
  byte-stable on Track 2 branches; if a Track 2 feature needs existing
  behavior to change, that's a Track 1 ticket or a coordinated merge.
- **Version numbers are taken at merge time, never reserved.** Save vN and
  wire vN: whoever rebases second takes the next free number. Golden
  testdata conflicts are never hand-merged — rebase, then re-record.
- **`content/` tables are append-only for both tracks** (already a replay
  invariant) — concurrent appends merge clean.
- STATUS.md conflicts are prose; keep both entries and move on.

## Cross-cutting: keep sprinkling juice

Same rule as roadmap one — juice ships *with* features, never saved for a
polish pass. It splits along the seam too: combat-clarity juice (aura rings,
DoT stack pips over health bars, channel beams, curse markers) belongs to
Track 1's features; meta juice (ladder fanfare, biome ambience, recap
presentation, trophy reveals, the hardcore memorial) to Track 2's.

## Deliberately not on this list (yet)

- **Client prediction / sim-to-wasm** — the latency-feel layer (DESIGN §13).
  Real, valuable, and a whole project; not before the content is worth
  predicting.
- **Player-to-player trade** — wants economy-integrity design (durable item
  identity lives in the character store, not entity IDs) before any UI.
- **Ascendancy classes** — Track 1 item 6 builds the scaffold; classes come
  when milestones feel cramped.
- **Seasons / ladder resets** — Track 2's meta is deliberately
  non-seasonal; the reroll drivers (modes, feats, the alt loop) never
  expire. Resets only if a real economy someday demands them.
- **Controller support, content-as-data-files, admin auth** — each
  graduates when its audience shows up.
