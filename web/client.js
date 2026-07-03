// draupforge web client — phase 2: still a window into the authoritative
// server, now over the binary delta wire. The client reconstructs views from
// delta frames (net.js), acks each one, and renders ~150ms behind the newest
// view, interpolating entity positions between views — which hides both the
// reduced send rate and snapshot quantization. No prediction yet: input
// still feels its latency.

"use strict";

const SCALE = 42;          // pixels per world unit
const PICKUP_RANGE = 1.9;  // world units; matches server (with margin)
const USE_RANGE = 1.9;     // stairs/portal use range; matches server (with margin)
const LOG_LINES = 9;
const VIEW_HISTORY = 32;   // kept as delta baselines; matches the server cap

// ---------------------------------------------------------------- state

let ws = null;
let myId = 0;
let gen = 0;                // welcome generation; acks echo it
let myName = "";            // our identity name ("" = guest)
let roster = new Map();     // actor id → identity name, server-maintained
let guestMode = false;      // the join screen chose "play as guest"
let fatalError = false;     // server refused us; keep that overlay on close
let social = null;          // latest social snap: {party, online, invite}
let snap = null;            // newest reconstructed view (HUD, log, input)
let seenSelf = false;       // distinguishes "not spawned yet" from "died"
let pendingPickup = 0;      // drop entity we're walking toward
let lastPickupSent = 0;
let stairs = null;          // {x, y} milli — this floor's descent stairs
let runState = null;        // RunSnap: {floor, portals, run, best, portal?}
let pendingDescend = false; // walking toward the stairs to take them
let lastDescendSent = 0;
let pendingPortal = false;  // walking toward the portal to enter it
let lastPortalSent = 0;
let mouse = { x: 0, y: 0 }; // canvas px
let cam = { x: 0, y: 0 };   // world units
const names = new Map();    // entity id -> label, survives despawn

// Interpolation: views buffered on the SERVER timeline (tick × tickMs), so
// network jitter perturbs only the clock-offset estimate, not view spacing.
// The renderer lerps between the two views around (now + clockOffset -
// interpDelay). clockOffset locks onto the fastest-arriving views (max) and
// decays slowly so a genuine latency increase re-converges; a huge backward
// jump (server pause, long stall) resnaps instead of waiting out the decay.
let worldMap = null;        // terrain from the welcome: {w, h, tile, rows}
let interpDelay = 150;      // ms; refined from the welcome's send cadence
let tickMs = 1000 / 30;     // refined from the welcome's tick_hz
let clockOffset = null;     // server-timeline ms minus performance.now() ms
const interpBuf = [];       // { st: server-timeline ms, view }, tick order
const INTERP_BUF_MAX = 60;  // hidden-tab safety: ws keeps delivering, rAF doesn't

// Fade-in/out so interest-range edges (and deaths) don't pop: firstSeen
// drives fade-in alpha; entities that leave the view linger as ghosts.
const FADE_MS = 250;
const firstSeen = new Map(); // entity id -> performance.now() at first sight
const ghosts = [];           // { until, coll, e } pending fade-outs

// Delta decoding: recently received views by tick, for use as baselines.
const viewHistory = new Map();
let awaitKeyframe = false;  // lost our baseline; ignore deltas until reset

const canvas = document.getElementById("game");
const ctx = canvas.getContext("2d");

// ------------------------------------------------------------- network

function connect() {
  fatalError = false; // a fresh attempt gets fresh disconnect reporting
  const wsProto = location.protocol === "https:" ? "wss" : "ws";
  const params = new URLSearchParams(location.search);
  if (guestMode) params.set("guest", "1");
  const qs = params.toString();
  ws = new WebSocket(`${wsProto}://${location.host}/ws${qs ? "?" + qs : ""}`);
  ws.binaryType = "arraybuffer";
  ws.onmessage = (e) => {
    if (typeof e.data === "string") {
      const msg = JSON.parse(e.data);
      if (msg.type === "welcome") {
        if (msg.v !== PROTOCOL_VERSION) {
          showOverlay(`PROTOCOL MISMATCH (server v${msg.v}, client v${PROTOCOL_VERSION})`);
          ws.close();
          return;
        }
        resetWorld(msg);
        if (msg.tick_hz && msg.send_every) {
          // 1.5 send intervals behind: one interval to always have a newer
          // view to lerp toward, half an interval of jitter slack.
          tickMs = 1000 / msg.tick_hz;
          const interval = tickMs * msg.send_every;
          interpDelay = Math.min(Math.max(1.5 * interval, 100), 250);
        }
      } else if (msg.type === "run") {
        runState = msg.run || null;
        updateRunHUD();
      } else if (msg.type === "pause") {
        if (msg.paused) showOverlay("PAUSED");
        else hideOverlay();
      } else if (msg.type === "roster") {
        applyRoster(msg.roster);
      } else if (msg.type === "social") {
        social = msg.social || null;
        renderSocial();
      } else if (msg.type === "stash") {
        stash = msg.stash || null;
        if (!panel.classList.contains("hidden")) renderPanel(me(), true);
      } else if (msg.type === "error") {
        // Refused (duplicate session, say). Back to the join screen, which
        // offers the ways forward: another name, or guest mode.
        fatalError = true;
        document.getElementById("join").classList.remove("hidden");
        document.getElementById("join-error").textContent = msg.error || "refused";
      } else if (msg.type === "snapshot") {
        onView(jsonToView(msg.snapshot)); // ?format=json debug wire
      }
      return;
    }
    const view = decodeViewFrame(e.data, (tick) => viewHistory.get(tick));
    if (view.needBaseline) {
      // We pruned the view this frame deltas against. Tell the server to
      // start over; skip frames until the keyframe lands.
      if (!awaitKeyframe) send({ kind: "ack", tick: 0, gen });
      awaitKeyframe = true;
      return;
    }
    awaitKeyframe = false;
    viewHistory.set(view.tick, view);
    while (viewHistory.size > VIEW_HISTORY) {
      viewHistory.delete(viewHistory.keys().next().value);
    }
    send({ kind: "ack", tick: view.tick, gen });
    onView(view);
  };
  ws.onclose = () => {
    if (!fatalError) showOverlay("DISCONNECTED");
  };
}

// applyRoster swaps in the actor→name map for named players. JSON object
// keys arrive as strings; view actor ids are numbers.
function applyRoster(obj) {
  roster = new Map(Object.entries(obj || {}).map(([id, n]) => [Number(id), n]));
}

// resetWorld applies a welcome. Every welcome is a whole new world on the
// same socket (join, floor swap, portal travel, death eject), so all
// world-derived state resets: views, interpolation, fades, names, layouts.
function resetWorld(msg) {
  myId = msg.actor;
  myName = msg.name || "";
  applyRoster(msg.roster);
  gen = msg.gen || 0;
  worldMap = msg.map || null;
  buildMinimapBase();
  stairs = msg.stairs || null;
  passiveTable = msg.passives || [];
  passiveKey = "";
  supportTable = msg.supports || [];
  cutSkillTable = msg.cut_skills || [];
  skillBarKey = "";
  closeCutDialog();
  autoCutShown = false;
  actorMotion.clear();
  telegraphMax.clear();
  stash = msg.stash || null;
  runState = msg.run || null;
  snap = null;
  seenSelf = false;
  pendingPickup = 0;
  pendingDescend = false;
  pendingPortal = false;
  clockOffset = null;
  interpBuf.length = 0;
  viewHistory.clear();
  awaitKeyframe = false;
  firstSeen.clear();
  ghosts.length = 0;
  effects.length = 0;
  flashes.clear();
  names.clear();
  bagLayout.clear();
  panelKey = "";
  hideOverlay();
  updateRunHUD();
}

function send(cmd) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(cmd));
}

// world coords in the protocol are milli-units (int). Local math uses units.
const toUnits = (milli) => milli / 1000;
const toMilli = (units) => Math.round(units * 1000);

// ------------------------------------------------------------------ views

function me() {
  if (!snap || !myId) return null;
  return snap.actors.get(myId) || null;
}

function onView(view) {
  snap = view;
  const now = performance.now();
  const newest = interpBuf.length ? interpBuf[interpBuf.length - 1] : null;
  // A paused server repeats its current tick to keep the wire warm; only
  // advancing ticks enter the interpolation timeline.
  if (!newest || view.tick > newest.view.tick) {
    const st = view.tick * tickMs;
    const off = st - now;
    if (clockOffset === null || off > clockOffset || clockOffset - off > 4 * interpDelay) {
      clockOffset = off; // first view, a faster path, or a stall/pause: resnap
    } else if (newest) {
      // Decay toward the observed offset so a genuinely slower connection
      // re-converges instead of clamping at the newest view forever.
      clockOffset -= Math.min(0.05 * (st - newest.st), clockOffset - off);
    }
    if (newest) {
      diffFades(newest.view, view, now);
      diffCastVFX(newest.view, view);
    }
    interpBuf.push({ st, view });
    while (interpBuf.length > INTERP_BUF_MAX) interpBuf.shift();
  }

  for (const a of view.actors.values()) {
    let name = a.def === "player" ? roster.get(a.id) || `player ${a.id}` : a.def.replace("_", " ");
    if (a.mods && a.mods.length) name = `${a.mods.join(" ")} ${name}`;
    names.set(a.id, name);
  }

  const self = me();
  if (self) {
    seenSelf = true;
    updateHUD(self);
    updatePassiveChooser(self);
  } else if (seenSelf) {
    showOverlay("YOU DIED");
  }
  syncCutDialog(self);
  autoOpenCut(self);

  // Position lookups fall back to the previous view: the victim of a
  // killing blow (and the dier of a death event) is already compacted out
  // of the view that carries the event.
  const findEnt = (id) =>
    view.actors.get(id) || (newest && newest.view.actors.get(id)) || null;
  const chains = new Map(); // caster id → strike positions, event order
  for (const ev of view.events) {
    logEvent(ev);
    sfxForEvent(ev);
    if (ev.kind === "hit") {
      // Splash hits (projectile explosions) arrive as "<skill>:aoe" — they
      // keep their numbers/flash but skip the impact burst; the explosion
      // ring at the direct hit is their visual.
      const splash = ev.note.endsWith(":aoe");
      const note = splash ? ev.note.slice(0, -4) : ev.note;
      const v = IMPACT_VFX[note];
      const target = findEnt(ev.other);
      if (v && target && !splash) spawnImpact(target.pos, view.tick * tickMs, v);
      if (target && !splash) {
        const ex = EXPLODE_VFX[note];
        if (ex) spawnExplosion(target.pos, view.tick * tickMs, ex);
        const cv = CHAIN_VFX[note];
        if (cv) {
          let c = chains.get(ev.actor);
          if (!c) {
            const caster = findEnt(ev.actor);
            c = { v: cv, pts: caster ? [caster.pos] : [] };
            chains.set(ev.actor, c);
          }
          c.pts.push(target.pos);
        }
      }
      if (target) {
        spawnDamageNumber(target.pos, view.tick * tickMs, ev.amount, ev.crit, ev.other === myId);
        flashes.set(ev.other, view.tick * tickMs + FLASH_MS);
      }
      if (ev.other === myId) shakeUntil = performance.now() + SHAKE_MS;
    }
    if (ev.kind === "death") {
      const dier = findEnt(ev.actor);
      if (dier) spawnDeathPop(dier.pos, view.tick * tickMs, dier);
    }
    if (ev.kind === "drop") {
      const d = view.drops.get(ev.other);
      if (d) spawnDropLanding(d.pos, view.tick * tickMs, d.item.rarity);
    }
  }
  // One bolt per caster per view: caster → victim → victim, in hit order.
  for (const c of chains.values()) spawnChainLightning(c.pts, view.tick * tickMs, c.v);
  // Flash entries expire by clock; sweep the map so dead IDs don't pile up.
  for (const [id, until] of flashes) {
    if (until < view.tick * tickMs - 2000) flashes.delete(id);
  }
  autoPickup(self);
  autoDescend(self);
  autoPortal(self);
  if (!panel.classList.contains("hidden")) renderPanel(self);
}

function autoPickup(self) {
  if (!pendingPickup || !self) return;
  const drop = snap.drops.get(pendingPickup);
  if (!drop) { pendingPickup = 0; return; } // got it, or someone else did
  const dx = toUnits(drop.pos.x - self.pos.x);
  const dy = toUnits(drop.pos.y - self.pos.y);
  const now = performance.now();
  if (Math.hypot(dx, dy) <= PICKUP_RANGE && now - lastPickupSent > 200) {
    send({ kind: "pickup", target: pendingPickup });
    lastPickupSent = now;
  }
}

// autoDescend/autoPortal mirror autoPickup: a click on the stairs/portal
// walks there, and the use command fires once in range. The server
// validates range again; the welcome for the new world clears the flag.
function autoDescend(self) {
  if (!pendingDescend || !self || !stairs) return;
  const now = performance.now();
  if (near(self, stairs, USE_RANGE) && now - lastDescendSent > 300) {
    send({ kind: "descend" });
    lastDescendSent = now;
  }
}

function autoPortal(self) {
  const portal = runState && runState.portal;
  if (!pendingPortal || !self || !portal) return;
  const now = performance.now();
  if (near(self, portal, USE_RANGE) && now - lastPortalSent > 300) {
    send({ kind: "enter_portal" });
    lastPortalSent = now;
  }
}

function near(self, p, range) {
  return Math.hypot(toUnits(p.x - self.pos.x), toUnits(p.y - self.pos.y)) <= range;
}

// -------------------------------------------------------------- render

function resize() {
  canvas.width = window.innerWidth;
  canvas.height = window.innerHeight;
}
window.addEventListener("resize", resize);
resize();

const worldToScreen = (mx, my) => ({
  x: (toUnits(mx) - cam.x) * SCALE + canvas.width / 2,
  y: (toUnits(my) - cam.y) * SCALE + canvas.height / 2,
});
const screenToWorldUnits = (px, py) => ({
  x: (px - canvas.width / 2) / SCALE + cam.x,
  y: (py - canvas.height / 2) / SCALE + cam.y,
});

