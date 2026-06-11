// Binary view frames — the WebSocket wire's snapshot encoding.
//
// One frame carries one view, either as a keyframe (baseline tick 0: the
// complete view, the client resets its state) or as a delta against an
// earlier view identified by its tick. The server only deltas against views
// the client has ACKED (Command{Kind: "ack", Tick: N}), so the client always
// holds the baseline a delta references; on an ack gap the server falls back
// to a keyframe. web/net.js is the mirror decoder — keep them in lockstep
// and bump Version when the layout changes.
//
// Primitives: uvarint / zigzag varint per encoding/binary, strings as
// uvarint length + UTF-8 bytes. Layout:
//
//	u8      frame type (1 = view)
//	uvarint tick
//	uvarint baseline tick (0 = keyframe)
//	section actors      (removed IDs, then changed/new entities with field masks)
//	section projectiles
//	section drops
//	events  (always sent in full, never delta'd)
package protocol

import (
	"encoding/binary"
	"fmt"
	"reflect"
)

const FrameView = 1

// Actor field-mask bits, in encode order.
const (
	actorIdentity = 1 << iota // def, team, radius, inv size — immutable per entity
	actorPos
	actorLife
	actorMaxLife
	actorMana
	actorMaxMana
	actorES
	actorAction
	actorEquipment
	actorInventory
	actorAilments
)

// Projectile field-mask bits.
const (
	projIdentity = 1 << iota // skill, radius
	projPos
)

// Drop field-mask bit. Drops are immutable once spawned, so a drop is either
// new (everything) or unchanged.
const dropAll = 1

// EncodeViewFrame encodes view as one binary frame, delta-encoded against
// base. A nil base produces a keyframe.
func EncodeViewFrame(base, view *Snapshot) []byte {
	w := &bwriter{}
	w.u8(FrameView)
	w.uv(view.Tick)
	if base == nil {
		w.uv(0)
		base = &Snapshot{}
	} else {
		w.uv(base.Tick)
	}
	encodeActors(w, base.Actors, view.Actors)
	encodeProjectiles(w, base.Projectiles, view.Projectiles)
	encodeDrops(w, base.Drops, view.Drops)
	w.uv(uint64(len(view.Events)))
	for _, ev := range view.Events {
		w.str(ev.Kind)
		w.uv(ev.Actor)
		w.uv(ev.Other)
		w.sv(ev.Amount)
		w.str(ev.Note)
	}
	return w.buf
}

// DecodeViewFrame reconstructs the view a frame carries. base must be the
// view whose tick matches the frame's baseline tick (nil for keyframes) —
// the caller keeps recently received views around for exactly this. base is
// never mutated.
func DecodeViewFrame(frame []byte, base *Snapshot) (Snapshot, error) {
	r := &breader{buf: frame}
	if t := r.u8(); t != FrameView && r.err == nil {
		return Snapshot{}, fmt.Errorf("protocol: unknown frame type %d", t)
	}
	tick := r.uv()
	baseTick := r.uv()
	if baseTick == 0 {
		base = &Snapshot{}
	} else if base == nil || base.Tick != baseTick {
		return Snapshot{}, fmt.Errorf("protocol: frame deltas against tick %d, baseline not available", baseTick)
	}
	view := Snapshot{Tick: tick}
	view.Actors = decodeActors(r, base.Actors)
	view.Projectiles = decodeProjectiles(r, base.Projectiles)
	view.Drops = decodeDrops(r, base.Drops)
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		view.Events = append(view.Events, EventSnap{
			Kind: r.str(), Actor: r.uv(), Other: r.uv(), Amount: r.sv(), Note: r.str(),
		})
	}
	if r.err != nil {
		return Snapshot{}, r.err
	}
	return view, nil
}

// BaselineTick reads just the frame's baseline reference, so a receiver can
// check it holds the baseline before decoding.
func BaselineTick(frame []byte) (uint64, error) {
	r := &breader{buf: frame}
	r.u8()
	r.uv()
	bt := r.uv()
	return bt, r.err
}

// ---------------------------------------------------------------- actors

