// draupforge web client — phase 2: still a window into the authoritative
// server, now over the binary delta wire. The client reconstructs views from
// delta frames (net.js), acks each one, and renders ~150ms behind the newest
// view, interpolating entity positions between views — which hides both the
// reduced send rate and snapshot quantization. No prediction yet: input
// still feels its latency.

"use strict";

const SCALE = 42;          // pixels per world unit
const PICKUP_RANGE = 1.9;  // world units; matches server (with margin)
const LOG_LINES = 9;
const VIEW_HISTORY = 32;   // kept as delta baselines; matches the server cap

// ---------------------------------------------------------------- state

let ws = null;
let myId = 0;
let snap = null;            // newest reconstructed view (HUD, log, input)
let seenSelf = false;       // distinguishes "not spawned yet" from "died"
let pendingPickup = 0;      // drop entity we're walking toward
let lastPickupSent = 0;
let mouse = { x: 0, y: 0 }; // canvas px
let cam = { x: 0, y: 0 };   // world units
const names = new Map();    // entity id -> label, survives despawn

// Interpolation: views buffered on the SERVER timeline (tick × tickMs), so
// network jitter perturbs only the clock-offset estimate, not view spacing.
// The renderer lerps between the two views around (now + clockOffset -
// interpDelay). clockOffset locks onto the fastest-arriving views (max) and
// decays slowly so a genuine latency increase re-converges; a huge backward
// jump (server pause, long stall) resnaps instead of waiting out the decay.
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
  ws = new WebSocket(`ws://${location.host}/ws${location.search}`);
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
        myId = msg.actor;
        if (msg.tick_hz && msg.send_every) {
          // 1.5 send intervals behind: one interval to always have a newer
          // view to lerp toward, half an interval of jitter slack.
          tickMs = 1000 / msg.tick_hz;
          const interval = tickMs * msg.send_every;
          interpDelay = Math.min(Math.max(1.5 * interval, 100), 250);
        }
      } else if (msg.type === "pause") {
        if (msg.paused) showOverlay("PAUSED");
        else hideOverlay();
      } else if (msg.type === "snapshot") {
        onView(jsonToView(msg.snapshot)); // ?format=json debug wire
      }
      return;
    }
    const view = decodeViewFrame(e.data, (tick) => viewHistory.get(tick));
    if (view.needBaseline) {
      // We pruned the view this frame deltas against. Tell the server to
      // start over; skip frames until the keyframe lands.
      if (!awaitKeyframe) send({ kind: "ack", tick: 0 });
      awaitKeyframe = true;
      return;
    }
    awaitKeyframe = false;
    viewHistory.set(view.tick, view);
    while (viewHistory.size > VIEW_HISTORY) {
      viewHistory.delete(viewHistory.keys().next().value);
    }
    send({ kind: "ack", tick: view.tick });
    onView(view);
  };
  ws.onclose = () => showOverlay("DISCONNECTED");
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
    names.set(a.id, a.def === "player" ? `player ${a.id}` : a.def.replace("_", " "));
  }

  const self = me();
  if (self) {
    seenSelf = true;
    updateHUD(self);
  } else if (seenSelf) {
    showOverlay("YOU DIED");
  }

  for (const ev of view.events) {
    logEvent(ev);
    if (ev.kind === "hit") {
      const v = IMPACT_VFX[ev.note];
      const target = view.actors.get(ev.other);
      if (v && target) spawnImpact(target.pos, view.tick * tickMs, v);
      if (ev.other === myId) shakeUntil = performance.now() + SHAKE_MS;
    }
  }
  autoPickup(self);
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
    drawGrid();
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

    // Client VFX run on the same delayed server-timeline clock as span();
    // an effect whose moment hasn't been rendered yet (t < 0) just waits.
    const rt = now + clockOffset - interpDelay;
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

function drawActor(a, pos) {
  const p = worldToScreen(pos.x, pos.y);
  const r = toUnits(a.radius) * SCALE;
  const isMe = a.id === myId;

  ctx.beginPath();
  ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
  ctx.fillStyle = a.team === 1 ? (isMe ? "#3d6fd1" : "#2a4fa3") : "#7a2424";
  ctx.fill();
  ctx.lineWidth = 2;
  ctx.strokeStyle = isMe ? "#cfc9bf" : "#00000066";
  ctx.stroke();

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

  ctx.fillStyle = "#8d8678";
  ctx.font = "11px Georgia";
  ctx.textAlign = "center";
  ctx.fillText(names.get(a.id) || a.def, p.x, p.y - r - 16);
}

const AILMENT_RINGS = [
  [1, "#e67e22cc"], // ignited
  [2, "#7fd4ffcc"], // chilled
  [4, "#f5e25fcc"], // shocked
];