// span() picks the two buffered views around the render time (on the server
// timeline) and the blend factor between them. Past the newest view we clamp
// rather than extrapolate.
function span() {
  if (interpBuf.length === 0) return null;
  const rt = performance.now() + clockOffset - interpDelay;
  while (interpBuf.length > 2 && interpBuf[1].st <= rt) interpBuf.shift();
  const a = interpBuf[0];
  const b = interpBuf.length > 1 ? interpBuf[1] : a;
  let t = 0;
  if (rt >= b.st) t = 1;
  else if (rt > a.st) t = (rt - a.st) / (b.st - a.st);
  return { from: a.view, to: b.view, t };
}

// diffFades compares consecutive views: actors and drops entering the view
// start a fade-in, ones leaving become fade-out ghosts. Projectiles are
// excluded — they're too short-lived to read as anything but mush when
// faded. (This covers interest-range edges, spawns, deaths, and pickups.)
function diffFades(prev, view, now) {
  for (const coll of ["actors", "drops"]) {
    for (const id of view[coll].keys()) {
      if (!prev[coll].has(id)) firstSeen.set(id, now);
    }
    for (const [id, e] of prev[coll]) {
      if (!view[coll].has(id)) {
        firstSeen.delete(id);
        ghosts.push({ until: now + FADE_MS, coll, e });
      }
    }
  }
}

function alphaFor(id, now) {
  const t0 = firstSeen.get(id);
  return t0 === undefined ? 1 : Math.min(1, (now - t0) / FADE_MS);
}

// lerpPos blends an entity's position across the span; entities that just
// entered view have no "from" and simply appear at their current spot.
function lerpPos(from, e, t) {
  const prev = from.get(e.id);
  if (!prev) return e.pos;
  return {
    x: prev.pos.x + (e.pos.x - prev.pos.x) * t,
    y: prev.pos.y + (e.pos.y - prev.pos.y) * t,
  };
}

function render() {
  ctx.fillStyle = "#0b0b10";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  const s = span();
  if (s) {
    const now = performance.now();
    renderClock = now + clockOffset - interpDelay; // delayed server-timeline clock
    const self = s.to.actors.get(myId);
    if (self) {
      const p = lerpPos(s.from.actors, self, s.t);
      cam.x = toUnits(p.x);
      cam.y = toUnits(p.y);
    }
    if (now < shakeUntil) {
      const k = (SHAKE_PX * (shakeUntil - now)) / SHAKE_MS / SCALE;
      cam.x += (Math.random() * 2 - 1) * k;
      cam.y += (Math.random() * 2 - 1) * k;
    }
    drawTerrain();
    drawStairs(now);
    drawPortal(now);
    drawTelegraphs(s);
    // Fade-out ghosts go under live entities; a ghost whose id reappears
    // (re-entered interest range) yields to the live drawing immediately.
    for (let i = ghosts.length - 1; i >= 0; i--) {
      const g = ghosts[i];
      const a = (g.until - now) / FADE_MS;
      if (a <= 0 || s.to[g.coll].has(g.e.id)) {
        ghosts.splice(i, 1);
        continue;
      }
      ctx.globalAlpha = a;
      if (g.coll === "actors") drawActor(g.e, g.e.pos);
      else drawDrop(g.e);
    }
    for (const d of s.to.drops.values()) {
      ctx.globalAlpha = alphaFor(d.id, now);
      drawDrop(d);
    }
    for (const a of s.to.actors.values()) {
      ctx.globalAlpha = alphaFor(a.id, now);
      drawActor(a, lerpPos(s.from.actors, a, s.t));
    }
    ctx.globalAlpha = 1;
    for (const p of s.to.projectiles.values()) drawProjectile(p, lerpPos(s.from.projectiles, p, s.t));

    drawMinimap(s);
    drawBossBar(s);

    // Client VFX run on the same delayed server-timeline clock as span();
    // an effect whose moment hasn't been rendered yet (t < 0) just waits.
    const rt = renderClock;
    for (let i = effects.length - 1; i >= 0; i--) {
      const e = effects[i];
      const t = (rt - e.st) / e.dur;
      if (t >= 1) {
        effects.splice(i, 1);
        continue;
      }
      if (t > 0) e.draw(t);
    }
    ctx.globalAlpha = 1;
  }
  requestAnimationFrame(render);
}

// --- telegraphs: the danger zones staged skills (and nova wind-ups) mark
// on the ground. The server sends center/radius plus a tick countdown; the
// fill sweeps up as the impact approaches, so "leave the circle" reads at a
// glance. Wind-ups whose total isn't on the wire (legacy novas) infer it
// from the largest countdown seen.

const telegraphMax = new Map(); // actor id → inferred countdown total

function drawTelegraphs(s) {
  for (const a of s.to.actors.values()) {
    const tg = a.telegraph;
    if (!tg) {
      telegraphMax.delete(a.id);
      continue;
    }
    let total = tg.total;
    if (!total) {
      total = Math.max(telegraphMax.get(a.id) || 0, tg.left);
      telegraphMax.set(a.id, total);
    }
    // Impact lands at server-timeline (view tick + ticks left); progress
    // runs 0→1 against the delayed render clock, so the sweep is smooth
    // between views.
    const impactSt = (s.to.tick + tg.left) * tickMs;
    const progress = Math.min(1, Math.max(0, 1 - (impactSt - renderClock) / (total * tickMs)));
    const p = worldToScreen(tg.x, tg.y);
    const r = toUnits(tg.radius) * SCALE;
    const hostile = a.team !== 1;
    const rim = hostile ? "#d84a3a" : "#6fa8e8";
    const fill = hostile ? "216,74,58" : "111,168,232";
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(${fill},${0.08 + 0.06 * progress})`;
    ctx.fill();
    ctx.lineWidth = 1.5;
    ctx.strokeStyle = rim + "aa";
    ctx.stroke();
    ctx.beginPath(); // the sweep: an inner disc growing to the rim
    ctx.arc(p.x, p.y, r * progress, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(${fill},${0.16 + 0.18 * progress})`;
    ctx.fill();
  }
}

// --- boss bar: any actor whose def is a named boss gets the big top-center
// life bar while it's on screen — the fight should feel like an occasion,
// so the bar appears when the boss does, not from across the floor.

const BOSS_DEFS = { barrow_king: "The Barrow King" };

function drawBossBar(s) {
  for (const a of s.to.actors.values()) {
    const name = BOSS_DEFS[a.def];
    if (!name || a.life <= 0) continue;
    const p = worldToScreen(a.pos.x, a.pos.y);
    const margin = 60;
    if (p.x < -margin || p.x > canvas.width + margin || p.y < -margin || p.y > canvas.height + margin) continue;
    const w = Math.min(canvas.width * 0.5, 520), h = 13;
    const x = (canvas.width - w) / 2, y = 58; // below the run HUD line
    ctx.fillStyle = "#000000b0";
    ctx.fillRect(x - 3, y - 3, w + 6, h + 6);
    const frac = a.max_life > 0 ? Math.max(0, a.life / a.max_life) : 0;
    const grad = ctx.createLinearGradient(x, y, x, y + h);
    grad.addColorStop(0, "#c23b2e");
    grad.addColorStop(1, "#711c13");
    ctx.fillStyle = grad;
    ctx.fillRect(x, y, w * frac, h);
    ctx.strokeStyle = "#c9a86acc";
    ctx.lineWidth = 1.5;
    ctx.strokeRect(x - 3.5, y - 3.5, w + 7, h + 7);
    ctx.fillStyle = "#e8dcc0";
    ctx.font = "14px Georgia";
    ctx.textAlign = "center";
    const label = a.mods && a.mods.length ? `${name} — ${a.mods.join(", ")}` : name;
    ctx.fillText(label, canvas.width / 2, y - 9);
    break; // one bar is plenty; two kings on screen is a future problem
  }
}

// drawTerrain paints the map when the welcome delivered one (floor tiles +
// walls, only the visible range), and falls back to the open-plane
// reference grid otherwise. Outside the map is void — the background color.
function drawTerrain() {
  if (!worldMap) {
    drawGrid();
    return;
  }
  const t = worldMap.tile / 1000; // tile edge in world units
  const x0 = Math.max(0, Math.floor((cam.x - canvas.width / 2 / SCALE) / t));
  const x1 = Math.min(worldMap.w - 1, Math.ceil((cam.x + canvas.width / 2 / SCALE) / t));
  const y0 = Math.max(0, Math.floor((cam.y - canvas.height / 2 / SCALE) / t));
  const y1 = Math.min(worldMap.h - 1, Math.ceil((cam.y + canvas.height / 2 / SCALE) / t));
  const px = (wx) => (wx - cam.x) * SCALE + canvas.width / 2;
  const py = (wy) => (wy - cam.y) * SCALE + canvas.height / 2;
  const ts = t * SCALE;

  for (let y = y0; y <= y1; y++) {
    const row = worldMap.rows[y];
    for (let x = x0; x <= x1; x++) {
      if (row[x] === "#") continue;
      ctx.fillStyle = "#14141d";
      ctx.fillRect(px(x * t), py(y * t), ts + 1, ts + 1);
    }
  }
  // Walls second, with a lit top edge so rooms read as sunken.
  for (let y = y0; y <= y1; y++) {
    const row = worldMap.rows[y];
    for (let x = x0; x <= x1; x++) {
      if (row[x] !== "#") continue;
      // Skip walls buried inside other walls — only faces near floor matter.
      const nearFloor =
        (x > 0 && row[x - 1] === ".") || (x < worldMap.w - 1 && row[x + 1] === ".") ||
        (y > 0 && worldMap.rows[y - 1][x] === ".") ||
        (y < worldMap.h - 1 && worldMap.rows[y + 1][x] === ".");
      if (!nearFloor) continue;
      ctx.fillStyle = "#2b2b3a";
      ctx.fillRect(px(x * t), py(y * t), ts + 1, ts + 1);
      ctx.fillStyle = "#3a3a4e";
      ctx.fillRect(px(x * t), py(y * t), ts + 1, 3);
    }
  }
  // Subtle floor grid to keep the movement reference the open plane had.
  ctx.strokeStyle = "#1b1b26";
  ctx.lineWidth = 1;
  ctx.beginPath();
  for (let x = x0; x <= x1 + 1; x++) {
    ctx.moveTo(px(x * t), py(y0 * t));
    ctx.lineTo(px(x * t), py((y1 + 1) * t));
  }
  for (let y = y0; y <= y1 + 1; y++) {
    ctx.moveTo(px(x0 * t), py(y * t));
    ctx.lineTo(px((x1 + 1) * t), py(y * t));
  }
  ctx.stroke();
}

// drawStairs paints the descent stairs (from the welcome — they never move
// within a floor): a shrinking stack of steps sinking into the dark.
function drawStairs(now) {
  if (!stairs) return;
  const p = worldToScreen(stairs.x, stairs.y);
  const s = SCALE * 0.45;
  ctx.save();
  ctx.translate(p.x, p.y);
  ctx.fillStyle = "#0b0b10";
  ctx.fillRect(-s, -s, 2 * s, 2 * s);
  ctx.strokeStyle = "#b8a44a";
  ctx.lineWidth = 1.5;
  ctx.strokeRect(-s, -s, 2 * s, 2 * s);
  ctx.fillStyle = "#3a3a4e";
  for (let i = 0; i < 4; i++) {
    const k = 1 - i * 0.22;
    ctx.fillRect(-s * k, s * (1 - k) - s * 0.15, 2 * s * k, s * 0.24);
  }
  ctx.restore();
  ctx.fillStyle = "#b8a44a";
  ctx.font = "11px Georgia";
  ctx.textAlign = "center";
  ctx.fillText("stairs down", p.x, p.y - s - 6);
}

// drawPortal paints the run's portal when it stands on this world: a slow
// two-arc swirl. In the hideout it is the way back; in the dungeon it is
// the death anchor and the door home.
function drawPortal(now) {
  const portal = runState && runState.portal;
  if (!portal) return;
  const p = worldToScreen(portal.x, portal.y);
  const r = SCALE * 0.55;
  const spin = now / 900;
  ctx.save();
  ctx.strokeStyle = "#7fd4ff";
  ctx.lineWidth = 2.5;
  for (let i = 0; i < 2; i++) {
    const a0 = spin + i * Math.PI;
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, a0, a0 + Math.PI * 0.7);
    ctx.stroke();
  }
  ctx.strokeStyle = "#bfeaff";
  ctx.lineWidth = 1.5;
  for (let i = 0; i < 2; i++) {
    const a0 = -spin * 1.4 + i * Math.PI;
    ctx.beginPath();
    ctx.arc(p.x, p.y, r * 0.62, a0, a0 + Math.PI * 0.6);
    ctx.stroke();
  }
  ctx.restore();
  ctx.fillStyle = "#7fd4ff";
  ctx.font = "11px Georgia";
  ctx.textAlign = "center";
  ctx.fillText("portal", p.x, p.y - r - 6);
}

function drawGrid() {
  ctx.strokeStyle = "#15151d";
  ctx.lineWidth = 1;
  const startX = Math.floor(cam.x - canvas.width / 2 / SCALE);
  const endX = Math.ceil(cam.x + canvas.width / 2 / SCALE);
  const startY = Math.floor(cam.y - canvas.height / 2 / SCALE);
  const endY = Math.ceil(cam.y + canvas.height / 2 / SCALE);
  ctx.beginPath();
  for (let x = startX; x <= endX; x++) {
    const px = (x - cam.x) * SCALE + canvas.width / 2;
    ctx.moveTo(px, 0); ctx.lineTo(px, canvas.height);
  }
  for (let y = startY; y <= endY; y++) {
    const py = (y - cam.y) * SCALE + canvas.height / 2;
    ctx.moveTo(0, py); ctx.lineTo(canvas.width, py);
  }
  ctx.stroke();
}

// --- actor models -----------------------------------------------------
//
// Actors are tiny hand-drawn vector bodies instead of flat circles: a
// shaded sphere, a facing derived from motion, and one def-specific
// accessory each so archetypes read across a room. All painters draw
// around (0,0) after the body sphere; ex/ey is the facing unit vector.

const actorMotion = new Map(); // id → { x, y, h } last pos + heading

