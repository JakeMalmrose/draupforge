// draupforge web client — phase 1: a dumb terminal for the authoritative
// server. No prediction, no interpolation; it renders the latest snapshot
// and sends commands. The server is the game; this is a window into it.

"use strict";

const SCALE = 42;          // pixels per world unit
const PICKUP_RANGE = 1.9;  // world units; matches server (with margin)
const LOG_LINES = 9;

// ---------------------------------------------------------------- state

let ws = null;
let myId = 0;
let snap = null;            // latest snapshot
let seenSelf = false;       // distinguishes "not spawned yet" from "died"
let pendingPickup = 0;      // drop entity we're walking toward
let lastPickupSent = 0;
let mouse = { x: 0, y: 0 }; // canvas px
let cam = { x: 0, y: 0 };   // world units
const names = new Map();    // entity id -> label, survives despawn

const canvas = document.getElementById("game");
const ctx = canvas.getContext("2d");

// ------------------------------------------------------------- network

function connect() {
  ws = new WebSocket(`ws://${location.host}/ws`);
  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === "welcome") {
      myId = msg.actor;
    } else if (msg.type === "snapshot") {
      snap = msg.snapshot;
      onSnapshot();
    }
  };
  ws.onclose = () => showOverlay("DISCONNECTED");
}

function send(cmd) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(cmd));
}

// world coords in the protocol are milli-units (int). Local math uses units.
const toUnits = (milli) => milli / 1000;
const toMilli = (units) => Math.round(units * 1000);

// ------------------------------------------------------------ snapshot

function me() {
  if (!snap || !myId) return null;
  return snap.actors.find((a) => a.id === myId) || null;
}

function onSnapshot() {
  for (const a of snap.actors) names.set(a.id, a.def === "player" ? `player ${a.id}` : a.def.replace("_", " "));

  const self = me();
  if (self) {
    seenSelf = true;
    cam.x = toUnits(self.pos.x);
    cam.y = toUnits(self.pos.y);
    updateHUD(self);
  } else if (seenSelf) {
    showOverlay("YOU DIED");
  }

  for (const ev of snap.events || []) logEvent(ev);
  autoPickup(self);
  if (!panel.classList.contains("hidden")) renderPanel(self);
}

function autoPickup(self) {
  if (!pendingPickup || !self) return;
  const drop = (snap.drops || []).find((d) => d.id === pendingPickup);
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

function render() {
  ctx.fillStyle = "#0b0b10";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  if (snap) {
    drawGrid();
    for (const d of snap.drops || []) drawDrop(d);
    for (const a of snap.actors) drawActor(a);
    for (const p of snap.projectiles || []) drawProjectile(p);
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

function drawActor(a) {
  const p = worldToScreen(a.pos.x, a.pos.y);
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

function drawProjectile(p) {
  const s = worldToScreen(p.pos.x, p.pos.y);
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
  const drop = (snap.drops || []).find(
    (d) => Math.hypot(toUnits(d.pos.x) - w.x, toUnits(d.pos.y) - w.y) < 0.8
  );
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
      if (!panel.classList.contains("hidden")) renderPanel(me());
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

function renderPanel(self) {
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
    li.onclick = () => send({ kind: "unequip", target: eq.item.id });
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
    li.onclick = () => send({ kind: "equip", target: item.id });
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

connect();
render();
