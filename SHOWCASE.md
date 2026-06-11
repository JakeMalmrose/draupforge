# Showcase — the architectural fixtures

A guided tour of the load-bearing machinery, for engineers. `DESIGN.md` is
the *why* and the rules; this is the *look what that bought us*. Everything
below is built and tested, not aspirational — `STATUS.md` tracks the
shortcuts and `RISKS.md` the known structural debts.

The whole game is ~a dozen Go packages and three browser files with no build
step. The thesis: in a server-authoritative ARPG, **determinism is the
product** — replays, golden tests, anti-cheat verification, and rollback all
fall out of one discipline, so the entire stack is shaped around protecting
it.

## 1. A bit-for-bit deterministic simulation

Same seed + same commands = the same world, byte for byte, every time, on
every architecture. Not "close enough" — `TestDeterminism` runs the same
script twice and asserts identical state hashes *every tick*
(`sim/sim_test.go`).

What it takes, all enforced:

- **No floats anywhere in `sim/`.** All gameplay math — damage, stats,
  positions, velocities — is `Fixed` (int64 milli-units, `sim/fixmath`),
  including integer Newton's-method `Sqrt`. Float reproducibility across an
  arm64 dev Mac and an amd64 server is "probably fine," and *probably* is a
  rotten foundation for replays.
- **No wall clock.** Time is a `uint64` tick counter at a fixed 30Hz; the
  host decides when `Step` runs. The sim cannot ask what time it is.
- **No Go map iteration in sim logic.** Map order is random per run; maps
  exist only as ID→entity lookups. Entities live in slices, iterated in
  insertion order, tombstoned mid-tick and compacted at tick end.
- **Owned randomness.** Hand-rolled xoshiro256\*\* (`sim/core/rng.go`) — not
  stdlib, so no Go release can ever silently reshuffle our sequences.

## 2. Four named RNG streams, so replays stay diff-able

One world seed expands (splitmix64) into **four independent streams**:
combat, loot, AI, mapgen. Adding an AI dice roll doesn't reshuffle every
loot drop that comes after it — a replay diff after a change to one system
shows *that system's* divergence, not avalanche noise. Stream consumption
order is explicitly part of replay compatibility.

## 3. Golden replay testing: behavior changes can't hide

The world FNV-1a-hashes its complete state — every actor, item affix,
projectile, and all four RNG states (`sim/core/hash.go`). The golden replay
test runs a scripted fight and compares per-tick hashes against a committed
trace. **Any** behavior change fails the suite; intentional ones re-record
the trace in the same commit, so behavior changes are visible in review as
"the golden moved." Regression testing for combat math, free, forever.

The same hash shows up live on the admin dashboard — two instances fed the
same seed and commands should display the same hash, which makes server
state verification a thing you can eyeball.

## 4. The stat algebra (the genre's heart, done right)

Every number flows through one evaluator implementing the PoE modifier
algebra:

```
final = (base + Σflat) × (1 + Σincreased − Σreduced) × Π(1 + more_i) × Π(1 − less_i)
```

*increased* shares one additive bucket; each *more* multiplies separately —
that split is the genre's entire balance language and it's first-class, not
emergent from scattered arithmetic.

Conditionality is a **uint64 tag bitset**: "10% increased fire spell damage"
is a modifier tagged `{Fire, Spell}`, a query carries a tag context, and
applicability is one `AND`+compare. Conditions like "while at full life" are
just tags computed per tick on the actor, so the evaluator itself stays a
pure, memoized, golden-testable function of the modifier list. Subtle trap
already caught by tests: per-damage-type queries *replace* the damage-type
tag in context, so added-fire can't leak into a cold roll.

## 5. Overflow-proof fixed-point (learning from PoE's 35.79M DPS wall)

PoE1 famously capped DoT at 2³¹ damage-per-minute — ~35.79M DPS — because a
unit choice multiplied into an integer ceiling. Our `fixmath.Mul` runs every
product through a **128-bit intermediate** (`math/bits.Mul64`) and *panics*
on result overflow: power creep past the int64 ceiling is a loud,
golden-test-visible crash, never silent wraparound corrupting a damage roll.
Division by zero panics too — a zero divisor in the sim is always a logic
bug, and returning 0 would hide it.

## 6. The damage pipeline: named stages, not smeared arithmetic

A hit is a value flowing through explicit stages —

```
SkillBase → Added → Conversion → Inc/More → HitCheck → CritCheck
  → Mitigation per type → DamageTaken → ApplyToPools (ES before life) → PostHit
```

— each independently unit-testable, with conversion order locked as a
balance lever. Damage-over-time is deliberately **not** a hit: DoTs skip
hit/crit/armour on a separate per-tick path, sharing only the mitigation
tail. Conflating them is the classic ARPG implementation mistake.

## 7. One goroutine owns the world

