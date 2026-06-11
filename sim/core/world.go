package core

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

type CommandKind uint8

const (
	CmdMove CommandKind = iota
	CmdUseSkill
	CmdStop
	// CmdEquip equips the item named by TargetID — from the inventory if
	// it's there, else from a ground drop in pickup range. A displaced item
	// goes to the inventory, or the ground if the bag is full.
	CmdEquip
	// CmdPickup moves the ground drop named by TargetID into the inventory.
	CmdPickup
	// CmdUnequip moves the equipped item named by TargetID into the
	// inventory; rejected if the bag is full.
	CmdUnequip
	// CmdDropItem drops the inventory item named by TargetID at the
	// actor's feet.
	CmdDropItem
)

// Command is the only way anything outside the sim affects it. The sim
// validates every command — clients (and AI) are suggestion boxes, never
// authorities. Callers must supply commands in a deterministic order.
type Command struct {
	Actor    EntityID
	Kind     CommandKind
	Point    space.Vec2 // move target or projectile aim
	Skill    string
	TargetID EntityID // melee target, or the item/drop an item verb names
	// Slot is the concrete equipment slot for CmdEquip, honored only when
	// HasSlot is set — so the struct's zero value means "sim picks by
	// family preference", and hand-built commands can't accidentally
	// target the weapon slot (slot 0).
	Slot    EquipSlot
	HasSlot bool
}

type EventKind uint8

const (
	EvHit EventKind = iota
	EvMiss
	EvDeath
	EvIgnite
	EvDrop
	EvEquip
	EvPickup
	EvUnequip
	EvChill
	EvShock
)

func (k EventKind) String() string {
	switch k {
	case EvHit:
		return "hit"
	case EvMiss:
		return "miss"
	case EvDeath:
		return "death"
	case EvIgnite:
		return "ignite"
	case EvDrop:
		return "drop"
	case EvEquip:
		return "equip"
	case EvPickup:
		return "pickup"
	case EvChill:
		return "chill"
	case EvShock:
		return "shock"
	default:
		return "unequip"
	}
}

// Event is the synchronous in-tick event record: the hook for loot, future
// triggers ("on kill, explode"), combat logging, and tests.
type Event struct {
	Kind   EventKind
	Tick   uint64
	Actor  EntityID // subject (attacker, dier, …)
	Other  EntityID // object (defender, killer, drop id, …)
	Amount fm.Fixed
	Note   string // skill id, item base, …
}

// World is all mutable state for one map instance. It is strictly
// single-goroutine; instances are the unit of parallelism.
type World struct {
	Tick    uint64
	Content *ContentDB

	// Grid is the terrain, immutable once set; nil means the v1 open plane
	// (no walls, straight-line movement). Set it before any actor spawns.
	Grid *space.Grid

	Actors      []*Actor
	Projectiles []*Projectile
	Drops       []*Drop

	// Hits queued this tick, resolved in queue order by the combat phase.
	PendingHits []Hit

	// Independent streams so adding a roll in one system doesn't reshuffle
	// the others in replays.
	RNGCombat, RNGLoot, RNGAI, RNGMap *RNG

	// LastEvents holds the completed tick's events for snapshots/logging.
	LastEvents []Event

	nextID EntityID
	idx    map[EntityID]*Actor
	events []Event
}

func NewWorld(db *ContentDB, seed uint64) *World {
	st := seed
	return &World{
		Content:   db,
		RNGCombat: NewRNG(SplitMix64(&st)),
		RNGLoot:   NewRNG(SplitMix64(&st)),
		RNGAI:     NewRNG(SplitMix64(&st)),
		RNGMap:    NewRNG(SplitMix64(&st)),
		idx:       make(map[EntityID]*Actor),
	}
}

func (w *World) nextEntityID() EntityID {
	w.nextID++
	return w.nextID
}

// AllocID hands out an entity ID for non-spawned identities (rolled items).
func (w *World) AllocID() EntityID { return w.nextEntityID() }

// DropByID returns nil for unknown or already-taken drops.
func (w *World) DropByID(id EntityID) *Drop {
	for _, d := range w.Drops {
		if d.ID == id && !d.Taken {
			return d
		}
	}
	return nil
}

func (w *World) SpawnActor(def *ActorDef, pos space.Vec2) *Actor {
	a := &Actor{
		ID:    w.nextEntityID(),
		Def:   def,
		Team:  def.Team,
		Pos:   pos,
		Sheet: stats.NewSheet(def.BaseStats),
	}
	a.Life = a.MaxLife()
	a.Mana = a.MaxMana()
	a.ES = a.MaxES()
	w.Actors = append(w.Actors, a)
	w.idx[a.ID] = a
	return a
}

func (w *World) SpawnProjectile(src *Actor, sk *SkillDef, pos, vel space.Vec2) *Projectile {
	p := &Projectile{
		ID:        w.nextEntityID(),
		Source:    src.ID,
		Team:      src.Team,
		Skill:     sk,
		Pos:       pos,
		Vel:       vel,
		TicksLeft: sk.ProjTTL,
	}
	w.Projectiles = append(w.Projectiles, p)
	return p
}

func (w *World) SpawnDrop(pos space.Vec2, item Item) *Drop {
	d := &Drop{ID: w.nextEntityID(), Pos: pos, Item: item}
	w.Drops = append(w.Drops, d)
	return d
}

// ActorByID returns nil for unknown or compacted-away actors.
func (w *World) ActorByID(id EntityID) *Actor { return w.idx[id] }

func (w *World) QueueHit(h Hit) { w.PendingHits = append(w.PendingHits, h) }

func (w *World) Emit(e Event) {
	e.Tick = w.Tick
	w.events = append(w.events, e)
}

// Events returns the events emitted so far this tick (for in-tick phases
// like loot that react to deaths).
func (w *World) Events() []Event { return w.events }

// BeginTick advances the clock and clears per-tick state.
func (w *World) BeginTick() {
	w.Tick++
	w.events = w.events[:0]
}

// EndTick publishes this tick's events and compacts dead entities.
func (w *World) EndTick() {
	w.LastEvents = append(w.LastEvents[:0], w.events...)

	actors := w.Actors[:0]
	for _, a := range w.Actors {
		if a.Dead {
			delete(w.idx, a.ID)
			continue
		}
		actors = append(actors, a)
	}
	w.Actors = actors

	projs := w.Projectiles[:0]
	for _, p := range w.Projectiles {
		if !p.Dead {
			projs = append(projs, p)
		}
	}
	w.Projectiles = projs

	drops := w.Drops[:0]
	for _, d := range w.Drops {
		if !d.Taken {
			drops = append(drops, d)
		}
	}
	w.Drops = drops
}
