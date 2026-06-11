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

// StatusKind enumerates the non-damaging ailments: timed packages of stat
// modifiers, as opposed to DoTs which deal damage and touch no stats.
type StatusKind uint8

const (
	StatusChill StatusKind = iota // less action speed, from cold hits
	StatusShock                   // increased damage taken, from lightning hits

	StatusKindCount
)

func (k StatusKind) String() string {
	switch k {
	case StatusChill:
		return "chill"
	default:
		return "shock"
	}
}

// ModSource is the sheet modifier source a status grants its modifiers
// under. The high bit keeps it disjoint from entity IDs (monotonic from 1),
// which are the only other modifier sources in play.
func (k StatusKind) ModSource() uint64 { return 1<<63 | uint64(k) }

// Status is one active ailment. Its gameplay effect lives on the actor's
// sheet as modifiers under Kind.ModSource(); the Status itself records
// magnitude and remaining time so stronger applications can replace it and
// expiry knows which modifiers to remove. One per kind, strongest wins.
type Status struct {
	Kind      StatusKind
	Magnitude fm.Fixed // fraction: 0.30 = 30% slow / 30% increased taken
	TicksLeft uint32
	Source    EntityID // who inflicted it
}

type Actor struct {
	ID    EntityID
	Def   *ActorDef
	Team  Team
	Pos   space.Vec2
	Sheet *stats.Sheet

	// Current resource pools; maxima come from the stat sheet.
	Life, Mana, ES fm.Fixed

	Action   Action
	DoTs     []DoT
	Statuses []Status

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
	ID        EntityID
	Source    EntityID // firing actor; may die before the projectile lands
	Team      Team
	Skill     *SkillDef
	Pos       space.Vec2
	Vel       space.Vec2 // per tick
	TicksLeft uint32
	Dead      bool
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
	EquipWeapon EquipSlot = iota
	EquipOffhand
	EquipHelmet
	EquipBody
	EquipGloves
	EquipBoots
	EquipAmulet
	EquipRing1
	EquipRing2
	EquipBelt

	EquipSlotCount

	// EquipAuto is the "server picks" sentinel for equip commands that
	// don't name a slot: first empty slot in family preference order.
	EquipAuto = EquipSlotCount
)

var equipSlotNames = [EquipSlotCount]string{
	"weapon", "offhand", "helmet", "body", "gloves", "boots",
	"amulet", "ring1", "ring2", "belt",
}

func (s EquipSlot) String() string {
	if s < EquipSlotCount {
		return equipSlotNames[s]
	}
	return "auto"
}

// ParseEquipSlot maps a wire slot name back to the enum; ok is false for
// anything that isn't exactly a concrete slot name.
func ParseEquipSlot(name string) (EquipSlot, bool) {
	for s := EquipSlot(0); s < EquipSlotCount; s++ {
		if equipSlotNames[s] == name {
			return s, true
		}
	}
	return EquipAuto, false
}

// SlotsFor maps an item's slot family to the concrete slots it can occupy,
// in fill-preference order. Read-only package data.
func SlotsFor(f SlotFamily) []EquipSlot {
	return familySlots[f]
}

var familySlots = [...][]EquipSlot{
	FamilyWeapon:  {EquipWeapon},
	FamilyOffhand: {EquipOffhand},
	FamilyHelmet:  {EquipHelmet},
	FamilyBody:    {EquipBody},
	FamilyGloves:  {EquipGloves},
	FamilyBoots:   {EquipBoots},
	FamilyAmulet:  {EquipAmulet},
	FamilyRing:    {EquipRing1, EquipRing2},
	FamilyBelt:    {EquipBelt},
}

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
	Chilled bool
	Shocked bool
}