A world is **strictly single-goroutine**: no locks, no atomics, no races,
perfect determinism. The phase order inside `Step` — commands → AI →
actions → projectiles → hits → DoTs → deaths/loot → events → compact — is
the determinism contract, and only the root `sim` package knows it.
Parallelism is horizontal: many instances, many goroutines (exactly how PoE
itself scales). The package graph is cycle-free by construction: data types
in `sim/core`, logic in leaf packages, `protocol/` imports *nothing*.

The server respects the invariant rather than fighting it: connection
goroutines only decode frames into a mutex-guarded queue; the tick goroutine
drains it and performs **all** world mutation — joins, leaves, commands,
stepping — at tick boundaries (`server/server.go`). Clients are a suggestion
box, not an authority: the server overwrites the actor field on every
command, so forging another player's ID does nothing (there's a test that
tries).

## 8. Netcode: binary deltas against acked baselines

The WebSocket wire (`protocol/binary.go` + its hand-written JS mirror
`web/net.js`, ~800 lines total) sends **delta frames against the last view
the client acknowledged**:

- Per-entity field masks: an actor that only moved costs an ID, a mask, and
  two varints.
- Client acks each view; the server keeps a short window of unacked views as
  baseline candidates and falls back to a keyframe on ack gaps — packet loss
  degrades to "send a keyframe," never to desync.
- **Interest culling is server-side truth**: `BuildSnapshotFor(viewer,
  radius, events)` means a snapshot is one client's *view*, not the world —
  the anti-maphack property is structural, since omniscience never crosses
  the wire.
- The sim ticks at 30Hz; views ship every N ticks (default 10Hz) with the
  skipped ticks' events accumulated, so send rate is a knob that drops no
  information.
- permessage-deflate with cross-frame context, which suits view streams'
  heavy redundancy.

The Go/JS codec pair is hand-maintained and kept honest by a versioned
welcome frame: any wire change bumps `protocol.Version` and a stale client
**fails loudly at connect** instead of misreading binary.

## 9. A client that renders the server's timeline, not the network's

`web/` is vanilla JS + canvas, no build step. The renderer draws ~150ms
behind the newest view, interpolating between the two views around the
render time — but the interpolation buffer is keyed to the **server tick
timeline** (`tick × tickMs`), not packet arrival times. A clock-offset
estimator locks onto the fastest-arriving views, decays slowly if latency
genuinely rises, and resnaps after stalls — so network jitter perturbs only
the offset estimate, never frame pacing. Entities crossing interest-range
edges (and deaths, and pickups) fade in/out instead of popping.

## 10. Pause that keeps the world warm

Pause lives in the server, not the sim (the sim has no concept of wall time
to pause). The tick loop keeps running — joins land, views keep flowing as
near-empty deltas, admin ops drain — but `Step` is skipped and player
commands are **dropped, not queued**, so a half-hour pause can't release a
flood of stale intent on resume. Clients get a `pause` control frame and
show an overlay. You can spawn monsters into a frozen world, arrange a
scene, and unpause.

## 11. An admin port with zero new locks

`server/admin.go` serves a dashboard + JSON API on its own port: tick health
(target vs actual Hz), entity/client/bandwidth counts, a live event stream,
the world hash, pause/resume, spawn, kick. The trick: every handler that
touches the world enqueues a closure that **the tick goroutine itself runs
between ticks** and replies over a channel. The single-goroutine invariant
holds, every response is a consistent between-ticks view, and the admin
feature added no locking to the world at all. The whole dashboard is one
embedded HTML string — it works wherever the binary runs. (Deliberately no
auth yet: localhost/tailnet only.)

## 12. Debuggability as a first-class wire

Three ways to watch the same world, same frames, different encodings:

```sh
echo '{"kind":"move","x":5000,"y":0}' | nc localhost 7777   # omniscient NDJSON debug wire
open 'http://localhost:8080/?format=json'                   # exactly what a culled client sees, in JSON
go run ./cmd/headless -script scripts/slice.json -hash      # no server at all: scripted fight, per-tick hashes
```

The TCP wire even handles commands sent before the welcome arrives (echo |
nc races its own spawn) by buffering them until the actor exists — there's a
test for that too, because that's exactly how a human pokes it.

## 13. Content is code, events are the lingua franca

Skills, monsters, base items, and affix pools are typed Go literals in
`content/` — compile-checked, refactorable, zero parsers, and the sim never
imports them (defs are wired in by the host). Monster AI emits the **same
Command type players send** — AI is just another command source, so
authority and validation are uniform. One synchronous event stream (hits,
deaths, drops, equips) feeds the combat log, the web client, the admin
dashboard, and the tests alike.

---

*Doc map: `README.md` (what/quickstart) → `DESIGN.md` (foundational
decisions) → `STATUS.md` (current state, invariants, session log) →
`RISKS.md` (ranked structural debts, honestly) → this file (the brag
sheet).*