// facingOf tracks per-actor heading from frame-to-frame motion; idle
// actors keep their last heading, newcomers face the camera.
function facingOf(id, pos) {
  const m = actorMotion.get(id);
  let h = m ? m.h : Math.PI / 2;
  let moving = false;
  if (m) {
    const dx = pos.x - m.x, dy = pos.y - m.y;
    if (dx * dx + dy * dy > 0.0004) {
      h = Math.atan2(dy, dx);
      moving = true;
    }
  }
  actorMotion.set(id, { x: pos.x, y: pos.y, h });
  return { h, moving };
}

// shade lightens (amt > 0) or darkens (amt < 0) a #rrggbb color.
function shade(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  const t = amt < 0 ? 0 : 255, f = Math.abs(amt);
  const ch = (v) => Math.round(v + (t - v) * f);
  return `rgb(${ch((n >> 16) & 255)},${ch((n >> 8) & 255)},${ch(n & 255)})`;
}

// bodySphere fills a lit-from-above sphere — the shared torso.
function bodySphere(r, color, squashX = 1) {
  const g = ctx.createRadialGradient(-r * 0.35, -r * 0.4, r * 0.15, 0, 0, r);
  g.addColorStop(0, shade(color, 0.35));
  g.addColorStop(0.65, color);
  g.addColorStop(1, shade(color, -0.4));
  ctx.beginPath();
  ctx.ellipse(0, 0, r * squashX, r, 0, 0, Math.PI * 2);
  ctx.fillStyle = g;
  ctx.fill();
}

function eyes(r, ex, ey, color) {
  const px = -ey, py = ex; // perpendicular
  ctx.fillStyle = color;
  for (const side of [-1, 1]) {
    ctx.beginPath();
    ctx.arc(ex * r * 0.45 + px * side * r * 0.22, ey * r * 0.45 + py * side * r * 0.22 - r * 0.15, Math.max(r * 0.09, 1.2), 0, Math.PI * 2);
    ctx.fill();
  }
}

