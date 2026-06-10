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
)

// Command is the only way anything outside the sim affects it. The sim
// validates every command — clients (and AI) are suggestion boxes, never
// authorities. Callers must supply commands in a deterministic order.
type Command struct {
	Actor    EntityID
	Kind     CommandKind
	Point    space.Vec2 // move target or projectile aim
	Skill    string
	TargetID EntityID // melee target
}

type EventKind uint8

const (
	EvHit EventKind = iota
	EvMiss
	EvDeath
	EvIgnite
	EvDrop
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
	default:
		return "drop"
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
}