func encodeActors(w *bwriter, base, view []ActorSnap) {
	inView := make(map[uint64]bool, len(view))
	for i := range view {
		inView[view[i].ID] = true
	}
	old := make(map[uint64]*ActorSnap, len(base))
	var removed []uint64
	for i := range base {
		if inView[base[i].ID] {
			old[base[i].ID] = &base[i]
		} else {
			removed = append(removed, base[i].ID)
		}
	}
	w.ids(removed)

	type change struct {
		a    *ActorSnap
		mask uint64
	}
	var changes []change
	for i := range view {
		a := &view[i]
		mask := actorMask(old[a.ID], a)
		if mask != 0 {
			changes = append(changes, change{a, mask})
		}
	}
	w.uv(uint64(len(changes)))
	for _, c := range changes {
		a := c.a
		w.uv(a.ID)
		w.uv(c.mask)
		if c.mask&actorIdentity != 0 {
			w.str(a.Def)
			w.uv(uint64(a.Team))
			w.sv(a.Radius)
			w.uv(uint64(a.InvSize))
		}
		if c.mask&actorPos != 0 {
			w.sv(a.Pos.X)
			w.sv(a.Pos.Y)
		}
		if c.mask&actorLife != 0 {
			w.sv(a.Life)
		}
		if c.mask&actorMaxLife != 0 {
			w.sv(a.MaxLife)
		}
		if c.mask&actorMana != 0 {
			w.sv(a.Mana)
		}
		if c.mask&actorMaxMana != 0 {
			w.sv(a.MaxMana)
		}
		if c.mask&actorES != 0 {
			w.sv(a.ES)
		}
		if c.mask&actorAction != 0 {
			w.str(a.Action)
		}
		if c.mask&actorEquipment != 0 {
			w.uv(uint64(len(a.Equipment)))
			for _, eq := range a.Equipment {
				w.str(eq.Slot)
				w.item(eq.Item)
			}
		}
		if c.mask&actorInventory != 0 {
			w.uv(uint64(len(a.Inventory)))
			for _, it := range a.Inventory {
				w.item(it)
			}
		}
		if c.mask&actorAilments != 0 {
			w.u8(a.Ail)
		}
	}
}

func actorMask(b, a *ActorSnap) uint64 {
	if b == nil {
		return actorIdentity | actorPos | actorLife | actorMaxLife | actorMana |
			actorMaxMana | actorES | actorAction | actorEquipment | actorInventory |
			actorAilments
	}
	var mask uint64
	if b.Def != a.Def || b.Team != a.Team || b.Radius != a.Radius || b.InvSize != a.InvSize {
		mask |= actorIdentity
	}
	if b.Pos != a.Pos {
		mask |= actorPos
	}
	if b.Life != a.Life {
		mask |= actorLife
	}
	if b.MaxLife != a.MaxLife {
		mask |= actorMaxLife
	}
	if b.Mana != a.Mana {
		mask |= actorMana
	}
	if b.MaxMana != a.MaxMana {
		mask |= actorMaxMana
	}
	if b.ES != a.ES {
		mask |= actorES
	}
	if b.Action != a.Action {
		mask |= actorAction
	}
	if !reflect.DeepEqual(b.Equipment, a.Equipment) {
		mask |= actorEquipment
	}
	if !reflect.DeepEqual(b.Inventory, a.Inventory) {
		mask |= actorInventory
	}
	if b.Ail != a.Ail {
		mask |= actorAilments
	}
	return mask
}

// actorDelta is one decoded changed/new entry: the fields named by mask are
// set, the rest are merged from the baseline.
type actorDelta struct {
	a    ActorSnap
	mask uint64
}

func decodeActors(r *breader, base []ActorSnap) []ActorSnap {
	removed := r.idset()
	changed := make(map[uint64]actorDelta)
	var order []uint64 // changed/new IDs in frame order; new ones append last
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		id := r.uv()
		mask := r.uv()
		a := ActorSnap{ID: id}
		if mask&actorIdentity != 0 {
			a.Def = r.str()
			a.Team = uint8(r.uv())
			a.Radius = r.sv()
			a.InvSize = int(r.uv())
		}
		if mask&actorPos != 0 {
			a.Pos = Vec{X: r.sv(), Y: r.sv()}
		}
		if mask&actorLife != 0 {
			a.Life = r.sv()
		}
		if mask&actorMaxLife != 0 {
			a.MaxLife = r.sv()
		}
		if mask&actorMana != 0 {
			a.Mana = r.sv()
		}
		if mask&actorMaxMana != 0 {
			a.MaxMana = r.sv()
		}
		if mask&actorES != 0 {
			a.ES = r.sv()
		}
		if mask&actorAction != 0 {
			a.Action = r.str()
		}
		if mask&actorEquipment != 0 {
			for n := r.uv(); n > 0 && r.err == nil; n-- {
				a.Equipment = append(a.Equipment, EquippedSnap{Slot: r.str(), Item: r.item()})
			}
		}
		if mask&actorInventory != 0 {
			for n := r.uv(); n > 0 && r.err == nil; n-- {
				a.Inventory = append(a.Inventory, r.item())
			}
		}
		if mask&actorAilments != 0 {
			a.Ail = r.u8()
		}
		changed[id] = actorDelta{a, mask}
		order = append(order, id)
	}
	if r.err != nil {
		return nil
	}

	// Surviving baseline actors keep their relative order (which is the
	// world's spawn order), new ones append in frame order — matching how
	// the encoder saw the view, so round-trips are exact.
	var out []ActorSnap
	inBase := make(map[uint64]bool, len(base))
	for i := range base {
		b := base[i]
		inBase[b.ID] = true
		if removed[b.ID] {
			continue
		}
		if d, ok := changed[b.ID]; ok {
			out = append(out, mergeActor(b, d))
		} else {
			out = append(out, b)
		}
	}
	for _, id := range order {
		if !inBase[id] {
			out = append(out, changed[id].a)
		}
	}
	return out
}