// One painter per archetype; fallback is a plain shaded sphere with eyes.
const MODEL_PAINTERS = {
  zombie(r, ex, ey) {
    const px = -ey, py = ex;
    bodySphere(r, "#7a2424");
    ctx.fillStyle = shade("#7a2424", -0.15); // arms shamble out front
    for (const side of [-1, 1]) {
      ctx.beginPath();
      ctx.arc(ex * r * 0.85 + px * side * r * 0.5, ey * r * 0.85 + py * side * r * 0.5, r * 0.28, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.strokeStyle = "#00000055"; // a stitched gash
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(-r * 0.5, r * 0.1);
    ctx.lineTo(r * 0.2, r * 0.45);
    ctx.stroke();
    eyes(r, ex, ey, "#e8d86a");
  },
  ghoul(r, ex, ey) {
    const px = -ey, py = ex;
    bodySphere(r, "#5f7a2e", 0.82);
    ctx.fillStyle = shade("#5f7a2e", 0.2); // claw spikes
    for (const side of [-1, 1]) {
      for (let i = 0; i < 3; i++) {
        const bx = ex * r * (0.45 + i * 0.18) + px * side * r * 0.62;
        const by = ey * r * (0.45 + i * 0.18) + py * side * r * 0.62;
        ctx.beginPath();
        ctx.moveTo(bx, by);
        ctx.lineTo(bx + ex * r * 0.3 + px * side * r * 0.18, by + ey * r * 0.3 + py * side * r * 0.18);
        ctx.lineTo(bx + ex * r * 0.05 + px * side * r * 0.3, by + ey * r * 0.05 + py * side * r * 0.3);
        ctx.fill();
      }
    }
    eyes(r, ex, ey, "#ff6a4a");
  },
  skeleton_archer(r, ex, ey) {
    bodySphere(r, "#6e6a58");
    const bx = ex * r * 0.75, by = ey * r * 0.75, h = Math.atan2(ey, ex);
    ctx.strokeStyle = "#c9b98a"; // the bow, held out front
    ctx.lineWidth = Math.max(r * 0.12, 1.5);
    ctx.beginPath();
    ctx.arc(bx, by, r * 0.75, h - 1.15, h + 1.15);
    ctx.stroke();
    ctx.lineWidth = 1; // the string
    ctx.strokeStyle = "#e8e0cc99";
    ctx.beginPath();
    ctx.moveTo(bx + Math.cos(h - 1.15) * r * 0.75, by + Math.sin(h - 1.15) * r * 0.75);
    ctx.lineTo(bx + Math.cos(h + 1.15) * r * 0.75, by + Math.sin(h + 1.15) * r * 0.75);
    ctx.stroke();
    eyes(r, ex, ey, "#2a2a30");
  },
  skeleton_mage(r, ex, ey) {
    const px = -ey, py = ex;
    bodySphere(r, "#5a4a8e");
    ctx.fillStyle = shade("#5a4a8e", -0.25); // the hood, always peaked
    ctx.beginPath();
    ctx.moveTo(-r * 0.55, -r * 0.3);
    ctx.lineTo(r * 0.55, -r * 0.3);
    ctx.lineTo(0, -r * 1.3);
    ctx.fill();
    const sx = px * r * 0.85 + ex * r * 0.3, sy = py * r * 0.85 + ey * r * 0.3;
    ctx.strokeStyle = "#3c3448"; // staff with a charged tip
    ctx.lineWidth = Math.max(r * 0.12, 1.5);
    ctx.beginPath();
    ctx.moveTo(sx - ex * r * 0.4, sy - ey * r * 0.4);
    ctx.lineTo(sx + ex * r * 0.5, sy + ey * r * 0.5);
    ctx.stroke();
    ctx.beginPath();
    ctx.arc(sx + ex * r * 0.55, sy + ey * r * 0.55, r * 0.22, 0, Math.PI * 2);
    ctx.fillStyle = "#8f6ff0";
    ctx.fill();
    eyes(r, ex, ey, "#b09fff");
  },
  bone_colossus(r, ex, ey) {
    bodySphere(r, "#a89c82");
    ctx.strokeStyle = "#00000033"; // ribs
    ctx.lineWidth = Math.max(r * 0.08, 1);
    for (let i = -1; i <= 1; i++) {
      ctx.beginPath();
      ctx.arc(0, r * 0.2 + i * r * 0.3, r * 0.7, Math.PI * 0.15, Math.PI * 0.85);
      ctx.stroke();
    }
    ctx.fillStyle = "#e8dfc8"; // horns
    for (const side of [-1, 1]) {
      ctx.beginPath();
      ctx.moveTo(side * r * 0.45, -r * 0.55);
      ctx.lineTo(side * r * 0.95, -r * 1.15);
      ctx.lineTo(side * r * 0.75, -r * 0.35);
      ctx.fill();
    }
    eyes(r, ex, ey, "#8a1c1c");
  },
  barrow_king(r, ex, ey) {
    bodySphere(r, "#6b5a7e");
    ctx.strokeStyle = "#00000040"; // burial wrappings
    ctx.lineWidth = Math.max(r * 0.09, 1);
    for (let i = -1; i <= 1; i++) {
      ctx.beginPath();
      ctx.arc(0, r * 0.15 + i * r * 0.32, r * 0.72, Math.PI * 0.1, Math.PI * 0.9);
      ctx.stroke();
    }
    ctx.fillStyle = "#d8c66a"; // the crown
    ctx.beginPath();
    ctx.moveTo(-r * 0.6, -r * 0.55);
    for (const [px2, py2] of [[-0.6, -1.0], [-0.3, -0.7], [0, -1.15], [0.3, -0.7], [0.6, -1.0], [0.6, -0.55]]) {
      ctx.lineTo(px2 * r, py2 * r);
    }
    ctx.closePath();
    ctx.fill();
    ctx.strokeStyle = "#00000055";
    ctx.lineWidth = 1;
    ctx.stroke();
    eyes(r, ex, ey, "#7fe8d4");
  },
  training_dummy(r) {
    bodySphere(r, "#b89a5a");
    ctx.strokeStyle = "#6e5432"; // post and crossbar
    ctx.lineWidth = Math.max(r * 0.16, 2);
    ctx.beginPath();
    ctx.moveTo(0, -r * 0.9);
    ctx.lineTo(0, r);
    ctx.moveTo(-r * 0.85, -r * 0.25);
    ctx.lineTo(r * 0.85, -r * 0.25);
    ctx.stroke();
  },
};

function paintPlayer(r, ex, ey, isMe, named) {
  const px = -ey, py = ex;
  bodySphere(r, isMe ? "#3d6fd1" : named ? "#3562b8" : "#2a4fa3");
  const sx = px * r * 0.8, sy = py * r * 0.8;
  ctx.strokeStyle = "#d8d8e0"; // sword at the ready
  ctx.lineWidth = Math.max(r * 0.14, 1.5);
  ctx.beginPath();
  ctx.moveTo(sx, sy);
  ctx.lineTo(sx + ex * r * 0.95, sy + ey * r * 0.95);
  ctx.stroke();
  ctx.beginPath(); // head, leaning into the facing
  ctx.arc(ex * r * 0.15, ey * r * 0.15 - r * 0.35, r * 0.42, 0, Math.PI * 2);
  ctx.fillStyle = "#d8b58f";
  ctx.fill();
  ctx.strokeStyle = "#00000044";
  ctx.lineWidth = 1;
  ctx.stroke();
}

function drawActor(a, pos) {
  const p = worldToScreen(pos.x, pos.y);
  const r = toUnits(a.radius) * SCALE;
  const isMe = a.id === myId;

  const rarityColor = RARITY_COLORS[a.rarity];
  const { h, moving } = facingOf(a.id, pos);
  const ex = Math.cos(h), ey = Math.sin(h);
  const bob = moving ? Math.sin(renderClock / 90 + a.id * 1.7) * r * 0.07 : 0;

  ctx.beginPath(); // grounding shadow
  ctx.ellipse(p.x, p.y + r * 0.75, r * 0.8, r * 0.28, 0, 0, Math.PI * 2);
  ctx.fillStyle = "#00000059";
  ctx.fill();

  ctx.save();
  ctx.translate(p.x, p.y + bob);
  if (a.team === 1) {
    paintPlayer(r, ex, ey, isMe, roster.has(a.id));
  } else {
    const painter = MODEL_PAINTERS[a.def];
    if (painter) painter(r, ex, ey);
    else {
      bodySphere(r, DEF_COLORS[a.def] || "#7a2424");
      eyes(r, ex, ey, "#e8d86a");
    }
  }
  if (rarityColor || isMe) {
    ctx.beginPath(); // rarity/self ring hugs the body
    ctx.arc(0, 0, r, 0, Math.PI * 2);
    ctx.lineWidth = rarityColor ? 2.5 : 1.5;
    ctx.strokeStyle = rarityColor || "#cfc9bf88";
    ctx.stroke();
  }
  ctx.restore();

  // hit flash: a white pulse that decays over FLASH_MS
  const flashUntil = flashes.get(a.id);
  if (flashUntil && renderClock < flashUntil) {
    const prev = ctx.globalAlpha;
    ctx.globalAlpha = prev * 0.8 * ((flashUntil - renderClock) / FLASH_MS);
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
    ctx.fillStyle = "#ffffff";
    ctx.fill();
    ctx.globalAlpha = prev;
  }

  // casting telegraph: a thin arc while winding up
  if (a.action.startsWith("windup")) {
    ctx.beginPath();
    ctx.arc(p.x, p.y, r + 4, 0, Math.PI * 2);
    ctx.strokeStyle = "#b8a44a";
    ctx.lineWidth = 1.5;
    ctx.stroke();
  }

  // ailment rings: ignite/chill/shock from the ail bitmask, stacked outward
  let ringR = r + 7;
  for (const [bit, color] of AILMENT_RINGS) {
    if (!(a.ail & bit)) continue;
    ctx.beginPath();
    ctx.arc(p.x, p.y, ringR, 0, Math.PI * 2);
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.stroke();
    ringR += 3;
  }

  // health bar
  const w = Math.max(r * 2, 28);
  const frac = a.max_life > 0 ? a.life / a.max_life : 0;
  ctx.fillStyle = "#000000aa";
  ctx.fillRect(p.x - w / 2, p.y - r - 11, w, 5);
  ctx.fillStyle = a.team === 1 ? "#3da14b" : "#a32626";
  ctx.fillRect(p.x - w / 2, p.y - r - 11, w * Math.max(0, frac), 5);

  // Named players read brighter than the anonymous rabble.
  ctx.fillStyle = rarityColor || (roster.has(a.id) ? "#dcd6c8" : "#8d8678");
  ctx.font = "11px Georgia";
  ctx.textAlign = "center";
  ctx.fillText(names.get(a.id) || a.def, p.x, p.y - r - 16);
}

// PoE-flavored rarity colors: magic blue, rare yellow.
const RARITY_COLORS = { magic: "#7a9bf0", rare: "#f0d060" };

// Fallback body tints for defs without a MODEL_PAINTERS entry; anything
// unlisted renders as a generic monster-red sphere.
const DEF_COLORS = {
  ghoul: "#5f7a2e",
  skeleton_mage: "#5a4a8e",
  skeleton_archer: "#6e6a58",
  bone_colossus: "#a89c82",
  barrow_king: "#6b5a7e",
};

const AILMENT_RINGS = [
  [1, "#e67e22cc"], // ignited
  [2, "#7fd4ffcc"], // chilled
  [4, "#f5e25fcc"], // shocked
  [8, "#9fff9fcc"], // buffed
];

const PROJ_COLORS = {
  fireball: ["#ffd27d", "#d35400"],
  spark: ["#ffffff", "#5fa8f5"],
  bone_arrow: ["#f2ead8", "#8d8678"],
  arc_bolt: ["#e8e0ff", "#8f6ff0"],
  bone_volley: ["#f2ead8", "#6e5f48"],
  grave_volley: ["#e8d8f2", "#5a4468"],
  grave_storm: ["#f2e0c8", "#7e3a2e"],
};

function drawProjectile(p, pos) {
  const s = worldToScreen(pos.x, pos.y);
  const r = Math.max(toUnits(p.radius) * SCALE, 4);
  const [inner, outer] = PROJ_COLORS[p.skill] || PROJ_COLORS.fireball;
  const grad = ctx.createRadialGradient(s.x, s.y, 1, s.x, s.y, r);
  grad.addColorStop(0, inner);
  grad.addColorStop(1, outer);
  ctx.beginPath();
  ctx.arc(s.x, s.y, r, 0, Math.PI * 2);
  ctx.fillStyle = grad;
  ctx.fill();
}

const DROP_RARITY_COLORS = { normal: "#cfc9bf", magic: "#8888ff", rare: "#ffff77", unique: "#ff9a3d" };
// Uncut gems get their own drop identity: teal for skills, violet for
// supports — a gem on the floor should read as an event, like a rare.
const GEM_DROP_COLORS = { skill: "#4ad1c8", support: "#c67fe8" };

function dropColor(item) {
  if (item.gem) return item.gem.support ? GEM_DROP_COLORS.support : GEM_DROP_COLORS.skill;
  return DROP_RARITY_COLORS[item.rarity] || DROP_RARITY_COLORS.normal;
}

function dropLabel(item) {
  if (item.gem) {
    return item.gem.support ? "uncut support gem" : `uncut skill gem (${item.gem.level})`;
  }
  return prettify(item.base);
}

function drawDrop(d) {
  const p = worldToScreen(d.pos.x, d.pos.y);
  const color = dropColor(d.item);
  const shafted = d.item.gem || d.item.rarity === "magic" || d.item.rarity === "rare";

  // Magic/rare/gem drops throw a pulsing light shaft, readable across a
  // room — a rare monster's triple drop should look like an event on the
  // floor, and an uncut gem is one.
  if (shafted) {
    const pulse = 0.55 + 0.25 * Math.sin(renderClock / 280 + d.id);
    const h = d.item.rarity === "rare" || d.item.gem ? 52 : 38;
    const grad = ctx.createLinearGradient(p.x, p.y, p.x, p.y - h);
    grad.addColorStop(0, color + "cc");
    grad.addColorStop(1, color + "00");
    const prev = ctx.globalAlpha;
    ctx.globalAlpha = prev * pulse;
    ctx.fillStyle = grad;
    ctx.fillRect(p.x - 3, p.y - h, 6, h);
    ctx.globalAlpha = prev;
  }

  ctx.save();
  ctx.translate(p.x, p.y);
  ctx.rotate(Math.PI / 4);
  ctx.fillStyle = color;
  ctx.fillRect(-6, -6, 12, 12);
  ctx.strokeStyle = "#000000aa";
  ctx.strokeRect(-6, -6, 12, 12);
  ctx.restore();
  ctx.fillStyle = !d.item.gem && d.item.rarity === "normal" ? "#b8a44a" : color;
  ctx.font = "11px Georgia";
  ctx.textAlign = "center";
  ctx.fillText(dropLabel(d.item), p.x, p.y - 12);
}

// ------------------------------------------------------------- minimap
//
// A radar in the corner: terrain baked once per welcome onto an offscreen
// canvas, live dots blitted over it each frame. Entities appear only while
// in interest range — the radar shows what you sense, stairs and the
// portal are floor knowledge and always show.

const MM_TILE = 3; // px per tile
const mmCanvas = document.getElementById("minimap");
const mmCtx = mmCanvas.getContext("2d");
let mmBase = null;

function buildMinimapBase() {
  if (!worldMap) {
    mmBase = null;
    mmCanvas.classList.add("hidden");
    return;
  }
  mmCanvas.classList.remove("hidden");
  mmCanvas.width = worldMap.w * MM_TILE;
  mmCanvas.height = worldMap.h * MM_TILE;
  mmBase = document.createElement("canvas");
  mmBase.width = mmCanvas.width;
  mmBase.height = mmCanvas.height;
  const b = mmBase.getContext("2d");
  for (let y = 0; y < worldMap.h; y++) {
    const row = worldMap.rows[y];
    for (let x = 0; x < worldMap.w; x++) {
      b.fillStyle = row[x] === "#" ? "#1c1c26" : "#3a3a4e";
      b.fillRect(x * MM_TILE, y * MM_TILE, MM_TILE, MM_TILE);
    }
  }
}

function mmDot(pos, color, r) {
  const t = worldMap.tile; // milli-units per tile
  mmCtx.fillStyle = color;
  mmCtx.beginPath();
  mmCtx.arc((pos.x / t) * MM_TILE, (pos.y / t) * MM_TILE, r, 0, Math.PI * 2);
  mmCtx.fill();
}

function drawMinimap(s) {
  if (!mmBase || !worldMap) return;
  mmCtx.drawImage(mmBase, 0, 0);
  if (stairs) mmDot(stairs, "#f0d060", 3.5);
  const portal = runState && runState.portal;
  if (portal) mmDot(portal, "#7fd4ff", 3);
  for (const a of s.to.actors.values()) {
    if (a.id === myId) continue;
    mmDot(a.pos, a.team === 1 ? "#4a7ad1" : RARITY_COLORS[a.rarity] || "#a33030", 2);
  }
  const self = s.to.actors.get(myId);
  if (self) mmDot(self.pos, "#efe9dc", 2.5);
}

// -------------------------------------------------------------- audio
//
// Procedural stingers — a tiny WebAudio synth, no asset files. Every cue
// is an oscillator envelope; kinds are throttled so a nova hitting six
// zombies reads as one crunch, not a machine gun. M mutes (persisted).

let audioCtx = null;
let audioMuted = localStorage.getItem("df-muted") === "1";
const sfxLast = new Map(); // kind -> last play time (ms)

function audioUnlock() {
  if (!audioCtx) {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    audioCtx = new AC();
  }
  if (audioCtx.state === "suspended") audioCtx.resume();
}

// blip: one enveloped oscillator. freq can be [start, end] for a sweep.
function blip(freq, dur, type, gain, delay = 0) {
  if (!audioCtx || audioMuted) return;
  const t0 = audioCtx.currentTime + delay;
  const osc = audioCtx.createOscillator();
  const env = audioCtx.createGain();
  osc.type = type;
  const [f0, f1] = Array.isArray(freq) ? freq : [freq, freq];
  osc.frequency.setValueAtTime(f0, t0);
  if (f1 !== f0) osc.frequency.exponentialRampToValueAtTime(Math.max(1, f1), t0 + dur);
  env.gain.setValueAtTime(0, t0);
  env.gain.linearRampToValueAtTime(gain * 0.15, t0 + 0.005); // master 0.15
  env.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
  osc.connect(env).connect(audioCtx.destination);
  osc.start(t0);
  osc.stop(t0 + dur + 0.05);
}

// sfx: named cues with per-kind throttles.
const SFX = {
  hit: { min: 90, play: () => blip([180, 70], 0.08, "square", 0.5) },
  hit_me: { min: 120, play: () => blip([120, 45], 0.12, "square", 0.9) },
  crit: { min: 120, play: () => { blip([500, 900], 0.1, "sawtooth", 0.6); blip([180, 70], 0.08, "square", 0.5); } },
  death: { min: 100, play: () => blip([260, 40], 0.22, "sawtooth", 0.7) },
  death_rare: { min: 100, play: () => { blip([320, 50], 0.3, "sawtooth", 0.9); blip([160, 30], 0.35, "square", 0.7, 0.08); } },
  level_up: { min: 500, play: () => [440, 554, 659, 880].forEach((f, i) => blip(f, 0.14, "triangle", 0.8, i * 0.07)) },
  orb: { min: 200, play: () => { blip(1180, 0.06, "triangle", 0.7); blip(1760, 0.1, "triangle", 0.6, 0.05); } },
  drop_good: { min: 250, play: () => blip([880, 1320], 0.12, "triangle", 0.5) },
  flask: { min: 250, play: () => { blip([220, 140], 0.07, "sine", 0.9); blip([180, 120], 0.07, "sine", 0.8, 0.08); } },
  travel: { min: 400, play: () => blip([200, 700], 0.35, "sine", 0.6) },
};

function sfx(kind) {
  const def = SFX[kind];
  if (!def || !audioCtx || audioMuted) return;
  const now = performance.now();
  if (now - (sfxLast.get(kind) || 0) < def.min) return;
  sfxLast.set(kind, now);
  def.play();
}

// Event-driven cues; called from the view handler's event loop.
function sfxForEvent(ev) {
  switch (ev.kind) {
    case "hit":
      if (ev.other === myId) sfx("hit_me");
      else if (ev.crit) sfx("crit");
      else sfx("hit");
      break;
    case "death": {
      const dier = snap && snap.actors.get(ev.actor);
      sfx(dier && dier.rarity ? "death_rare" : "death");
      break;
    }
    case "level_up":
      if (ev.actor === myId) sfx("level_up");
      break;
    case "orb":
    case "gem":
      if (ev.actor === myId) sfx("orb");
      break;
    case "drop":
      sfx("drop_good");
      break;
    case "buff":
      if (ev.other === myId && ev.note && ev.note.endsWith("_flask")) sfx("flask");
      break;
    case "descend":
    case "portal":
    case "death_eject":
      sfx("travel");
      break;
  }
}

// ----------------------------------------------------------- client VFX
//
// Ephemeral client-side-only effects: cast flashes and impact bursts.
// Spawned from view diffs and hit events, timestamped on the SERVER
// timeline (tick × tickMs) so they stay in sync with the interpolated
// entities they decorate — the renderer runs them through the same
// clockOffset − interpDelay clock as everything else.

const effects = []; // { st, dur, draw(t) } with t in [0,1]
const EFFECTS_MAX = 64;

function spawnEffect(st, dur, draw) {
  effects.push({ st, dur, draw });
  while (effects.length > EFFECTS_MAX) effects.shift();
}

const easeOut = (t) => 1 - (1 - t) * (1 - t);

// Cast effects key off the windup→done action transition between
// consecutive views, not off hit events — a nova that hits nothing still
// reads as a cast. Windups are several views long, so the transition is
// always observed.
const CAST_VFX = {
  frost_nova: spawnNova,
  zombie_slam: spawnSlam,
};

function diffCastVFX(prev, view) {
  const st = view.tick * tickMs;
  for (const [id, a] of view.actors) {
    const before = prev.actors.get(id);
    if (!before || !before.action.startsWith("windup:")) continue;
    if (a.action === before.action) continue;
    const fx = CAST_VFX[before.action.slice(7)];
    if (fx) fx(a.pos, st);
  }
}

// Frost nova: a shard-spoked ring expanding out to the skill's real 4-unit
// blast radius, so the visual is also the hitbox telegraph.
function spawnNova(pos, st) {
  spawnEffect(st, 500, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const r = (0.5 + 3.5 * easeOut(t)) * SCALE;
    ctx.globalAlpha = 1 - t;
    ctx.strokeStyle = "#bfeaff";
    ctx.lineWidth = 3;
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
    ctx.stroke();
    ctx.strokeStyle = "#7fd4ff";
    ctx.lineWidth = 1.5;
    ctx.beginPath();
    ctx.arc(p.x, p.y, r * 0.72, 0, Math.PI * 2);
    ctx.stroke();
    ctx.beginPath();
    for (let i = 0; i < 8; i++) {
      const ang = (Math.PI / 4) * i + 0.4 * t;
      ctx.moveTo(p.x + Math.cos(ang) * r * 0.85, p.y + Math.sin(ang) * r * 0.85);
      ctx.lineTo(p.x + Math.cos(ang) * r * 1.12, p.y + Math.sin(ang) * r * 1.12);
    }
    ctx.stroke();
  });
}

// Zombie slam: a jagged ground-crack star under the zombie, shaped once
// per slam so each smash cracks differently.
function spawnSlam(pos, st) {
  const n = 12;
  const jags = [];
  for (let i = 0; i < n; i++) {
    jags.push(i % 2 ? 0.45 + Math.random() * 0.2 : 0.9 + Math.random() * 0.25);
  }
  spawnEffect(st, 350, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const r = (0.6 + 1.3 * easeOut(t)) * SCALE;
    ctx.globalAlpha = 1 - t;
    ctx.beginPath();
    for (let i = 0; i <= n; i++) {
      const ang = (Math.PI * 2 * i) / n;
      const rr = r * jags[i % n];
      const x = p.x + Math.cos(ang) * rr;
      const y = p.y + Math.sin(ang) * rr;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.closePath();
    ctx.fillStyle = "#3a2a2066";
    ctx.fill();
    ctx.strokeStyle = "#c89b6a";
    ctx.lineWidth = 2;
    ctx.stroke();
  });
}

const IMPACT_VFX = {
  fireball: { core: "#ffd27d", glow: "#d35400", r: 0.9 },
  spark: { core: "#ffffff", glow: "#5fa8f5", r: 0.7 },
  frost_nova: { core: "#e8fbff", glow: "#7fd4ff", r: 0.6 },
  zombie_slam: { core: "#ffe8d0", glow: "#a32626", r: 0.7 },
  bone_arrow: { core: "#f2ead8", glow: "#8d8678", r: 0.6 },
  ghoul_claws: { core: "#ffe8d0", glow: "#5f7a2e", r: 0.5 },
  arc_bolt: { core: "#e8e0ff", glow: "#8f6ff0", r: 0.8 },
  arc: { core: "#ffffff", glow: "#8f6ff0", r: 0.8 },
  bone_volley: { core: "#f2ead8", glow: "#6e5f48", r: 0.9 },
  colossus_slam: { core: "#ffe8d0", glow: "#a89c82", r: 1.2 },
  barrow_slam: { core: "#f2e0ff", glow: "#6b5a7e", r: 1.3 },
  grave_volley: { core: "#e8d8f2", glow: "#5a4468", r: 0.8 },
  grave_storm: { core: "#f2e0c8", glow: "#7e3a2e", r: 0.8 },
};

// Skills whose projectile impacts detonate: radius mirrors the server's
// ExplodeRadius (display-only, like FLASK_MAX).
const EXPLODE_VFX = {
  fireball: { color: "#ff9a3d", inner: "#ffd27d", radius: 2 },
};

// Chain (hitscan) skills: hit events with these notes get a jagged bolt
// drawn caster → victim → victim instead of a flying projectile.
const CHAIN_VFX = {
  arc: { core: "#ffffff", glow: "#8f6ff0" },
};

// Explosion at a projectile impact: a fire flash expanding to the skill's
// real splash radius — the visual is also the falloff telegraph.
function spawnExplosion(pos, st, v) {
  spawnEffect(st, 320, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const r = (0.4 + (v.radius - 0.4) * easeOut(t)) * SCALE;
    ctx.globalAlpha = (1 - t) * 0.85;
    ctx.fillStyle = v.color;
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
    ctx.fill();
    ctx.globalAlpha = 1 - t;
    ctx.strokeStyle = v.inner;
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
    ctx.stroke();
    ctx.fillStyle = v.inner;
    ctx.beginPath();
    ctx.arc(p.x, p.y, Math.max(1, r * 0.35 * (1 - t)), 0, Math.PI * 2);
    ctx.fill();
  });
}

