# Roadmap — the road to "actually fun"

A fun-first reading of what to build next. This is **not** the risk register
(`RISKS.md`) or the foundations status (`STATUS.md`) — it deliberately ignores
structural debt and server hardening and asks one question only: *what gets a
player to "just one more run" fastest?*

The foundations are done. The thing that is missing is not engine — it's a
**game**. Right now you spawn in a dungeon, kill a few monsters, pick up gear,
maybe ding a level, and then… nothing. No stakes, no escalation, no goal, no
build identity. Every milestone below exists to fix one of those.

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

## Phase 1 — Give the run a shape (the descent)

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

## Phase 2 — Give combat moments (threat + feel)

*Makes the loop satisfying second-to-second and gives the descent a payoff.*

- **A telegraphed boss** at floor milestones — a multi-stage, wind-up attack you
  dodge by reading it. This is the destination of a descent and the first fight
  that's *about* the player playing well. (It also forces the action model to
  grow on purpose — RISKS.md #1 — so design that state machine first.)
- **Pack variety and density.** A few more monster archetypes and the occasional
  "rare/magic" pack with a modifier or two. Variety is what keeps rooms from
  feeling identical.
- **Juice** (see the cross-cutting note below) — danger you can *see* coming and
  feedback you can *feel* is most of perceived fun.

## Phase 3 — Make the character yours (build agency)

*Creates the "one more character" replay drive and makes loot mean something.*

- **Skill gems / slottable skills.** Choosing your skills (instead of a fixed
  per-actor list) is the single biggest source of build identity. Start tiny: a
  handful of active gems and a couple of support modifiers.
- **A small passive / ascendancy-lite choice** at level-up milestones — a few
  forks, not a 1000-node tree. Just enough that two players diverge.
- **Per-slot affix pools** so gear can target a build (no more cast speed on
  boots). An item is exciting when it's good *for me*, not good in the abstract.

## Phase 4 — Make loot sticky (reward + retention)

*The dopamine layer and the reason to come back.* Leans on DESIGN §14 phase 2
(server-owned characters that survive disconnects).

- **A town hub + persistent stash + a character that survives logout.** Without
  this, every good drop evaporates and there's no meta-progression to chase.
- **Currency / crafting-lite** — a couple of reroll/upgrade sinks so loot has a
  use beyond equip-or-vendor.
- **Uniques with build-defining mods** — the chase items that make a player say
  "I want to build around *that*."

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
auth, replay logs) are real — but they're "make it robust for strangers," not
"make it fun for one player." Defer them until there's a fun game worth
hardening and players worth protecting.
