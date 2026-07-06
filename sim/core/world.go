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
	// CmdChoosePassive takes the milestone passive named by Passive —
	// permanent, one pick per milestone, level-gated.
	CmdChoosePassive
	// CmdUseFlask drinks the flask in slot TargetID (an index into
	// Def.Flasks): costs charges, applies the flask's buff.
	CmdUseFlask
	// CmdApplyOrb spends one Orb (kind in the Orb field) on the inventory
	// item named by TargetID, rerolling or upgrading its rarity.
	CmdApplyOrb
	// CmdCutSkill consumes the uncut skill gem named by TargetID and cuts
	// its draft choice Choice as a new gem. With Replace set, the gem at
	// GemIndex is destroyed to make room (mandatory at the skill-gem cap).
	CmdCutSkill
	// CmdLevelGem consumes the uncut skill gem named by TargetID to raise
	// the gem at GemIndex to the uncut gem's drop level (must be higher).
	CmdLevelGem
	// CmdCutSupport consumes the uncut support gem named by TargetID and
	// sockets its draft choice Choice into the gem at GemIndex, socket
	// Socket. An occupied socket's old support is destroyed.
	CmdCutSupport
	// CmdAddSocket spends a jeweller orb to add a support socket to the
	// gem at GemIndex (capped at MaxGemSockets).
	CmdAddSocket
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
	// Passive is the PassiveDef ID for CmdChoosePassive.
	Passive string
	// Orb is the currency kind for CmdApplyOrb.
	Orb OrbKind
	// Gem-verb addressing: Choice indexes an uncut gem's draft, GemIndex
	// an actor's cut gems, Socket a gem's support sockets. Replace guards
	// CmdCutSkill's destroy-to-make-room path so a zero-valued command
	// can't silently eat gem 0.
	Choice   int
	GemIndex int
	Socket   int
	Replace  bool
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
	EvBuff
	// EvLootStarved fires when an item wanted more affixes than the pool
	// could legally supply — content-authoring visibility, not gameplay.
	EvLootStarved
	// EvLevelUp fires once per level gained; Amount carries the new level.
	EvLevelUp
	// EvPassive fires when an actor takes a milestone passive; Note = ID.
	EvPassive
	// EvOrb: Actor banked a dropped orb (Note = kind, Amount = new count),
	// or crafted with one (Other = the item, Note = "kind:item_base").
	EvOrb
	// EvGem narrates gem verbs: Note = "cut:skill_id", "level:skill_id"
	// (Amount = new level), "support:support_id:skill_id", or
	// "socket:skill_id" (Amount = new socket count).
	EvGem
	// EvSpawn fires when a queued mid-tick spawn materializes: Actor = the
	// newcomer, Other = the cause (the dier for on-death adds), Note = def.
	EvSpawn
	// EvBlock fires when a defender blocks a hit (Actor = attacker,
	// Other = defender): the hit dealt nothing.
	EvBlock
	// EvStun fires when a hit stuns its target (Actor = attacker,
	// Other = defender): the target's action was interrupted.
	EvStun
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
	case EvBuff:
		return "buff"
	case EvLootStarved:
		return "loot_starved"
	case EvLevelUp:
		return "level_up"
	case EvPassive:
		return "passive"
	case EvOrb:
		return "orb"
	case EvGem:
		return "gem"
	case EvSpawn:
		return "spawn"
	case EvBlock:
		return "block"
	case EvStun:
		return "stun"
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
	Crit   bool   // hit events: the strike crit (display emphasis)
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

	// Buffs queued this tick (skill effect points), applied in queue order
	// by the combat phase before hits resolve — chronologically faithful,
	// since effect points fire in the action phase. Same pattern as hits so
	// skills stays a leaf package.
	PendingBuffs []PendingBuff

	// Actor spawns queued this tick (on-death adds; minions someday),
	// materialized in queue order by DrainSpawns at root sim's fixed spawn
	// phase — after combat and deaths, before compaction. Mid-tick systems
	// never insert actors directly: IDs mint at drain, the newcomers take
	// no action and eat no hit on their birth tick, and iteration order
	// stays stable while a tick is in flight (RISKS #2, designed here).
	PendingSpawns []PendingSpawn

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
		Home:  pos,
		Sheet: stats.NewSheet(def.BaseStats),
	}
	a.SetLevel(def.Level) // clamps 0 → 1; applies PerLevel growth before pools fill
	a.Life = a.MaxLife()
	a.Mana = a.MaxMana()
	a.ES = a.MaxES()
	if n := len(def.Flasks); n > 0 {
		a.FlaskCharges = make([]int32, n)
		for i := range a.FlaskCharges {
			a.FlaskCharges[i] = FlaskMaxCharges // start full — fun over friction
		}
	}
	for _, id := range def.StartingGems {
		if sk := w.Content.Skills[id]; sk != nil { // content.DB() asserts these resolve
			a.GrantGem(sk, 1)
		}
	}
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