// Chain lightning: one jagged polyline through the strike positions,
// shaped once per bolt (in world units, so it stays pinned under camera
// movement) and redrawn fading with a white core over a colored glow.
function spawnChainLightning(points, st, v) {
  if (points.length < 2) return;
  const path = [];
  for (let i = 0; i < points.length - 1; i++) {
    const ax = toUnits(points[i].x), ay = toUnits(points[i].y);
    const bx = toUnits(points[i + 1].x), by = toUnits(points[i + 1].y);
    const segs = Math.max(2, Math.round(Math.hypot(bx - ax, by - ay) / 0.7));
    // Perpendicular jitter at each interior vertex makes the jag.
    const nx = -(by - ay), ny = bx - ax;
    const nl = Math.hypot(nx, ny) || 1;
    for (let s = 0; s <= segs; s++) {
      if (i > 0 && s === 0) continue; // shared vertex with the last segment
      const f = s / segs;
      const j = s === 0 || s === segs ? 0 : (Math.random() - 0.5) * 0.55;
      path.push({ x: ax + (bx - ax) * f + (nx / nl) * j, y: ay + (by - ay) * f + (ny / nl) * j });
    }
  }
  spawnEffect(st, 260, (t) => {
    ctx.globalAlpha = (1 - t) * (0.7 + 0.3 * Math.random()); // crackle
    ctx.lineJoin = "round";
    ctx.beginPath();
    for (let i = 0; i < path.length; i++) {
      const p = worldToScreen(toMilli(path[i].x), toMilli(path[i].y));
      if (i === 0) ctx.moveTo(p.x, p.y);
      else ctx.lineTo(p.x, p.y);
    }
    ctx.strokeStyle = v.glow;
    ctx.lineWidth = 4;
    ctx.stroke();
    ctx.strokeStyle = v.core;
    ctx.lineWidth = 1.5;
    ctx.stroke();
  });
}

// Impact burst at whoever got hit: a six-ray starburst in the skill's
// palette around a shrinking core.
function spawnImpact(pos, st, v) {
  const base = Math.random() * Math.PI;
  spawnEffect(st, 250, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const r = v.r * SCALE * (0.4 + 0.6 * easeOut(t));
    ctx.globalAlpha = 1 - t;
    ctx.strokeStyle = v.glow;
    ctx.lineWidth = 2;
    ctx.beginPath();
    for (let i = 0; i < 6; i++) {
      const ang = base + (Math.PI / 3) * i;
      ctx.moveTo(p.x + Math.cos(ang) * r * 0.35, p.y + Math.sin(ang) * r * 0.35);
      ctx.lineTo(p.x + Math.cos(ang) * r, p.y + Math.sin(ang) * r);
    }
    ctx.stroke();
    ctx.fillStyle = v.core;
    ctx.beginPath();
    ctx.arc(p.x, p.y, Math.max(1, r * 0.3 * (1 - t)), 0, Math.PI * 2);
    ctx.fill();
  });
}

// Camera shake when something connects with *you*.
let shakeUntil = 0;
const SHAKE_MS = 220;
const SHAKE_PX = 5;

// Hit flashes: entity id → server-time the white pulse ends. Consulted by
// drawActor against renderClock (set each frame from the interp clock).
const flashes = new Map();
const FLASH_MS = 130;
let renderClock = 0;

// Floating damage numbers: drift up and fade. Crits punch — bigger,
// golden, and they linger a beat longer; damage on *you* reads red.
function spawnDamageNumber(pos, st, amount, crit, onMe) {
  const jx = (Math.random() - 0.5) * 0.7; // de-stack simultaneous hits
  const text = fmtDamage(amount);
  spawnEffect(st, crit ? 900 : 650, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const rise = (crit ? 1.5 : 1.0) * easeOut(t) * SCALE;
    ctx.globalAlpha = t < 0.6 ? 1 : 1 - (t - 0.6) / 0.4;
    ctx.font = crit ? "bold 17px Georgia" : onMe ? "bold 13px Georgia" : "12px Georgia";
    ctx.textAlign = "center";
    const x = p.x + jx * SCALE;
    const y = p.y - 14 - rise;
    ctx.strokeStyle = "#000000cc";
    ctx.lineWidth = 3;
    ctx.strokeText(text, x, y);
    ctx.fillStyle = crit ? "#ffcf4d" : onMe ? "#ff7d6a" : "#f2ead8";
    ctx.fillText(text, x, y);
  });
}

// Milli-damage → display: whole numbers once they'd round cleanly, one
// decimal for chip damage so it doesn't read as "0".
function fmtDamage(amount) {
  const d = amount / 1000;
  return d >= 10 ? String(Math.round(d)) : d.toFixed(1);
}

// Drop landing: a ground ring where loot hits the floor — a blink for
// normal items, wider and rarity-colored for magic/rare.
function spawnDropLanding(pos, st, rarity) {
  const color = DROP_RARITY_COLORS[rarity] || DROP_RARITY_COLORS.normal;
  const big = rarity === "rare" ? 1.6 : rarity === "magic" ? 1.2 : 0.7;
  spawnEffect(st, rarity === "normal" ? 250 : 450, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const r = big * easeOut(t) * SCALE * 0.8;
    ctx.globalAlpha = (1 - t) * 0.8;
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.ellipse(p.x, p.y, r, r * 0.45, 0, 0, Math.PI * 2);
    ctx.stroke();
  });
}

// Death pop: a bursting ring plus shards where something died — larger
// and rarity-colored for magic/rare monsters, so a rare kill lands.
function spawnDeathPop(pos, st, dier) {
  const color = RARITY_COLORS[dier.rarity] || (dier.team === 1 ? "#7fa4d1" : "#c96a4a");
  const size = dier.rarity ? 1.6 : 1.0;
  const r0 = toUnits(dier.radius);
  const base = Math.random() * Math.PI;
  spawnEffect(st, dier.rarity ? 600 : 420, (t) => {
    const p = worldToScreen(pos.x, pos.y);
    const r = (r0 + 1.5 * size * easeOut(t)) * SCALE;
    ctx.globalAlpha = (1 - t) * 0.9;
    ctx.strokeStyle = color;
    ctx.lineWidth = 1 + 2.5 * (1 - t);
    ctx.beginPath();
    ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
    ctx.stroke();
    ctx.beginPath();
    for (let i = 0; i < 8; i++) {
      const ang = base + (Math.PI / 4) * i;
      ctx.moveTo(p.x + Math.cos(ang) * r * 0.7, p.y + Math.sin(ang) * r * 0.7);
      ctx.lineTo(p.x + Math.cos(ang) * r * 1.15, p.y + Math.sin(ang) * r * 1.15);
    }
    ctx.stroke();
  });
}

// --------------------------------------------------------------- input

canvas.addEventListener("mousemove", (e) => { mouse.x = e.offsetX; mouse.y = e.offsetY; });

canvas.addEventListener("mousedown", (e) => {
  audioUnlock();
  if (e.button !== 0 || !snap) return;
  const w = screenToWorldUnits(e.offsetX, e.offsetY);
  const clicked = (p, r) => p && Math.hypot(toUnits(p.x) - w.x, toUnits(p.y) - w.y) < r;
  pendingPickup = 0;
  pendingDescend = false;
  pendingPortal = false;
  let drop = null;
  for (const d of snap.drops.values()) {
    if (clicked(d.pos, 0.8)) { drop = d; break; }
  }
  if (drop) {
    pendingPickup = drop.id;
    send({ kind: "move", x: drop.pos.x, y: drop.pos.y });
  } else if (clicked(stairs, 1.0)) {
    pendingDescend = true;
    send({ kind: "move", x: stairs.x, y: stairs.y });
  } else if (clicked(runState && runState.portal, 1.0)) {
    pendingPortal = true;
    send({ kind: "move", x: runState.portal.x, y: runState.portal.y });
  } else {
    send({ kind: "move", x: toMilli(w.x), y: toMilli(w.y) });
  }
});

// The skill bar is driven by the actor's cut gems (up to four, in cut
// order) on keys Q/E/R/T. Mana costs arrive on the gem snap — the server
// computes them; the client never re-derives cost math. SKILL_META is
// display-only flavor: glyph color and whether the skill wants an aim
// point; unknown skills get a neutral aimed default.
const GEM_KEYS = ["q", "e", "r", "t"];
const SKILL_META = {
  fireball: { color: "#d35400", aimed: true, kind: "Projectile",
    desc: "Hurl a fireball that explodes on impact, splashing everything nearby. Can ignite." },
  frost_nova: { color: "#7fd4ff", aimed: false, kind: "Nova",
    desc: "A ring of cold erupts from you, chilling everything it touches." },
  spark: { color: "#5fa8f5", aimed: true, kind: "Projectile",
    desc: "Loose a wandering bolt that ricochets off walls until it finds flesh. Can shock." },
  adrenaline: { color: "#9fff9f", aimed: false, kind: "Buff",
    desc: "Surge with borrowed speed for a few seconds." },
  arc_bolt: { color: "#8f6ff0", aimed: true, kind: "Projectile" },
  arc: { color: "#8f6ff0", aimed: true, kind: "Chain",
    desc: "Lightning leaps to the enemy nearest your cursor, then chains onward. Can shock." },
  bone_arrow: { color: "#8d8678", aimed: true, kind: "Attack",
    desc: "A swift physical arrow — long reach, and it scales with your weapon." },
};

// gemIconSVG is the one gem glyph, colored per skill: draft cards, the gem
// panel, the skill bar. Supports get a rounded "socket stone" cut instead
// of the skill diamond. The glow rides inline so it works everywhere.
function gemIconSVG(color, { support = false, size = 26, glow = true } = {}) {
  const filter = glow ? ` style="filter: drop-shadow(0 0 ${size / 5}px ${color}aa)"` : "";
  if (support) {
    return `<svg class="gem-icon" width="${size}" height="${size}" viewBox="0 0 24 24"${filter}>
      <rect x="4" y="4" width="16" height="16" rx="5" fill="${color}" stroke="#000a" stroke-width="1.2"/>
      <rect x="7.5" y="7.5" width="9" height="9" rx="3" fill="#ffffff2e"/>
      <circle cx="9.2" cy="8.6" r="2" fill="#ffffff70"/>
    </svg>`;
  }
  return `<svg class="gem-icon" width="${size}" height="${size}" viewBox="0 0 24 24"${filter}>
    <polygon points="12,1.5 21,9 12,22.5 3,9" fill="${color}" stroke="#000a" stroke-width="1.2"/>
    <polygon points="12,1.5 16.5,9 12,22.5 7.5,9" fill="#ffffff24"/>
    <polygon points="3,9 21,9 12,13.5" fill="#00000026"/>
    <circle cx="9.4" cy="6.4" r="1.8" fill="#ffffff80"/>
  </svg>`;
}

let supportTable = [];  // SupportSnap list from the welcome
let cutSkillTable = []; // SkillSnap list from the welcome
let skillBarKey = "";

function skillName(id) {
  const s = cutSkillTable.find((sk) => sk.id === id);
  return s ? s.name : prettify(id);
}

function supportInfo(id) {
  return supportTable.find((s) => s.id === id) || { id, name: prettify(id), desc: "", legal_for: [] };
}

// renderSkillBar rebuilds the bar's DOM when the gem loadout changes
// (cut, level, socket, support — anything that renames or re-costs a slot).
function renderSkillBar(self) {
  const gems = (self && self.gems) || [];
  const key = JSON.stringify(gems.map((g) => [g.skill, g.level, g.mana_cost]));
  if (key === skillBarKey) return;
  skillBarKey = key;
  skillBarEl.replaceChildren();
  gems.slice(0, GEM_KEYS.length).forEach((g, i) => {
    const meta = SKILL_META[g.skill] || { color: "#cfc9bf", aimed: true };
    const btn = document.createElement("button");
    btn.className = "skill-slot";
    btn.id = `slot-${i}`;
    btn.title = `${skillName(g.skill)} — level ${g.level}, ${fmtMana(g.mana_cost)} mana`;
    btn.innerHTML =
      `<div class="glyph">${gemIconSVG(meta.color, { size: 22 })}</div>` +
      `<span class="slot-name">${skillName(g.skill)}</span>` +
      `<span class="key">${GEM_KEYS[i].toUpperCase()}</span>` +
      `<span class="gem-level">${g.level}</span>`;
    btn.onclick = () => castGem(i);
    skillBarEl.appendChild(btn);
  });
}

const fmtMana = (milli) => Math.ceil(milli / 1000);

function castGem(i) {
  const self = me();
  const g = self && self.gems && self.gems[i];
  if (!g) return;
  const meta = SKILL_META[g.skill] || { aimed: true };
  if (meta.aimed) {
    const w = screenToWorldUnits(mouse.x, mouse.y);
    send({ kind: "use_skill", skill: g.skill, x: toMilli(w.x), y: toMilli(w.y) });
  } else {
    send({ kind: "use_skill", skill: g.skill });
  }
  const el = document.getElementById(`slot-${i}`);
  if (el) {
    el.classList.add("cast-flash");
    setTimeout(() => el.classList.remove("cast-flash"), 160);
  }
}

// Flask slots mirror the player def's flask order; charges arrive on the
// actor snap. FLASK_MAX mirrors core.FlaskMaxCharges (display-only).
const FLASKS = [
  { key: "1", name: "Life Flask", cls: "life" },
  { key: "2", name: "Mana Flask", cls: "mana" },
];
const FLASK_MAX = 60;
const FLASK_COST = 30;

const flaskRackEl = document.getElementById("flask-rack");
FLASKS.forEach((f, i) => {
  const el = document.createElement("button");
  el.className = `flask ${f.cls}`;
  el.id = `flask-${i}`;
  el.title = f.name;
  el.innerHTML = `<div class="flask-fill"></div><span class="key">${f.key}</span>`;
  el.onclick = () => send({ kind: "use_flask", target: i });
  flaskRackEl.appendChild(el);
});

