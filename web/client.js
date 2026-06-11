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
    if (newest) diffFades(newest.view, view, now);
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

  for (const ev of view.events) logEvent(ev);
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
    const self = s.to.actors.get(myId);
    if (self) {
      const p = lerpPos(s.from.actors, self, s.t);
      cam.x = toUnits(p.x);
      cam.y = toUnits(p.y);
    }
    drawGrid();
    const now = performance.now();
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

function drawProjectile(p, pos) {
  const s = worldToScreen(pos.x, pos.y);
  const r = Math.max(toUnits(p.radius) * SCALE, 4);
  const grad = ctx.createRadialGradient(s.x, s.y, 1, s.x, s.y, r);
  grad.addColorStop(0, "#ffd27d");
  grad.addColorStop(1, "#d35400");
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
    case "i":
      panel.classList.toggle("hidden");
      if (!panel.classList.contains("hidden")) renderPanel(me(), true);
      break;
  }
});

// ------------------------------------------------------------ UI panel

const panel = document.getElementById("panel");
const equipmentEl = document.getElementById("equipment");
const inventoryEl = document.getElementById("inventory");
const invCountEl = document.getElementById("inv-count");

function itemLI(item, extra) {
  const li = document.createElement("li");
  const name = document.createElement("span");
  name.className = `rarity-${item.rarity}`;
  name.textContent = (extra ? `[${extra}] ` : "") + item.base.replace("_", " ");
  li.appendChild(name);
  for (const af of item.affixes || []) {
    const div = document.createElement("span");
    div.className = "affix";
    div.textContent = `${af.id.replace(/_/g, " ")}: ${af.value / 1000}`;
    li.appendChild(div);
  }
  return li;
}

// The panel only re-renders when its contents change. Rebuilding it every
// view destroys the element mid-click — mousedown lands on a node that's
// gone by mouseup, so click events never fire.
let panelKey = "";

function renderPanel(self, force) {
  const key = !self ? "dead" : JSON.stringify([
    (self.equipment || []).map((e) => e.item.id),
    (self.inventory || []).map((i) => i.id),
  ]);
  if (!force && key === panelKey) return;
  panelKey = key;

  equipmentEl.replaceChildren();
  inventoryEl.replaceChildren();
  if (!self) return;

  if ((self.equipment || []).length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "nothing equipped";
    equipmentEl.appendChild(li);
  }
  for (const eq of self.equipment || []) {
    const li = itemLI(eq.item, eq.slot);
    li.title = "click to unequip";
    li.onmousedown = (e) => { if (e.button === 0) send({ kind: "unequip", target: eq.item.id }); };
    equipmentEl.appendChild(li);
  }

  invCountEl.textContent = `(${(self.inventory || []).length})`;
  if ((self.inventory || []).length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "empty bag";
    inventoryEl.appendChild(li);
  }
  for (const item of self.inventory || []) {
    const li = itemLI(item);
    li.title = "click to equip · right-click to drop";
    li.onmousedown = (e) => { if (e.button === 0) send({ kind: "equip", target: item.id }); };
    li.oncontextmenu = (e) => {
      e.preventDefault();
      send({ kind: "drop_item", target: item.id });
    };
    inventoryEl.appendChild(li);
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
    case "drop":
      text = `${ev.note.replace("_", " ")} dropped`;
      break;
    case "pickup":
      text = `${nameOf(ev.actor)} picked up ${ev.note.replace("_", " ")}`;
      break;
    case "equip":
      text = `${nameOf(ev.actor)} equipped ${ev.note.replace("_", " ")}`;
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
