// draupforge wire decoding — the JS mirror of protocol/binary.go. Keep the
// two in lockstep and bump the version together; the server's welcome frame
// carries its version so a stale client fails loudly instead of misreading
// frames.
//
// A decoded view is { tick, actors: Map, projectiles: Map, drops: Map,
// events: [] } keyed by entity ID; entity objects use the same field names
// as the JSON snapshot wire, so the renderer doesn't care which wire fed it.

"use strict";

const PROTOCOL_VERSION = 21;

const FRAME_VIEW = 1;

const ACTOR_IDENTITY = 1 << 0; // def, team, radius, inv_size, rarity, mods
const ACTOR_POS = 1 << 1;
const ACTOR_LIFE = 1 << 2;
const ACTOR_MAXLIFE = 1 << 3;
const ACTOR_MANA = 1 << 4;
const ACTOR_MAXMANA = 1 << 5;
const ACTOR_ES = 1 << 6;
const ACTOR_ACTION = 1 << 7;
const ACTOR_EQUIPMENT = 1 << 8;
const ACTOR_INVENTORY = 1 << 9;
const ACTOR_AILMENTS = 1 << 10; // bitmask: 1 ignited, 2 chilled, 4 shocked
const ACTOR_PROGRESS = 1 << 11; // level, xp, xp_next
const ACTOR_PASSIVES = 1 << 12; // taken milestone-passive IDs
const ACTOR_FLASKS = 1 << 13; // charges per flask slot
const ACTOR_ORBS = 1 << 14; // crafting-currency wallet
const ACTOR_GEMS = 1 << 15; // cut skill gems (skill bar + gem panel)
const ACTOR_TELEGRAPH = 1 << 16; // pending-effect danger zone

const PROJ_IDENTITY = 1 << 0; // skill, radius
const PROJ_POS = 1 << 1;

function netReader(buf) {
  const bytes = new Uint8Array(buf);
  const td = new TextDecoder();
  let off = 0;
  const fail = () => {
    throw new Error(`malformed view frame at byte ${off}`);
  };
  const u8 = () => {
    if (off >= bytes.length) fail();
    return bytes[off++];
  };
  // Unsigned varint (LEB128). Multiplication instead of bit shifts: values
  // are milli-unit int64s that exceed 32-bit JS bitwise range but stay well
  // inside Number's 2^53.
  const uv = () => {
    let v = 0, mul = 1;
    for (;;) {
      const b = u8();
      v += (b & 0x7f) * mul;
      if ((b & 0x80) === 0) return v;
      mul *= 128;
    }
  };
  // Signed varint: zigzag per encoding/binary.
  const sv = () => {
    const u = uv();
    return u % 2 === 1 ? -(u + 1) / 2 : u / 2;
  };
  const str = () => {
    const n = uv();
    if (off + n > bytes.length) fail();
    const s = td.decode(bytes.subarray(off, off + n));
    off += n;
    return s;
  };
  return { u8, uv, sv, str };
}

function readItem(r) {
  const item = { id: r.uv(), base: r.str(), rarity: r.str(), ilvl: r.uv() };
  if (r.u8() === 1) item.implicit = { id: r.str(), value: r.sv() };
  const n = r.uv();
  if (n > 0) {
    item.affixes = [];
    for (let i = 0; i < n; i++) item.affixes.push({ id: r.str(), value: r.sv() });
  }
  if (r.u8() === 1) {
    const gem = { support: r.u8() === 1, level: r.uv(), choices: [] };
    for (let m = r.uv(); m > 0; m--) gem.choices.push(r.str());
    item.gem = gem;
  }
  if (r.u8() === 1) {
    const unique = { name: r.str(), desc: r.str(), mods: [] };
    for (let m = r.uv(); m > 0; m--) unique.mods.push(r.str());
    item.unique = unique;
  }
  return item;
}

function emptyView() {
  return { tick: 0, actors: new Map(), projectiles: new Map(), drops: new Map(), events: [] };
}

// jsonToView normalizes a full-JSON snapshot (the ?format=json debug wire)
// into the same shape binary decoding produces.
function jsonToView(snap) {
  const view = emptyView();
  view.tick = snap.tick;
  for (const a of snap.actors || []) view.actors.set(a.id, a);
  for (const p of snap.projectiles || []) view.projectiles.set(p.id, p);
  for (const d of snap.drops || []) view.drops.set(d.id, d);
  view.events = snap.events || [];
  return view;
}