func mergeActor(b ActorSnap, d actorDelta) ActorSnap {
	a, n := b, d.a
	if d.mask&actorIdentity != 0 {
		a.Def, a.Team, a.Radius, a.InvSize = n.Def, n.Team, n.Radius, n.InvSize
	}
	if d.mask&actorPos != 0 {
		a.Pos = n.Pos
	}
	if d.mask&actorLife != 0 {
		a.Life = n.Life
	}
	if d.mask&actorMaxLife != 0 {
		a.MaxLife = n.MaxLife
	}
	if d.mask&actorMana != 0 {
		a.Mana = n.Mana
	}
	if d.mask&actorMaxMana != 0 {
		a.MaxMana = n.MaxMana
	}
	if d.mask&actorES != 0 {
		a.ES = n.ES
	}
	if d.mask&actorAction != 0 {
		a.Action = n.Action
	}
	if d.mask&actorEquipment != 0 {
		a.Equipment = n.Equipment
	}
	if d.mask&actorInventory != 0 {
		a.Inventory = n.Inventory
	}
	if d.mask&actorAilments != 0 {
		a.Ail = n.Ail
	}
	return a
}

// ----------------------------------------------------------- projectiles

func encodeProjectiles(w *bwriter, base, view []ProjectileSnap) {
	inView := make(map[uint64]bool, len(view))
	for i := range view {
		inView[view[i].ID] = true
	}
	old := make(map[uint64]*ProjectileSnap, len(base))
	var removed []uint64
	for i := range base {
		if inView[base[i].ID] {
			old[base[i].ID] = &base[i]
		} else {
			removed = append(removed, base[i].ID)
		}
	}
	w.ids(removed)

	type change struct {
		p    *ProjectileSnap
		mask uint64
	}
	var changes []change
	for i := range view {
		p := &view[i]
		var mask uint64
		if b := old[p.ID]; b == nil {
			mask = projIdentity | projPos
		} else {
			if b.Skill != p.Skill || b.Radius != p.Radius {
				mask |= projIdentity
			}
			if b.Pos != p.Pos {
				mask |= projPos
			}
		}
		if mask != 0 {
			changes = append(changes, change{p, mask})
		}
	}
	w.uv(uint64(len(changes)))
	for _, c := range changes {
		w.uv(c.p.ID)
		w.uv(c.mask)
		if c.mask&projIdentity != 0 {
			w.str(c.p.Skill)
			w.sv(c.p.Radius)
		}
		if c.mask&projPos != 0 {
			w.sv(c.p.Pos.X)
			w.sv(c.p.Pos.Y)
		}
	}
}

type projDelta struct {
	p    ProjectileSnap
	mask uint64
}

func decodeProjectiles(r *breader, base []ProjectileSnap) []ProjectileSnap {
	removed := r.idset()
	changed := make(map[uint64]projDelta)
	var order []uint64
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		id := r.uv()
		mask := r.uv()
		p := ProjectileSnap{ID: id}
		if mask&projIdentity != 0 {
			p.Skill = r.str()
			p.Radius = r.sv()
		}
		if mask&projPos != 0 {
			p.Pos = Vec{X: r.sv(), Y: r.sv()}
		}
		changed[id] = projDelta{p, mask}
		order = append(order, id)
	}
	if r.err != nil {
		return nil
	}
	var out []ProjectileSnap
	inBase := make(map[uint64]bool, len(base))
	for i := range base {
		b := base[i]
		inBase[b.ID] = true
		if removed[b.ID] {
			continue
		}
		if d, ok := changed[b.ID]; ok {
			p := b
			if d.mask&projIdentity != 0 {
				p.Skill, p.Radius = d.p.Skill, d.p.Radius
			}
			if d.mask&projPos != 0 {
				p.Pos = d.p.Pos
			}
			out = append(out, p)
		} else {
			out = append(out, b)
		}
	}
	for _, id := range order {
		if !inBase[id] {
			out = append(out, changed[id].p)
		}
	}
	return out
}

