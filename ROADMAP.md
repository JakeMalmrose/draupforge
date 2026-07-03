# Roadmap — the road to "actually fun"

A fun-first reading of what to build next. This is **not** the risk register
(`RISKS.md`) or the foundations status (`STATUS.md`) — it deliberately ignores
structural debt and server hardening and asks one question only: *what gets a
player to "just one more run" fastest?*

The foundations are done — and so, now, are most of the phases below: the
descent (stakes), gems/passives/per-slot affixes (build identity), currency,
rarity packs, juice, and a public multiplayer server your friends can join.
The phases keep their original text as the record of *why* each thing was
built; ✅ marks what shipped — as of session 50, all of it did. What's next
is a content question (more skills, uniques, bosses, floors), not a
systems one.

## North star: the loop has to pull

The ARPG core loop is *kill → loot → get stronger → kill harder things*. We
have every verb. What we don't have is the **tension and arc** that make the
loop addictive:

- a reason to keep going (escalation + a score),
- a reason to be afraid (threat you can see coming),
- a reason this character is *yours* (build choices),
- a reason to log back in (loot that sticks around and matters).

The phases are ordered by fun-per-unit-effort. Ship them thin and end-to-end,
not deep and partial.

## Phase 1 — Give the run a shape (the descent) — ✅ shipped (session 15)

*Turns a sandbox into a game.* This is already the plan (STATUS.md feature plan,
DESIGN §14) and it is correctly #1: nothing else matters without stakes.

- **Floors that escalate.** Descend via stairs into a fresh World seeded from
  (run seed + floor index); packs scale with floor depth (the `ActorDef.Level`
  / `PerLevel` machinery already exists).
- **A death/portal economy.** Death costs something and ejects you; a run grants
  limited portal uses. Risk/reward — "push one more floor or bank it?" — is the
  whole game in one decision.
- **Depth is the score.** "How deep did you get" is the first thing a player can
  brag about and the first thing that makes a run feel like *a run*.

If only one thing ships this year, it's this.

## Phase 2 — Give combat moments (threat + feel) — mostly shipped

*Makes the loop satisfying second-to-second and gives the descent a payoff.*

- ✅ **A telegraphed boss** at floor milestones — a multi-stage, wind-up attack
  you dodge by reading it. This is the destination of a descent and the first
  fight that's *about* the player playing well. (It also forces the action
  model to grow on purpose — RISKS.md #1 — so design that state machine
  first.) *Shipped (session 45): the Barrow King every 5th floor — tracked
  triple slam, ring volleys, gap-bisecting storm below half life — on the
  staged-skill machinery of DESIGN §15, with exact ground telegraphs on the
  wire.*
- ✅ **Pack variety and density.** Five archetypes (zombie, archer, ghoul,
  skeleton mage, colossus) plus magic/rare monster mods with scaled payouts.
- ✅ **Juice** — damage numbers, hit flashes, death pops, drop beams, audio
  stingers, minimap. Keep adding (see the cross-cutting note).

## Phase 3 — Make the character yours (build agency) — ✅ shipped

*Creates the "one more character" replay drive and makes loot mean something.*

- ✅ **Skill gems / slottable skills.** Shipped bigger than planned (session
  34): loot-driven uncut gems, draft-of-3 cutting, 10 supports, sockets.
- ✅ **A small passive / ascendancy-lite choice** at level-up milestones —
  forks at levels 5 and 10.
- ✅ **Per-slot affix pools** so gear can target a build (no more cast speed
  on boots).

## Phase 4 — Make loot sticky (reward + retention) — the live frontier

*The dopamine layer and the reason to come back.* DESIGN §14 phase 2 shipped
(sessions 37–38): named characters survive logout via the identity store.

- ✅ **A town hub + persistent stash.** The hideout is the hub; the stash
  (session 48) is a 60-slot per-identity bank on the identity store — a
  good drop no longer lives or dies with one character's bag.
- ✅ **Currency / crafting-lite** — transmute/alch/chaos (+ jeweller for gem
  sockets), banked straight to the killer.
- ✅ **Uniques with build-defining mods** — the chase items that make a player
  say "I want to build around *that*." (Session 50: four uniques on sheet
  stats nothing else rolls — extra projectiles, extra chains — each with a
  downside worth arguing about.)

## Cross-cutting: sprinkle juice continuously

Cheap, mostly client-side, punches far above its weight for *perceived* fun —
do a thin slice early and keep adding:

- floating damage numbers + crit emphasis,
- hit flashes, death pops, brief screen shake on big hits,
- pickup/level-up/rare-drop stingers (audio), rarity-colored drop beams,
- clearer telegraphs for enemy wind-ups.

None of it changes the sim; all of it makes the existing combat feel twice as
good. Don't save it all for a "polish pass" — it's what makes playtests fun
*while* you build the rest.

## Deliberately not on this list (yet)

The structural risks (`RISKS.md`), the determinism footguns, performance work
(spatial grids, GC), and serious netcode hardening (per-client send queues,
rate limiting, replay logs) are real — but they're "make it robust for
strangers," not "make it fun for one player." One update since the public
deploy: strangers *can* connect now, so hardening graduates the moment they
actually do. Until then, fun still wins the tie.