const PROJ_COLORS = {
  fireball: ["#ffd27d", "#d35400"],
  spark: ["#ffffff", "#5fa8f5"],
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

function drawDrop(d) {
  const p = worldToScreen(d.pos.x, d.pos.y);
  ctx.save();
  ctx.translate(p.x, p.y);
  ctx.rotate(Math.PI / 4);
  ctx.fillStyle = { normal: "#cfc9bf", magic: "#8888ff", rare: "#ffff77" }[d.item.rarity] || "#cfc9bf";
  ctx.fillRect(-6, -6, 12, 12);
  ctx.strokeStyle = "#000000aa";
  ctx.strokeRect(-6, -6, 12, 12);
  ctx.restore();
  ctx.fillStyle = "#b8a44a";
  ctx.font = "11px Georgia";
  ctx.textAlign = "center";
  ctx.fillText(d.item.base.replace("_", " "), p.x, p.y - 12);
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
};

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

// --------------------------------------------------------------- input

canvas.addEventListener("mousemove", (e) => { mouse.x = e.offsetX; mouse.y = e.offsetY; });

canvas.addEventListener("mousedown", (e) => {
  if (e.button !== 0 || !snap) return;
  const w = screenToWorldUnits(e.offsetX, e.offsetY);
  let drop = null;
  for (const d of snap.drops.values()) {
    if (Math.hypot(toUnits(d.pos.x) - w.x, toUnits(d.pos.y) - w.y) < 0.8) { drop = d; break; }
  }
  if (drop) {
    pendingPickup = drop.id;
    send({ kind: "move", x: drop.pos.x, y: drop.pos.y });
  } else {
    pendingPickup = 0;
    send({ kind: "move", x: toMilli(w.x), y: toMilli(w.y) });
  }
});

window.addEventListener("keydown", (e) => {
  if (e.repeat) return;
  switch (e.key.toLowerCase()) {
    case "q": {
      const w = screenToWorldUnits(mouse.x, mouse.y);
      send({ kind: "use_skill", skill: "fireball", x: toMilli(w.x), y: toMilli(w.y) });
      break;
    }
    case "e":
      send({ kind: "use_skill", skill: "frost_nova" });
      break;
    case "r": {
      const w = screenToWorldUnits(mouse.x, mouse.y);
      send({ kind: "use_skill", skill: "spark", x: toMilli(w.x), y: toMilli(w.y) });
      break;
    }
    case "i":
      panel.classList.toggle("hidden");
      if (!panel.classList.contains("hidden")) renderPanel(me(), true);
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

const prettify = (id) => id.replace(/_/g, " ");

// --- tooltip (item names and affix ids come from our own content tables,
// never from other players, so innerHTML is safe here)

function showTooltip(item, where, e) {
  const lines = [
    `<span class="tt-name rarity-${item.rarity}">${prettify(item.base)}</span>`,
    `<span class="tt-kind">${item.rarity}${where ? " · " + where : ""}</span>`,
  ];
  for (const af of item.affixes || []) {
    lines.push(`<span class="tt-affix">${prettify(af.id)}: ${af.value / 1000}</span>`);
  }
  if (!(item.affixes || []).length) lines.push('<span class="tt-plain">no affixes</span>');
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
  for (const s of [...equipmentEl.children, ...inventoryEl.children]) {
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
  div.classList.add("filled", `rarity-${item.rarity}`);
  div.dataset.base = item.base;
  div.insertAdjacentHTML("afterbegin", ICONS[item.base] || ICON_FALLBACK);
  makeDraggable(div, item, from);
  div.addEventListener("mouseenter", (e) => showTooltip(item, where, e));
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
    (self.inventory || []).map((i) => i.id),
    self.inv_size,
  ]);
  if (!force && key === panelKey) return;
  panelKey = key;
  hideTooltip();

  equipmentEl.replaceChildren();
  inventoryEl.replaceChildren();
  if (!self) {
    invCountEl.textContent = "";
    return;
  }

  const bySlot = new Map();
  for (const eq of self.equipment || []) bySlot.set(eq.slot, eq.item);
  for (const name of EQUIP_SLOTS) {
    const div = slotDiv(name);
    equipSlotZone(div, name);
    const item = bySlot.get(name);
    if (item) fillSlot(div, item, "equip", name);
    equipmentEl.appendChild(div);
  }

  const inv = self.inventory || [];
  const cap = self.inv_size || inv.length;
  invCountEl.textContent = `(${inv.length}/${cap})`;
  const cells = layoutBag(inv, cap);
  for (let i = 0; i < cap; i++) {
    const div = slotDiv();
    bagCellZone(div, i);
    if (cells[i]) fillSlot(div, cells[i], "inv", legalEquipSlots(cells[i].base).join("/"));
    inventoryEl.appendChild(div);
  }
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
      text = `${nameOf(ev.actor)} hit ${nameOf(ev.other)} for ${(ev.amount / 1000).toFixed(1)} (${ev.note})`;
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
      text = `${ev.note.replace("_", " ")} dropped`;
      break;
    case "pickup":
      text = `${nameOf(ev.actor)} picked up ${ev.note.replace("_", " ")}`;
      break;
    case "equip":
      text = `${nameOf(ev.actor)} equipped ${ev.note.replace("_", " ")}`;
      break;
    case "unequip":
      text = `${nameOf(ev.actor)} unequipped ${ev.note.replace("_", " ")}`;
      break;
  }
  if (!text) return;
  const div = document.createElement("div");
  div.className = `ev-${ev.kind}`;
  div.textContent = text;
  logEl.appendChild(div);
  while (logEl.children.length > LOG_LINES) logEl.removeChild(logEl.firstChild);
}

// -------------------------------------------------------------- misc

function updateHUD(self) {
  const lifePct = self.max_life > 0 ? (100 * self.life) / self.max_life : 0;
  const manaPct = self.max_mana > 0 ? (100 * self.mana) / self.max_mana : 0;
  document.getElementById("life-fill").style.width = `${lifePct}%`;
  document.getElementById("mana-fill").style.width = `${manaPct}%`;
  document.getElementById("life-text").textContent =
    `${Math.ceil(self.life / 1000)} / ${Math.ceil(self.max_life / 1000)}`;
  document.getElementById("mana-text").textContent =
    `${Math.floor(self.mana / 1000)} / ${Math.ceil(self.max_mana / 1000)}`;
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

connect();
render();