// ----------------------------------------------------------------- drops

func encodeDrops(w *bwriter, base, view []DropSnap) {
	inView := make(map[uint64]bool, len(view))
	for i := range view {
		inView[view[i].ID] = true
	}
	inBase := make(map[uint64]bool, len(base))
	var removed []uint64
	for i := range base {
		inBase[base[i].ID] = true
		if !inView[base[i].ID] {
			removed = append(removed, base[i].ID)
		}
	}
	w.ids(removed)

	var added []*DropSnap
	for i := range view {
		if !inBase[view[i].ID] {
			added = append(added, &view[i])
		}
	}
	w.uv(uint64(len(added)))
	for _, d := range added {
		w.uv(d.ID)
		w.uv(dropAll)
		w.sv(d.Pos.X)
		w.sv(d.Pos.Y)
		w.item(d.Item)
	}
}

func decodeDrops(r *breader, base []DropSnap) []DropSnap {
	removed := r.idset()
	var added []DropSnap
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		d := DropSnap{ID: r.uv()}
		r.uv() // mask, always dropAll
		d.Pos = Vec{X: r.sv(), Y: r.sv()}
		d.Item = r.item()
		added = append(added, d)
	}
	if r.err != nil {
		return nil
	}
	var out []DropSnap
	for i := range base {
		if !removed[base[i].ID] {
			out = append(out, base[i])
		}
	}
	return append(out, added...)
}

// ------------------------------------------------------ writer / reader

type bwriter struct{ buf []byte }

func (w *bwriter) u8(v byte)    { w.buf = append(w.buf, v) }
func (w *bwriter) uv(v uint64)  { w.buf = binary.AppendUvarint(w.buf, v) }
func (w *bwriter) sv(v int64)   { w.buf = binary.AppendVarint(w.buf, v) } // zigzag
func (w *bwriter) str(s string) { w.uv(uint64(len(s))); w.buf = append(w.buf, s...) }
func (w *bwriter) ids(ids []uint64) {
	w.uv(uint64(len(ids)))
	for _, id := range ids {
		w.uv(id)
	}
}
func (w *bwriter) item(it ItemSnap) {
	w.uv(it.ID)
	w.str(it.Base)
	w.str(it.Rarity)
	w.uv(uint64(len(it.Affixes)))
	for _, af := range it.Affixes {
		w.str(af.ID)
		w.sv(af.Value)
	}
}

// breader carries a sticky error: after the first malformed read every
// subsequent read returns zero values, and the caller checks err once.
type breader struct {
	buf []byte
	off int
	err error
}

func (r *breader) fail() {
	if r.err == nil {
		r.err = fmt.Errorf("protocol: truncated or malformed frame at byte %d", r.off)
	}
}

func (r *breader) u8() byte {
	if r.err != nil || r.off >= len(r.buf) {
		r.fail()
		return 0
	}
	b := r.buf[r.off]
	r.off++
	return b
}

func (r *breader) uv() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.buf[r.off:])
	if n <= 0 {
		r.fail()
		return 0
	}
	r.off += n
	return v
}

func (r *breader) sv() int64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Varint(r.buf[r.off:])
	if n <= 0 {
		r.fail()
		return 0
	}
	r.off += n
	return v
}

func (r *breader) str() string {
	n := r.uv()
	if r.err != nil || r.off+int(n) > len(r.buf) || n > uint64(len(r.buf)) {
		r.fail()
		return ""
	}
	s := string(r.buf[r.off : r.off+int(n)])
	r.off += int(n)
	return s
}

func (r *breader) idset() map[uint64]bool {
	out := make(map[uint64]bool)
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		out[r.uv()] = true
	}
	return out
}

func (r *breader) item() ItemSnap {
	it := ItemSnap{ID: r.uv(), Base: r.str(), Rarity: r.str()}
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		it.Affixes = append(it.Affixes, AffixSnap{ID: r.str(), Value: r.sv()})
	}
	return it
}