const skillBarEl = document.getElementById("skill-bar");

// WASD held-key movement, PoE2-style. The held set drives a short move
// command every WASD_MS toward the combined direction; releasing the last
// key sends stop. Click-to-move still works — whichever input spoke last
// wins, because both are just move commands to the server.
const wasdHeld = new Set();
const WASD_DIRS = { w: [0, -1], a: [-1, 0], s: [0, 1], d: [1, 0] };
const WASD_MS = 100;
const WASD_REACH = 4; // units ahead per command — smooth without overshoot

setInterval(() => {
  if (!wasdHeld.size) return;
  const self = me();
  if (!self) return;
  let dx = 0;
  let dy = 0;
  for (const k of wasdHeld) {
    dx += WASD_DIRS[k][0];
    dy += WASD_DIRS[k][1];
  }
  if (!dx && !dy) return;
  const len = Math.hypot(dx, dy);
  send({
    kind: "move",
    x: self.pos.x + Math.round((dx / len) * WASD_REACH * 1000),
    y: self.pos.y + Math.round((dy / len) * WASD_REACH * 1000),
  });
}, WASD_MS);

window.addEventListener("keyup", (e) => {
  if (joinOpen()) return;
  const key = e.key.toLowerCase();
  if (key in WASD_DIRS && wasdHeld.delete(key) && !wasdHeld.size) {
    send({ kind: "stop" });
  }
});
window.addEventListener("blur", () => wasdHeld.clear());

window.addEventListener("keydown", (e) => {
  if (joinOpen()) return; // typing a name is not gameplay input
  if (e.repeat) return;
  audioUnlock();
  const key = e.key.toLowerCase();
  if (key === "escape") {
    if (cutState) closeCutDialog();
    return;
  }
  if (key in WASD_DIRS) {
    wasdHeld.add(key);
    return;
  }
  if (key === "m") {
    audioMuted = !audioMuted;
    localStorage.setItem("df-muted", audioMuted ? "1" : "0");
    logLine(audioMuted ? "sound muted (M to unmute)" : "sound on");
    return;
  }
  const slot = GEM_KEYS.indexOf(key);
  if (slot >= 0) {
    castGem(slot);
    return;
  }
  const flask = FLASKS.findIndex((f) => f.key === key);
  if (flask >= 0) {
    send({ kind: "use_flask", target: flask });
    return;
  }
  switch (key) {
    case "p":
      send({ kind: "plant_portal" });
      break;
    case "i":
      panel.classList.toggle("hidden");
      if (!panel.classList.contains("hidden")) renderPanel(me(), true);
      break;
    case "f":
      document.getElementById("social").classList.toggle("hidden");
      renderSocial();
      break;
  }
});

// ------------------------------------------------------------ UI panel
//
// Equipment is a row of fixed, labeled slots; the inventory is a grid with
// one cell per bag slot (capacity comes from the actor snap's inv_size).
// Items move by drag and drop: bag → equipment row equips (the server
// resolves which concrete slot), equipment → bag unequips, bag → the game
// canvas drops the item at your feet. Hovering an item shows its tooltip.

const panel = document.getElementById("panel");
const equipmentEl = document.getElementById("equipment");
const inventoryEl = document.getElementById("inventory");
const invCountEl = document.getElementById("inv-count");
const tooltipEl = document.getElementById("tooltip");

// Crafting currency: names/colors by OrbKind order. Clicking an orb arms
// apply mode; the next bag-item click sends apply_orb. The server
// validates rarity rules; the strip just previews intent.
const ORBS = [
  { id: "transmutation", name: "Transmutation", hint: "normal → magic", color: "#7a9bf0" },
  { id: "alchemy", name: "Alchemy", hint: "normal → rare", color: "#f0d060" },
  { id: "chaos", name: "Chaos", hint: "reroll a rare", color: "#d97b4a" },
  // Jewellers target the gem row, not the bag: arm it, click a skill gem.
  { id: "jeweller", name: "Jeweller", hint: "add a gem socket", color: "#4ad1c8" },
];
let armedOrb = -1;

const orbStripEl = document.getElementById("orb-strip");
ORBS.forEach((o, i) => {
  const btn = document.createElement("button");
  btn.className = "orb";
  btn.id = `orb-${i}`;
  btn.title = `${o.name} Orb — ${o.hint}`;
  btn.innerHTML = `<span class="orb-dot" style="background:${o.color}"></span><span id="orb-count-${i}">0</span>`;
  btn.onclick = () => {
    armedOrb = armedOrb === i ? -1 : i;
    renderOrbStrip(me());
  };
  orbStripEl.appendChild(btn);
});

function renderOrbStrip(self) {
  const orbs = (self && self.orbs) || [];
  ORBS.forEach((o, i) => {
    const btn = document.getElementById(`orb-${i}`);
    const n = orbs[i] || 0;
    document.getElementById(`orb-count-${i}`).textContent = n;
    btn.classList.toggle("armed", armedOrb === i);
    btn.classList.toggle("drained", n === 0);
  });
  panel.classList.toggle("orb-armed", armedOrb >= 0);
}

const EQUIP_SLOTS = [
  "weapon", "offhand", "helmet", "body", "gloves",
  "boots", "amulet", "ring1", "ring2", "belt",
];

// Which equipment slots each base item may occupy — mirrors the server's
// slot families, used for drop-target highlighting and slot-addressed
// equips. The server validates regardless.
const BASE_SLOTS = {
  rusty_sword: ["weapon"],
  wooden_shield: ["offhand"],
  leather_cap: ["helmet"],
  leather_vest: ["body"],
  leather_gloves: ["gloves"],
  leather_boots: ["boots"],
  bone_amulet: ["amulet"],
  iron_ring: ["ring1", "ring2"],
  leather_belt: ["belt"],
};

// Procedural item icons: inline SVG keyed by base id, tinted by rarity via
// currentColor. Unknown bases fall back to the drops' diamond.
const ICONS = {
  rusty_sword:
    '<svg viewBox="0 0 24 24"><rect x="10.5" y="2" width="3" height="13" fill="currentColor"/>' +
    '<rect x="7" y="14" width="10" height="2.5" fill="currentColor"/>' +
    '<rect x="10.5" y="16.5" width="3" height="5" fill="currentColor"/></svg>',
  wooden_shield:
    '<svg viewBox="0 0 24 24"><path d="M12 2.5 L20 5.5 V12 C20 17 16.5 20.5 12 21.5 C7.5 20.5 4 17 4 12 V5.5 Z" ' +
    'fill="none" stroke="currentColor" stroke-width="2.5"/></svg>',
  leather_cap:
    '<svg viewBox="0 0 24 24"><path d="M5 14 A7 7 0 0 1 19 14 Z" fill="currentColor"/>' +
    '<rect x="3" y="14.5" width="18" height="3" rx="1.5" fill="currentColor"/></svg>',
  leather_vest:
    '<svg viewBox="0 0 24 24"><path d="M8 3 L4 8 V21 H20 V8 L16 3 L12 6.5 Z" fill="currentColor"/></svg>',
  leather_gloves:
    '<svg viewBox="0 0 24 24"><rect x="8" y="4" width="9" height="13" rx="2" fill="currentColor"/>' +
    '<rect x="4.5" y="9" width="5" height="3.5" rx="1.5" fill="currentColor"/>' +
    '<rect x="8" y="18" width="9" height="3" fill="currentColor"/></svg>',
  leather_boots:
    '<svg viewBox="0 0 24 24"><path d="M7 3 H13 V13 H20 V20 H7 Z" fill="currentColor"/></svg>',
  bone_amulet:
    '<svg viewBox="0 0 24 24"><path d="M5 4 A 9 9 0 0 0 19 4" fill="none" stroke="currentColor" stroke-width="2"/>' +
    '<rect x="9" y="11" width="6" height="6" transform="rotate(45 12 14)" fill="currentColor"/></svg>',
  iron_ring:
    '<svg viewBox="0 0 24 24"><circle cx="12" cy="13.5" r="6" fill="none" stroke="currentColor" stroke-width="3"/>' +
    '<rect x="9.5" y="2" width="5" height="5" transform="rotate(45 12 4.5)" fill="currentColor"/></svg>',
  leather_belt:
    '<svg viewBox="0 0 24 24"><rect x="2" y="9.5" width="20" height="5" rx="1" fill="currentColor"/>' +
    '<rect x="8.5" y="6.5" width="7" height="11" rx="1" fill="none" stroke="currentColor" stroke-width="2"/></svg>',
};
const ICON_FALLBACK =
  '<svg viewBox="0 0 24 24"><rect x="7" y="7" width="10" height="10" transform="rotate(45 12 12)" fill="currentColor"/></svg>';
// Uncut gems: a faceted stone, colored by kind via the slot's gem class.
const ICON_GEM =
  '<svg viewBox="0 0 24 24"><path d="M12 2.5 L19 8 L16 21 H8 L5 8 Z" fill="currentColor"/>' +
  '<path d="M5 8 H19 M12 2.5 L9.5 8 L8 21 M12 2.5 L14.5 8 L16 21" stroke="#0b0b10" stroke-width="1" fill="none"/></svg>';

const prettify = (id) => id.replace(/_/g, " ");

// --- tooltip (item names and affix ids come from our own content tables,
// never from other players, so innerHTML is safe here)

function itemLines(item, where) {
  if (item.gem) {
    const kind = item.gem.support ? "Uncut Support Gem" : `Uncut Skill Gem (level ${item.gem.level})`;
    const cls = item.gem.support ? "gem-support" : "gem-skill";
    const lines = [
      `<span class="tt-name ${cls}">${kind}</span>`,
      `<span class="tt-kind">click to cut</span>`,
    ];
    for (const c of item.gem.choices || []) {
      lines.push(`<span class="tt-affix">${item.gem.support ? supportInfo(c).name : skillName(c)}</span>`);
    }
    return lines;
  }
  if (item.unique) {
    // Uniques speak for themselves: authored name and mod lines, the base
    // as the kind line, flavor text last.
    const lines = [
      `<span class="tt-name rarity-unique">${item.unique.name}</span>`,
      `<span class="tt-kind">unique ${prettify(item.base)}${where ? " · " + where : ""}</span>`,
    ];
    if (item.implicit) {
      lines.push(`<span class="tt-implicit">${prettify(item.implicit.id)}: ${item.implicit.value / 1000}</span>`);
    }
    for (const m of item.unique.mods || []) {
      lines.push(`<span class="tt-affix rarity-unique">${m}</span>`);
    }
    if (item.unique.desc) lines.push(`<span class="tt-plain">${item.unique.desc}</span>`);
    return lines;
  }
  const lines = [
    `<span class="tt-name rarity-${item.rarity}">${prettify(item.base)}</span>`,
    `<span class="tt-kind">${item.rarity}${where ? " · " + where : ""}</span>`,
  ];
  if (item.implicit) {
    lines.push(`<span class="tt-implicit">${prettify(item.implicit.id)}: ${item.implicit.value / 1000}</span>`);
  }
  for (const af of item.affixes || []) {
    lines.push(`<span class="tt-affix">${prettify(af.id)}: ${af.value / 1000}</span>`);
  }
  if (!(item.affixes || []).length) lines.push('<span class="tt-plain">no affixes</span>');
  return lines;
}

function showTooltip(item, where, e, compare) {
  const lines = itemLines(item, where);
  // Hovering a bag item: show what it would replace, PoE-style — the
  // upgrade decision without cross-referencing the equipment row.
  if (compare && !item.gem) {
    const self = me();
    const bySlot = new Map();
    for (const eq of (self && self.equipment) || []) bySlot.set(eq.slot, eq.item);
    for (const slot of legalEquipSlots(item.base)) {
      const worn = bySlot.get(slot);
      if (!worn || worn.id === item.id) continue;
      lines.push(`<span class="tt-compare">— equipped (${slot}) —</span>`);
      lines.push(...itemLines(worn, ""));
    }
  }
  tooltipEl.innerHTML = lines.join("");
  tooltipEl.classList.remove("hidden");
  moveTooltip(e);
}

function moveTooltip(e) {
  const pad = 14;
  const r = tooltipEl.getBoundingClientRect();
  let x = e.clientX + pad;
  let y = e.clientY + pad;
  if (x + r.width > window.innerWidth - 8) x = e.clientX - r.width - pad;
  if (y + r.height > window.innerHeight - 8) y = e.clientY - r.height - pad;
  tooltipEl.style.left = `${x}px`;
  tooltipEl.style.top = `${y}px`;
}

function hideTooltip() {
  tooltipEl.classList.add("hidden");
}

// --- drag and drop

let drag = null; // { id, from: "inv" | "equip", base }

function legalEquipSlots(base) {
  return BASE_SLOTS[base] || [];
}

function makeDraggable(el, item, from) {
  el.draggable = true;
  el.addEventListener("dragstart", (e) => {
    drag = { id: item.id, from, base: item.base };
    el.classList.add("dragging");
    hideTooltip();
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", String(item.id));
    // Highlight legal targets: matching equipment slots for bag items,
    // plus all bag cells (rearrange, or the unequip destination).
    if (from === "inv") {
      for (const s of equipmentEl.children) {
        if (legalEquipSlots(item.base).includes(s.dataset.slot)) s.classList.add("drop-ok");
      }
      for (const s of document.getElementById("stash").children) s.classList.add("drop-ok");
    }
    for (const s of inventoryEl.children) s.classList.add("drop-ok");
  });
  el.addEventListener("dragend", () => {
    drag = null;
    el.classList.remove("dragging");
    clearDropHighlights();
  });
}

function clearDropHighlights() {
  const stashCells = document.getElementById("stash").children;
  for (const s of [...equipmentEl.children, ...inventoryEl.children, ...stashCells]) {
    s.classList.remove("drop-ok");
  }
}

// equipSlotZone makes one labeled equipment slot accept bag items whose
// base belongs there, equipping into THIS slot — a ring dragged onto ring2
// goes to ring2.
function equipSlotZone(div, name) {
  div.dataset.slot = name;
  div.addEventListener("dragover", (e) => {
    if (drag && drag.from === "inv" && legalEquipSlots(drag.base).includes(name)) {
      e.preventDefault();
    }
  });
  div.addEventListener("drop", (e) => {
    if (!drag || drag.from !== "inv" || !legalEquipSlots(drag.base).includes(name)) return;
    e.preventDefault();
    e.stopPropagation();
    send({ kind: "equip", target: drag.id, slot: name });
    drag = null;
    clearDropHighlights();
  });
}