// SpawnProjectileGem is SpawnProjectile with a baked gem context and chain
// budget — the player-cast path.
func (w *World) SpawnProjectileGem(src *Actor, sk *SkillDef, pos, vel space.Vec2, gem GemCtx) *Projectile {
	p := w.SpawnProjectile(src, sk, pos, vel)
	p.Gem = gem
	p.ChainsLeft = gem.Chains()
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

// PendingBuff is one queued buff application awaiting the combat phase.
type PendingBuff struct {
	Target EntityID
	Buff   *BuffDef
	Source EntityID
}

func (w *World) QueueBuff(b PendingBuff) { w.PendingBuffs = append(w.PendingBuffs, b) }

// PendingSpawn is one queued mid-tick actor creation.
type PendingSpawn struct {
	Def *ActorDef
	Pos space.Vec2
	// Level overrides the def's (0 keeps it) — adds inherit their parent's.
	Level int
	// Source is the causing entity (the dier, the summoner) for the event.
	Source EntityID
	// Owner marks the newcomer a minion of that actor (0 = independent).
	Owner EntityID
	// Lifespan > 0 stamps the newcomer short-lived: it despawns quietly
	// after that many ticks (Actor.LifespanTicks).
	Lifespan uint32
}

// CreditFor resolves who a kill pays: the deepest live owner above the
// killer (a minion's kills are its summoner's), or the killer itself when
// the chain dead-ends. Hop-capped so a content cycle can't loop.
func (w *World) CreditFor(killer *Actor) *Actor {
	credit := killer
	for hops := 0; hops < 4 && credit.Owner != 0; hops++ {
		owner := w.ActorByID(credit.Owner)
		if owner == nil || owner.Dead {
			break
		}
		credit = owner
	}
	return credit
}

func (w *World) QueueSpawn(s PendingSpawn) { w.PendingSpawns = append(w.PendingSpawns, s) }

// DrainSpawns materializes queued spawns in queue order: positions clamp
// to walkable ground, IDs mint here (deterministic in queue order), pools
// fill at the leveled maxima, and each birth emits EvSpawn. Root sim calls
// this at its fixed phase; nothing else may.
func (w *World) DrainSpawns() {
	for _, ps := range w.PendingSpawns {
		pos := ps.Pos
		if w.Grid != nil {
			if p, ok := w.Grid.NearestWalkable(pos); ok {
				pos = p
			} else {
				continue // nowhere legal to stand; the add is simply lost
			}
		}
		a := w.SpawnActor(ps.Def, pos)
		a.Owner = ps.Owner
		a.LifespanTicks = ps.Lifespan
		if ps.Level > 0 {
			a.SetLevel(ps.Level)
			a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
		}
		w.Emit(Event{Kind: EvSpawn, Actor: a.ID, Other: ps.Source, Note: ps.Def.ID})
	}
	w.PendingSpawns = w.PendingSpawns[:0]
}

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