// decodeViewFrame decodes one binary frame against baseFor(baselineTick).
// Returns the reconstructed view, or { needBaseline: tick } if the frame
// deltas against a view we no longer hold (caller acks 0 and waits for a
// keyframe).
function decodeViewFrame(buf, baseFor) {
  const r = netReader(buf);
  if (r.u8() !== FRAME_VIEW) throw new Error("unknown frame type");
  const tick = r.uv();
  const baseTick = r.uv();
  let base;
  if (baseTick === 0) {
    base = emptyView();
  } else {
    base = baseFor(baseTick);
    if (!base) return { needBaseline: baseTick };
  }

  const view = {
    tick,
    actors: new Map(base.actors),
    projectiles: new Map(base.projectiles),
    drops: new Map(base.drops),
    events: [],
  };

  // Actors: removals, then changed/new entries merged over the baseline.
  for (let n = r.uv(); n > 0; n--) view.actors.delete(r.uv());
  for (let n = r.uv(); n > 0; n--) {
    const id = r.uv();
    const mask = r.uv();
    const a = Object.assign({}, view.actors.get(id) || { id });
    if (mask & ACTOR_IDENTITY) {
      a.def = r.str();
      a.team = r.uv();
      a.radius = r.sv();
      a.inv_size = r.uv();
      a.rarity = r.str();
      a.mods = [];
      for (let m = r.uv(); m > 0; m--) a.mods.push(r.str());
    }
    if (mask & ACTOR_POS) a.pos = { x: r.sv(), y: r.sv() };
    if (mask & ACTOR_LIFE) a.life = r.sv();
    if (mask & ACTOR_MAXLIFE) a.max_life = r.sv();
    if (mask & ACTOR_MANA) a.mana = r.sv();
    if (mask & ACTOR_MAXMANA) a.max_mana = r.sv();
    if (mask & ACTOR_ES) a.es = r.sv();
    if (mask & ACTOR_ACTION) a.action = r.str();
    if (mask & ACTOR_EQUIPMENT) {
      a.equipment = [];
      for (let m = r.uv(); m > 0; m--) a.equipment.push({ slot: r.str(), item: readItem(r) });
    }
    if (mask & ACTOR_INVENTORY) {
      a.inventory = [];
      for (let m = r.uv(); m > 0; m--) a.inventory.push(readItem(r));
    }
    if (mask & ACTOR_AILMENTS) a.ail = r.u8();
    if (mask & ACTOR_PROGRESS) {
      a.level = r.uv();
      a.xp = r.sv();
      a.xp_next = r.sv();
    }
    if (mask & ACTOR_PASSIVES) {
      a.passives = [];
      for (let m = r.uv(); m > 0; m--) a.passives.push(r.str());
    }
    if (mask & ACTOR_FLASKS) {
      a.flasks = [];
      for (let m = r.uv(); m > 0; m--) a.flasks.push(r.sv());
    }
    if (mask & ACTOR_ORBS) {
      a.orbs = [];
      for (let m = r.uv(); m > 0; m--) a.orbs.push(r.sv());
    }
    if (mask & ACTOR_GEMS) {
      a.gems = [];
      for (let m = r.uv(); m > 0; m--) {
        const g = { skill: r.str(), level: r.uv(), sockets: r.uv(), supports: [] };
        for (let k = r.uv(); k > 0; k--) g.supports.push(r.str());
        g.mana_cost = r.sv();
        a.gems.push(g);
      }
    }
    if (mask & ACTOR_TELEGRAPH) {
      a.telegraph = r.u8() === 1
        ? { x: r.sv(), y: r.sv(), radius: r.sv(), left: r.uv(), total: r.uv() }
        : null;
    }
    view.actors.set(id, a);
  }

  // Projectiles.
  for (let n = r.uv(); n > 0; n--) view.projectiles.delete(r.uv());
  for (let n = r.uv(); n > 0; n--) {
    const id = r.uv();
    const mask = r.uv();
    const p = Object.assign({}, view.projectiles.get(id) || { id });
    if (mask & PROJ_IDENTITY) {
      p.skill = r.str();
      p.radius = r.sv();
    }
    if (mask & PROJ_POS) p.pos = { x: r.sv(), y: r.sv() };
    view.projectiles.set(id, p);
  }

  // Drops are immutable: removals plus complete new entries.
  for (let n = r.uv(); n > 0; n--) view.drops.delete(r.uv());
  for (let n = r.uv(); n > 0; n--) {
    const d = { id: r.uv() };
    r.uv(); // mask: always "everything" for drops
    d.pos = { x: r.sv(), y: r.sv() };
    d.item = readItem(r);
    view.drops.set(d.id, d);
  }

  // Events ride along in full.
  for (let n = r.uv(); n > 0; n--) {
    view.events.push({ kind: r.str(), actor: r.uv(), other: r.uv(), amount: r.sv(), note: r.str(), crit: r.u8() === 1 });
  }
  return view;
}

// Node export for parity checks; browsers just use the globals.
if (typeof module !== "undefined") {
  module.exports = { PROTOCOL_VERSION, decodeViewFrame, jsonToView, emptyView };
}