// bagCellZone makes one bag cell accept drags: bag→cell rearranges (pure
// client state), equipment→cell unequips with the item landing in that
// cell.
function bagCellZone(div, idx) {
  div.addEventListener("dragover", (e) => {
    if (drag) e.preventDefault();
  });
  div.addEventListener("drop", (e) => {
    if (!drag) return;
    e.preventDefault();
    e.stopPropagation();
    if (drag.from === "inv") {
      moveBagItem(drag.id, idx);
    } else if (drag.from === "stash") {
      send({ kind: "stash_take", choice: drag.id });
    } else {
      bagLayout.set(drag.id, idx);
      send({ kind: "unequip", target: drag.id });
    }
    drag = null;
    clearDropHighlights();
  });
}

// Container-level fallbacks for drops that land in the gaps: equipment
// area picks the slot automatically, bag area takes unequips wherever.
function makeDropZone(el, accepts, action) {
  el.addEventListener("dragover", (e) => {
    if (drag && drag.from === accepts) e.preventDefault();
  });
  el.addEventListener("drop", (e) => {
    if (!drag || drag.from !== accepts) return;
    e.preventDefault();
    action(drag.id);
    drag = null;
    clearDropHighlights();
  });
}

makeDropZone(equipmentEl, "inv", (id) => send({ kind: "equip", target: id }));
makeDropZone(inventoryEl, "equip", (id) => send({ kind: "unequip", target: id }));
makeDropZone(canvas, "inv", (id) => send({ kind: "drop_item", target: id }));
// The stash (a hidden section outside the hideout, so these can't misfire
// elsewhere): bag → stash banks, stash → bag withdraws.
makeDropZone(document.getElementById("stash"), "inv", (id) => send({ kind: "stash_put", target: id }));
makeDropZone(inventoryEl, "stash", (id) => send({ kind: "stash_take", choice: id }));

// --- bag layout: which cell each item sits in. Client-side only — the
// server's bag is an ordered list; the arrangement is presentation state,
// reconciled against the server's contents every render.

const bagLayout = new Map(); // item id -> cell index

function layoutBag(inv, cap) {
  for (const id of [...bagLayout.keys()]) {
    if (!inv.some((i) => i.id === id)) bagLayout.delete(id);
  }
  const used = new Set();
  for (const [id, c] of [...bagLayout]) {
    if (c >= cap || used.has(c)) bagLayout.delete(id);
    else used.add(c);
  }
  const cells = new Array(cap).fill(null);
  let next = 0;
  for (const it of inv) {
    let c = bagLayout.get(it.id);
    if (c === undefined) {
      while (used.has(next) && next < cap) next++;
      c = Math.min(next, cap - 1);
      bagLayout.set(it.id, c);
      used.add(c);
    }
    cells[c] = it;
  }
  return cells;
}

// moveBagItem reassigns an item to a cell, swapping with any occupant.
function moveBagItem(id, idx) {
  const from = bagLayout.get(id);
  for (const [oid, c] of bagLayout) {
    if (c === idx && oid !== id && from !== undefined) bagLayout.set(oid, from);
  }
  bagLayout.set(id, idx);
  renderPanel(me(), true);
}

// --- slot rendering

function slotDiv(label) {
  const div = document.createElement("div");
  div.className = "slot";
  if (label) {
    const span = document.createElement("span");
    span.className = "slot-label";
    span.textContent = label;
    div.appendChild(span);
  }
  return div;
}

function fillSlot(div, item, from, where) {
  if (item.gem) {
    div.classList.add("filled", item.gem.support ? "gem-support" : "gem-skill");
    div.insertAdjacentHTML("afterbegin", ICON_GEM);
  } else {
    div.classList.add("filled", `rarity-${item.rarity}`);
    div.dataset.base = item.base;
    div.insertAdjacentHTML("afterbegin", ICONS[item.base] || ICON_FALLBACK);
  }
  makeDraggable(div, item, from);
  div.addEventListener("mouseenter", (e) => showTooltip(item, where, e, from === "inv"));
  div.addEventListener("mousemove", moveTooltip);
  div.addEventListener("mouseleave", hideTooltip);
}

// The panel only re-renders when its contents change. Rebuilding it every
// view would destroy the drag source mid-gesture and the hovered slot
// mid-tooltip.
let panelKey = "";

function renderPanel(self, force) {
  const key = !self ? "dead" : JSON.stringify([
    (self.equipment || []).map((e) => e.item.id),
    (self.inventory || []).map((i) => [i.id, i.rarity, (i.affixes || []).length]),
    self.inv_size,
    self.orbs || [],
    self.gems || [],
    stashAvailable() ? (stash.items || []).map((i) => [i.base, i.rarity]) : null,
  ]);
  if (!force && key === panelKey) return;
  panelKey = key;
  hideTooltip();

  equipmentEl.replaceChildren();
  inventoryEl.replaceChildren();
  if (!self) {
    invCountEl.textContent = "";
    gemListEl.replaceChildren();
    return;
  }

  renderGemList(self);

  const bySlot = new Map();
  for (const eq of self.equipment || []) bySlot.set(eq.slot, eq.item);
  for (const name of EQUIP_SLOTS) {
    const div = slotDiv(name);
    equipSlotZone(div, name);
    const item = bySlot.get(name);
    if (item) fillSlot(div, item, "equip", name);
    equipmentEl.appendChild(div);
  }

  renderOrbStrip(self);
  const inv = self.inventory || [];
  const cap = self.inv_size || inv.length;
  invCountEl.textContent = `(${inv.length}/${cap})`;
  const cells = layoutBag(inv, cap);
  for (let i = 0; i < cap; i++) {
    const div = slotDiv();
    bagCellZone(div, i);
    if (cells[i]) {
      fillSlot(div, cells[i], "inv", legalEquipSlots(cells[i].base).join("/"));
      const item = cells[i];
      div.addEventListener("click", () => {
        // Uncut gems open the cutting dialog; crafting orbs target gear.
        if (item.gem) {
          openCutDialog(item);
          return;
        }
        if (armedOrb < 0 || ORBS[armedOrb].id === "jeweller") return;
        send({ kind: "apply_orb", orb: ORBS[armedOrb].id, target: item.id });
        armedOrb = -1;
        renderOrbStrip(me());
      });
    }
    inventoryEl.appendChild(div);
  }

  renderStash();
}

// --- stash: the hideout bank. Named players only, hideout only — the
// server enforces both; the panel just doesn't offer it elsewhere. Items
// render like bag items (their wire ids are stash indices) and move by
// the same drag gestures.

let stash = null; // StashSnap from the welcome / "stash" frames

const stashSectionEl = document.getElementById("stash-section");
const stashEl = document.getElementById("stash");
const stashCountEl = document.getElementById("stash-count");

function stashAvailable() {
  return stash && myName && runState && runState.floor === 0;
}

function renderStash() {
  stashEl.replaceChildren();
  if (!stashAvailable()) {
    stashSectionEl.classList.add("hidden");
    return;
  }
  stashSectionEl.classList.remove("hidden");
  const items = stash.items || [];
  stashCountEl.textContent = `(${items.length}/${stash.cap})`;
  // Enough rows for the contents plus a drop row; the cap is the truth,
  // sixty empty cells is just noise.
  const shown = Math.min(stash.cap, Math.max(10, (Math.floor(items.length / 10) + 1) * 10));
  for (let i = 0; i < shown; i++) {
    const div = slotDiv();
    if (items[i]) fillSlot(div, items[i], "stash", "stash");
    stashEl.appendChild(div);
  }
}

// --- gem row: one card per cut gem — name, level, socket pips. With a
// jeweller armed, clicking a card buys it a socket.

const gemListEl = document.getElementById("gem-list");

function renderGemList(self) {
  gemListEl.replaceChildren();
  (self.gems || []).forEach((g, i) => {
    const card = document.createElement("button");
    card.className = "gem-card";
    const meta = SKILL_META[g.skill] || { color: "#cfc9bf" };
    const pips = (g.supports || []).map((sup) => {
      if (!sup) return '<span class="socket empty" title="empty socket"></span>';
      const info = supportInfo(sup);
      return `<span class="socket filled" title="${info.name} — ${info.desc}">${info.name[0]}</span>`;
    }).join("");
    card.innerHTML =
      gemIconSVG(meta.color, { size: 15 }) +
      `<span class="gem-name">${skillName(g.skill)}</span>` +
      `<span class="gem-lv">Lv ${g.level}</span>` +
      `<span class="sockets">${pips}</span>`;
    card.title = `${skillName(g.skill)} — level ${g.level}, ${fmtMana(g.mana_cost)} mana, ${g.sockets} socket${g.sockets === 1 ? "" : "s"}`;
    card.onclick = () => {
      if (armedOrb >= 0 && ORBS[armedOrb].id === "jeweller") {
        send({ kind: "add_socket", gem: i });
        armedOrb = -1;
        renderOrbStrip(me());
      }
    };
    gemListEl.appendChild(card);
  });
}

// ------------------------------------------------------------ cut dialog
//
// Clicking an uncut gem in the bag opens this. Skill gems offer their
// three-choice draft (or leveling an existing gem to the drop's level);
// support gems pick a support, then a target gem and socket. Every button
// just sends a command — the server validates, and the dialog closes
// optimistically; a rejected cut leaves the item in the bag to try again.

let cutState = null; // { itemId, support, level, choices, choice }
let autoCutShown = false; // one nudge per welcome, not per view
const gemDialog = document.getElementById("gem-dialog");
const gemScrim = document.getElementById("gem-scrim");
gemScrim.addEventListener("click", (e) => {
  if (e.target === gemScrim) closeCutDialog();
});

function openCutDialog(item) {
  cutState = {
    itemId: item.id,
    support: !!item.gem.support,
    level: item.gem.level,
    choices: item.gem.choices || [],
    choice: -1,
  };
  renderCutDialog();
}

function closeCutDialog() {
  cutState = null;
  gemScrim.classList.add("hidden");
  gemDialog.innerHTML = "";
}

// A fresh exile's first act is choosing a skill: with no gems cut and an
// uncut skill gem in the bag, the dialog presents itself. Dismissing it
// keeps it away until the next welcome — the bag click still works.
function autoOpenCut(self) {
  if (autoCutShown || cutState || !self) return;
  if ((self.gems || []).length > 0) return;
  const uncut = (self.inventory || []).find((i) => i.gem && !i.gem.support);
  if (!uncut) return;
  autoCutShown = true;
  openCutDialog(uncut);
}

// syncCutDialog closes the dialog once its item leaves the bag (the cut
// landed, or the item was dropped/lost some other way).
function syncCutDialog(self) {
  if (!cutState) return;
  if (!self || !(self.inventory || []).some((i) => i.id === cutState.itemId)) closeCutDialog();
}

function dialogButton(html, onclick, disabled, title) {
  const btn = document.createElement("button");
  btn.className = "passive-option";
  btn.innerHTML = html;
  if (title) btn.title = title;
  if (disabled) btn.disabled = true;
  else btn.onclick = onclick;
  gemDialog.appendChild(btn);
  return btn;
}

function dialogNote(text) {
  const p = document.createElement("p");
  p.className = "dialog-note";
  p.textContent = text;
  gemDialog.appendChild(p);
}

// dialogHeader builds the modal's masthead: an uncut-gem icon, a title,
// and one line of context.
function dialogHeader(support, title, sub) {
  const head = document.createElement("div");
  head.className = "dlg-head";
  const color = support ? GEM_DROP_COLORS.support : GEM_DROP_COLORS.skill;
  head.innerHTML =
    gemIconSVG(color, { support, size: 34 }) +
    `<div><h3>${title}</h3><p class="dlg-sub">${sub}</p></div>`;
  gemDialog.appendChild(head);
}

// draftCard is one of the three big pick-3 choices.
function draftCard(html, onclick, disabled) {
  const btn = document.createElement("button");
  btn.className = "draft-card";
  btn.innerHTML = html;
  if (disabled) btn.disabled = true;
  else btn.onclick = onclick;
  return btn;
}

