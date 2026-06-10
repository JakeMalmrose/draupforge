package core

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// EntityID is assigned monotonically by the world and never reused. Actors,
// projectiles, and drops share one ID space.
type EntityID uint64

type ActionKind uint8

const (
	ActionIdle ActionKind = iota
	ActionMove
	ActionSkill
)

type ActionPhase uint8

const (
	PhaseWindup ActionPhase = iota
	PhaseRecovery
)

// Action is what an actor is currently doing. One action at a time; skill
// use runs windup → effect point → recovery, counted in ticks.
type Action struct {
	Kind ActionKind

	MoveTarget space.Vec2

	Skill         *SkillDef
	AimPoint      space.Vec2 // projectile aim
	TargetID      EntityID   // melee target
	Phase         ActionPhase
	TicksLeft     uint32
	RecoveryTicks uint32 // precomputed at use time with speed applied
}

// DoT is an active damage-over-time effect. DoTs are not hits — they skip
// hit/crit/armour and only share the resistance tail of mitigation.
type DoT struct {
	Type      DamageType
	PerTick   fm.Fixed
	TicksLeft uint32
	Source    EntityID
}

type Actor struct {
	ID    EntityID
	Def   *ActorDef
	Team  Team
	Pos   space.Vec2
	Sheet *stats.Sheet

	// Current resource pools; maxima come from the stat sheet.
	Life, Mana, ES fm.Fixed

	Action Action
	DoTs   []DoT

	// Equipment by concrete slot; nil = empty. Equipped items grant their
	// affixes as sheet modifiers sourced by the item's ID.
	Equipment [EquipSlotCount]*Item

	// Inventory is a flat ID-addressed bag (no spatial grid), capacity
	// capped by the def's InventorySize. Order is insertion order and is
	// part of world state (hashed).
	Inventory []Item

	// Dead actors are tombstoned during the tick and compacted at tick end,
	// so slice indices stay stable while a tick is in flight.
	Dead bool
}

func (a *Actor) MaxLife() fm.Fixed { return a.Sheet.Eval(stats.Life, 0) }
func (a *Actor) MaxMana() fm.Fixed { return a.Sheet.Eval(stats.Mana, 0) }
func (a *Actor) MaxES() fm.Fixed   { return a.Sheet.Eval(stats.EnergyShield, 0) }

type Projectile struct {
	ID     EntityID
	Source EntityID // firing actor; may die before the projectile lands
	Team   Team
	Skill  *SkillDef
	Pos    space.Vec2
	Vel    space.Vec2 // per tick
	TicksLeft uint32
	Dead   bool
}

type Rarity uint8

const (
	RarityNormal Rarity = iota
	RarityMagic
	RarityRare
)

func (r Rarity) String() string {
	switch r {
	case RarityMagic:
		return "magic"
	case RarityRare:
		return "rare"
	default:
		return "normal"
	}
}

type RolledAffix struct {
	Def   *AffixDef
	Value fm.Fixed
}

type Item struct {
	// ID is allocated at roll time from the world's entity counter and is
	// stable for the item's whole life — it is the modifier Source used to
	// cleanly remove the item's stats on unequip.
	ID      EntityID
	Base    *BaseItemDef
	Rarity  Rarity
	Affixes []RolledAffix
}

type Drop struct {
	ID   EntityID
	Pos  space.Vec2
	Item Item
	// Taken drops are tombstoned (picked up mid-tick) and compacted at
	// tick end, mirroring how dead actors work.
	Taken bool
}

// EquipSlot is a concrete equipment slot on an actor.
type EquipSlot uint8

const (
	EquipRing1 EquipSlot = iota
	EquipRing2
	EquipBelt

	EquipSlotCount
)

func (s EquipSlot) String() string {
	switch s {
	case EquipRing1:
		return "ring1"
	case EquipRing2:
		return "ring2"
	default:
		return "belt"
	}
}

// SlotsFor maps an item's slot family to the concrete slots it can occupy,
// in fill-preference order. Read-only package data.
func SlotsFor(f SlotFamily) []EquipSlot {
	switch f {
	case FamilyRing:
		return ringSlots
	default:
		return beltSlots
	}
}

var (
	ringSlots = []EquipSlot{EquipRing1, EquipRing2}
	beltSlots = []EquipSlot{EquipBelt}
)

// Hit is one resolved-or-pending strike flowing through the damage pipeline.
// It is created with attacker/skill identity; the pipeline fills in outcomes.
type Hit struct {
	Attacker, Defender EntityID
	Skill              *SkillDef
	Tags               stats.TagSet

	// Outcomes, populated by the pipeline.
	Damage  [DamageTypeCount]fm.Fixed
	Crit    bool
	Evaded  bool
	Ignited bool
}