function renderCutDialog() {
  if (!cutState) return;
  const self = me();
  if (!self) {
    closeCutDialog();
    return;
  }
  const gems = self.gems || [];
  const st = cutState;
  gemDialog.innerHTML = "";

  if (!st.support && st.choice >= 0) {
    // At the four-gem cap: cutting means destroying one, sockets and all.
    dialogHeader(false, `Cut ${skillName(st.choices[st.choice])}`,
      "your gem bar is full — choose the gem it replaces");
    gems.forEach((g, i) => {
      const meta = SKILL_META[g.skill] || { color: "#cfc9bf" };
      dialogButton(
        gemIconSVG(meta.color, { size: 22 }) +
          `<b>${skillName(g.skill)}</b><span>Lv ${g.level} · ${g.sockets} socket${g.sockets === 1 ? "" : "s"} — destroyed</span>`,
        () => {
          send({ kind: "cut_skill", target: st.itemId, choice: st.choice, replace: true, gem: i });
          closeCutDialog();
        });
    });
    dialogButton("back", () => {
      st.choice = -1;
      renderCutDialog();
    });
  } else if (!st.support) {
    dialogHeader(false, "Cut a Skill Gem",
      gems.length ? `a level ${st.level} uncut gem — pick one of three` :
        "your first skill — pick one of three, the rest drop from monsters");
    const row = document.createElement("div");
    row.className = "draft-row";
    st.choices.forEach((c, idx) => {
      const owned = gems.some((g) => g.skill === c);
      const meta = SKILL_META[c] || { color: "#cfc9bf" };
      row.appendChild(draftCard(
        gemIconSVG(meta.color, { size: 46 }) +
          `<b class="draft-name">${skillName(c)}</b>` +
          `<span class="draft-kind" style="color:${meta.color}">${meta.kind || "Skill"} · Lv ${st.level}</span>` +
          `<span class="draft-desc">${meta.desc || ""}</span>` +
          (owned ? '<span class="draft-owned">already cut</span>' : ""),
        () => {
          if (gems.length >= GEM_KEYS.length) {
            st.choice = idx;
            renderCutDialog();
          } else {
            send({ kind: "cut_skill", target: st.itemId, choice: idx });
            closeCutDialog();
          }
        },
        owned,
      ));
    });
    gemDialog.appendChild(row);
    const uppable = gems.map((g, i) => [g, i]).filter(([g]) => g.level < st.level);
    if (uppable.length) {
      dialogNote("— or raise an existing gem to this level —");
      for (const [g, i] of uppable) {
        const meta = SKILL_META[g.skill] || { color: "#cfc9bf" };
        dialogButton(
          gemIconSVG(meta.color, { size: 22 }) +
            `<b>${skillName(g.skill)}</b><span>Lv ${g.level} → Lv ${st.level}</span>`,
          () => {
            send({ kind: "level_gem", target: st.itemId, gem: i });
            closeCutDialog();
          });
      }
    }
  } else if (st.choice < 0) {
    dialogHeader(true, "Cut a Support Gem", "pick one of three — it sockets into a skill");
    const row = document.createElement("div");
    row.className = "draft-row";
    st.choices.forEach((c, idx) => {
      const info = supportInfo(c);
      row.appendChild(draftCard(
        gemIconSVG(GEM_DROP_COLORS.support, { support: true, size: 46 }) +
          `<b class="draft-name">${info.name}</b>` +
          `<span class="draft-kind" style="color:${GEM_DROP_COLORS.support}">Support</span>` +
          `<span class="draft-desc">${info.desc}</span>`,
        () => {
          st.choice = idx;
          renderCutDialog();
        },
      ));
    });
    gemDialog.appendChild(row);
  } else {
    const info = supportInfo(st.choices[st.choice]);
    dialogHeader(true, `Socket ${info.name}`, info.desc || "choose the skill gem it supports");
    gems.forEach((g, i) => {
      const legal = (info.legal_for || []).includes(g.skill) && !(g.supports || []).includes(info.id);
      const row = document.createElement("div");
      row.className = "socket-row" + (legal ? "" : " illegal");
      row.innerHTML = `<b>${skillName(g.skill)}</b>`;
      (g.supports || []).forEach((sup, s) => {
        const btn = document.createElement("button");
        btn.className = "socket-pick" + (sup ? " occupied" : "");
        btn.textContent = sup ? supportInfo(sup).name[0] : "○";
        btn.title = sup ? `replaces ${supportInfo(sup).name}` : "empty socket";
        if (!legal) btn.disabled = true;
        else btn.onclick = () => {
          send({ kind: "cut_support", target: st.itemId, choice: st.choice, gem: i, socket: s });
          closeCutDialog();
        };
        row.appendChild(btn);
      });
      if (!legal) {
        const why = document.createElement("span");
        why.className = "why";
        why.textContent = (g.supports || []).includes(info.id) ? "already socketed" : "incompatible";
        row.appendChild(why);
      }
      gemDialog.appendChild(row);
    });
    dialogButton("back", () => {
      st.choice = -1;
      renderCutDialog();
    });
  }
  dialogButton("cancel", closeCutDialog);
  gemScrim.classList.remove("hidden");
}

// ----------------------------------------------------------- event log

const logEl = document.getElementById("log");

function nameOf(id) {
  return names.get(id) || `#${id}`;
}

function logEvent(ev) {
  let text = null;
  switch (ev.kind) {
    case "hit":
      text = `${nameOf(ev.actor)} hit ${nameOf(ev.other)} for ${(ev.amount / 1000).toFixed(1)}${ev.crit ? " CRIT" : ""} (${ev.note})`;
      break;
    case "miss":
      text = `${nameOf(ev.actor)} missed ${nameOf(ev.other)}`;
      break;
    case "death":
      text = `${nameOf(ev.actor)} died`;
      break;
    case "ignite":
      text = `${nameOf(ev.other)} is burning`;
      break;
    case "chill":
      text = `${nameOf(ev.other)} is chilled (${Math.round(ev.amount / 10)}% slow)`;
      break;
    case "shock":
      text = `${nameOf(ev.other)} is shocked (+${Math.round(ev.amount / 10)}% damage taken)`;
      break;
    case "drop":
      text = `${ev.note.replace(/_/g, " ")} dropped`;
      break;
    case "pickup":
      text = `${nameOf(ev.actor)} picked up ${ev.note.replace(/_/g, " ")}`;
      break;
    case "gem": {
      const parts = ev.note.split(":");
      const n = Math.round(ev.amount / 1000);
      switch (parts[0]) {
        case "cut":
          text = `${nameOf(ev.actor)} cut ${skillName(parts[1])} (level ${n})`;
          break;
        case "level":
          text = `${nameOf(ev.actor)} raised ${skillName(parts[1])} to level ${n}`;
          break;
        case "support":
          text = `${nameOf(ev.actor)} socketed ${supportInfo(parts[1]).name} into ${skillName(parts[2])}`;
          break;
        case "socket":
          text = `${nameOf(ev.actor)} added a socket to ${skillName(parts[1])} (${n})`;
          break;
      }
      break;
    }
    case "equip":
      text = `${nameOf(ev.actor)} equipped ${ev.note.replace("_", " ")}`;
      break;
    case "unequip":
      text = `${nameOf(ev.actor)} unequipped ${ev.note.replace("_", " ")}`;
      break;
    case "orb": {
      if (ev.note.includes(":")) {
        const [kind, base] = ev.note.split(":");
        text = `${nameOf(ev.actor)} used a ${kind} orb on ${base.replace("_", " ")}`;
      } else {
        text = `${nameOf(ev.actor)} found a ${ev.note} orb (${Math.round(ev.amount / 1000)})`;
      }
      break;
    }
    case "level_up":
      text = `${nameOf(ev.actor)} is now level ${Math.round(ev.amount / 1000)}!`;
      break;
    // Run events are host-synthesized (amounts are floors/counts × 1000).
    case "descend":
      text = `descended to floor ${Math.round(ev.amount / 1000)}`;
      break;
    case "death_eject":
      text = `death! ejected to the portal — ${Math.round(ev.amount / 1000)} portal uses left`;
      break;
    case "run_over":
      text = `THE RUN IS OVER — reached floor ${Math.round(ev.amount / 1000)}. a new run begins`;
      break;
    case "portal":
      switch (ev.note) {
        case "planted":
          text = `portal planted on floor ${Math.round(ev.amount / 1000)}`;
          break;
        case "hideout":
          text = `stepped through to the hideout — ${Math.round(ev.amount / 1000)} portal uses left`;
          break;
        case "return":
          text = `returned to floor ${Math.round(ev.amount / 1000)}`;
          break;
        case "exhausted":
          text = "no portal uses left";
          break;
      }
      break;
  }
  if (!text) return;
  logLine(text, `ev-${ev.kind}`);
}

// logLine appends one line to the event log (client-side notices use it
// too — mute toggles and the like).
function logLine(text, cls) {
  const div = document.createElement("div");
  if (cls) div.className = cls;
  div.textContent = text;
  logEl.appendChild(div);
  while (logEl.children.length > LOG_LINES) logEl.removeChild(logEl.firstChild);
}

// -------------------------------------------------------------- misc

function updateHUD(self) {
  const lifePct = self.max_life > 0 ? (100 * self.life) / self.max_life : 0;
  const manaPct = self.max_mana > 0 ? (100 * self.mana) / self.max_mana : 0;
  document.getElementById("life-fill").style.height = `${lifePct}%`;
  document.getElementById("mana-fill").style.height = `${manaPct}%`;
  renderSkillBar(self);
  (self.gems || []).slice(0, GEM_KEYS.length).forEach((g, i) => {
    const el = document.getElementById(`slot-${i}`);
    if (el) el.classList.toggle("drained", self.mana < g.mana_cost);
  });
  (self.flasks || []).forEach((charges, i) => {
    const el = document.getElementById(`flask-${i}`);
    if (!el) return;
    el.querySelector(".flask-fill").style.height = `${(100 * charges) / FLASK_MAX}%`;
    el.classList.toggle("drained", charges < FLASK_COST);
  });
  document.getElementById("life-text").textContent =
    `${Math.ceil(self.life / 1000)} / ${Math.ceil(self.max_life / 1000)}`;
  document.getElementById("mana-text").textContent =
    `${Math.floor(self.mana / 1000)} / ${Math.ceil(self.max_mana / 1000)}`;
  document.getElementById("level-badge").textContent = `Lv ${self.level || 1}`;
  // xp_next 0 = max level: show a full bar instead of dividing by zero.
  const xpPct = self.xp_next > 0 ? (100 * self.xp) / self.xp_next : 100;
  document.getElementById("xp-fill").style.width = `${xpPct}%`;
}

// updateRunHUD paints the descent scoreboard; floor 0 is the hideout.
// ------------------------------------------------------- passive chooser
//
// The milestone-choice card: shown while the player has an unlocked,
// untaken milestone. Server-authoritative — clicking sends choose_passive
// and the card clears only when the actor's passives list confirms it.
let passiveTable = [];
let passiveKey = "";
const passiveBox = document.getElementById("passive-chooser");

function updatePassiveChooser(self) {
  const taken = new Set(self.passives || []);
  const takenMilestones = new Set(
    [...taken].map((id) => (passiveTable.find((p) => p.id === id) || {}).milestone),
  );
  let pending = 0;
  for (const p of passiveTable) {
    if (self.level >= p.milestone && !takenMilestones.has(p.milestone)) {
      if (!pending || p.milestone < pending) pending = p.milestone;
    }
  }
  const key = pending ? `${pending}:${taken.size}` : "";
  if (key === passiveKey) return;
  passiveKey = key;
  if (!pending) {
    passiveBox.classList.add("hidden");
    passiveBox.innerHTML = "";
    return;
  }
  passiveBox.innerHTML = `<h3>Level ${pending} — choose a passive</h3>`;
  for (const p of passiveTable) {
    if (p.milestone !== pending) continue;
    const btn = document.createElement("button");
    btn.className = "passive-option";
    btn.innerHTML = `<b>${p.name}</b><span>${p.desc}</span>`;
    btn.onclick = () => send({ kind: "choose_passive", passive: p.id });
    passiveBox.appendChild(btn);
  }
  passiveBox.classList.remove("hidden");
}

function updateRunHUD() {
  const el = document.getElementById("run-status");
  if (!runState) {
    el.textContent = "";
    return;
  }
  const where = runState.floor === 0 ? "Hideout" : `Floor ${runState.floor}`;
  el.textContent =
    `Run ${runState.run} · ${where} · Portals ${runState.portals} · Best floor ${runState.best}`;
}

function showOverlay(text) {
  document.getElementById("overlay-text").textContent = text;
  document.getElementById("overlay").classList.remove("hidden");
}

// hideOverlay only ends a pause: death re-asserts itself every view while
// dead, and a closed socket can't deliver a resume, so neither is lost.
function hideOverlay() {
  document.getElementById("overlay").classList.add("hidden");
}

// -------------------------------------------------------------- social
//
// The F panel: your party, and every named player online (the default
// friends list — invitable unless already partied with you). Social frames
// arrive only for named players; guests see a nudge to claim a name.

function renderSocial() {
  const panel = document.getElementById("social");
  const partyEl = document.getElementById("party-list");
  const onlineEl = document.getElementById("online-list");
  const leaveBtn = document.getElementById("leave-party");
  partyEl.textContent = "";
  onlineEl.textContent = "";
  if (!social) {
    partyEl.textContent = myName ? "just you" : "guests can't party — claim a name";
    onlineEl.textContent = "…";
    leaveBtn.classList.add("hidden");
  } else {
    const party = social.party || [];
    if (party.length <= 1) partyEl.textContent = "just you";
    else {
      for (const n of party) {
        const row = document.createElement("div");
        row.className = "social-row";
        row.textContent = n === myName ? `${n} (you)` : n;
        partyEl.appendChild(row);
      }
    }
    leaveBtn.classList.toggle("hidden", party.length <= 1);
    const online = social.online || [];
    if (!online.length) onlineEl.textContent = "nobody else is on";
    for (const n of online) {
      const row = document.createElement("div");
      row.className = "social-row";
      const label = document.createElement("span");
      label.textContent = n;
      row.appendChild(label);
      if (!party.includes(n)) {
        const btn = document.createElement("button");
        btn.textContent = "invite";
        btn.onclick = () => send({ kind: "invite", name: n });
        row.appendChild(btn);
      }
      onlineEl.appendChild(row);
    }
  }
  // The invite toast lives outside the panel — it must interrupt.
  const toast = document.getElementById("invite-toast");
  if (social && social.invite) {
    document.getElementById("invite-text").textContent =
      `${social.invite} invites you to their party`;
    toast.classList.remove("hidden");
  } else {
    toast.classList.add("hidden");
  }
}

function sendAccept() {
  send({ kind: "accept_invite" });
}
function sendDecline() {
  send({ kind: "decline_invite" });
}
function sendLeaveParty() {
  send({ kind: "leave_party" });
}

// ---------------------------------------------------------------- join

function joinOpen() {
  return !document.getElementById("join").classList.contains("hidden");
}

// claimName registers the typed name: the server answers with an HttpOnly
// token cookie that authenticates every later visit — no password, and the
// name itself grants nothing.
async function claimName() {
  const err = document.getElementById("join-error");
  const name = document.getElementById("join-name").value.trim();
  if (!name) {
    err.textContent = "pick a name first";
    return;
  }
  try {
    const r = await fetch("/api/claim", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    const body = await r.json();
    if (!r.ok) {
      err.textContent = body.error || "that didn't work";
      return;
    }
    document.getElementById("join").classList.add("hidden");
    connect();
  } catch {
    err.textContent = "server unreachable";
  }
}

function playGuest() {
  guestMode = true;
  document.getElementById("join").classList.add("hidden");
  connect();
}

// boot: a remembered identity goes straight in; everyone else picks a name
// or plays as a guest.
async function boot() {
  let name = "";
  try {
    name = (await (await fetch("/api/whoami")).json()).name || "";
  } catch {} // unreachable server: fall through, connect() will say so
  if (name) {
    connect();
    return;
  }
  document.getElementById("join").classList.remove("hidden");
  document.getElementById("join-name").focus();
}

boot();
render();
