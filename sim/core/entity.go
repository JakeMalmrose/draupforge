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

	// MoveTarget is the requested destination. On grid worlds movement
	// follows Path instead (waypoints from the pathfinder, ending at the
	// closest reachable approach to MoveTarget); MoveTarget is kept so a
	// re-issued move to (nearly) the same place doesn't repath every tick.
	MoveTarget space.Vec2
	Path       []space.Vec2
	PathStep   int

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

// StatusKind discriminates the timed-status container's entries: built-in
// ailments (magnitude scales with the hit, strongest wins) and
// content-defined buffs (fixed modifier packages, duration refreshes). All
// of them are packages of stat modifiers on a timer — DoTs deal damage and
// touch no stats, so they live elsewhere.
type StatusKind uint8

const (
	StatusChill StatusKind = iota // less action speed, from cold hits
	StatusShock                   // increased damage taken, from lightning hits
	StatusBuff                    // content-defined: Status.Buff names the def

	StatusKindCount
)

func (k StatusKind) String() string {
	switch k {
	case StatusChill:
		return "chill"
	case StatusShock:
		return "shock"
	default:
		return "buff"
	}
}

// ModSource is the sheet modifier source an ailment grants its modifiers
// under. The high bit keeps it disjoint from entity IDs (monotonic from 1);
// bit 62 stays clear, which is what keeps it disjoint from BuffDef sources.
func (k StatusKind) ModSource() uint64 { return 1<<63 | uint64(k) }

// Status is one active timed status. Its gameplay effect lives on the
// actor's sheet as modifiers under ModSource(); the Status itself records
// magnitude and remaining time so stronger applications can replace it
// (ailments) or refresh it (buffs), and expiry knows which modifiers to
// remove. One per ailment kind; one per buff def.
type Status struct {
	Kind      StatusKind
	Buff      *BuffDef // set iff Kind == StatusBuff
	Magnitude fm.Fixed // ailments — fraction: 0.30 = 30% slow / 30% increased taken
	TicksLeft uint32
	Source    EntityID // who inflicted it
}

// ModSource is the sheet source this status granted its modifiers under.
func (s Status) ModSource() uint64 {
	if s.Buff != nil {
		return s.Buff.ModSource()
	}
	return s.Kind.ModSource()
}

type Actor struct {
	ID    EntityID
	Def   *ActorDef
	Team  Team
	Pos   space.Vec2
	Sheet *stats.Sheet

	// Home is where the actor spawned — the anchor of a leashed monster's
	// territory (see ActorDef.LeashRadius). Zone-local like Pos: transfers
	// re-anchor it at the injection point. Saved and hashed; the minimal
	// per-actor AI state RISKS.md said leashing would need.
	Home space.Vec2

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

	// Progression: Level drives the Def.PerLevel growth modifiers; XP is
	// progress into the current level (plain int64 — accumulators stay out
	// of Fixed, see RISKS.md). Monsters keep their spawn level and XP 0.
	Level int
	XP    int64

	// Rarity and Mods: rolled at spawn for magic/rare monsters (players
	// stay RarityNormal). The mods' stat packages live on the sheet under
	// MonsterModSource for the actor's whole life — nothing removes them.
	Rarity Rarity
	Mods   []*MonsterModDef

	// Passives: milestone choices taken, in pick order — durable character
	// state (transfers across zones, unlike everything else status-shaped).
	// Their stat packages live on the sheet under PassiveModSource.
	Passives []*PassiveDef

	// FlaskCharges tracks each flask slot's charges (parallel to
	// Def.Flasks). Spent on use, gained on kills, durable across zones.
	FlaskCharges []int32

	// Dead actors are tombstoned during the tick and compacted at tick end,
	// so slice indices stay stable while a tick is in flight.
	Dead bool
}

// Flask economy (open for tuning): a use costs half the cap, kills feed a
// sixth back, so sustained fighting keeps roughly one sip banked.
const (
	FlaskMaxCharges  int32 = 60
	FlaskUseCost     int32 = 30
	FlaskGainPerKill int32 = 10
)

// LevelModSource is the sheet source for per-level growth modifiers: bit 62
// alone, disjoint from entity IDs (top bits clear), ailment sources (bit 63
// set, 62 clear), and buff sources (both top bits set).
const LevelModSource uint64 = 1 << 62

// MonsterModSource is the shared sheet source for rarity-mod packages:
// bits 63 and 61 (62 clear) — disjoint from entity IDs (top bits clear),
// LevelModSource (bit 62), ailment sources (bit 63 | kind, kind < 8), and
// buff sources (bits 63+62). One source for all rarity mods is enough
// because they are permanent — nothing ever removes them individually.
const MonsterModSource uint64 = 1<<63 | 1<<61

// PassiveModSource is the shared sheet source for milestone passives —
// bit 63 with bit 60 alone in the low-space, disjoint from every other
// source space (see MonsterModSource). Shared for the same reason: a
// taken passive never comes off.
const PassiveModSource uint64 = 1<<63 | 1<<60

// HasMilestone reports whether the actor already took a passive of the
// given milestone.
func (a *Actor) HasMilestone(m int) bool {
	for _, p := range a.Passives {
		if p.Milestone == m {
			return true
		}
	}
	return false
}

// TakePassive grants a milestone passive: records it and installs its
// modifiers under PassiveModSource. Callers gate on level and milestone
// (the command validator, character injection).
func (a *Actor) TakePassive(def *PassiveDef) {
	a.Passives = append(a.Passives, def)
	for _, m := range def.Mods {
		a.Sheet.Add(stats.Modifier{
			Stat:   m.Stat,
			Layer:  m.Layer,
			Value:  m.Value,
			Tags:   m.Tags,
			Source: PassiveModSource,
		})
	}
}

// ApplyMonsterMods sets the actor's rarity and grants each modifier
// package on the sheet under MonsterModSource. Callers refill pools
// afterwards — Life maxima may have grown.
func (a *Actor) ApplyMonsterMods(r Rarity, mods []*MonsterModDef) {
	a.Rarity = r
	a.Mods = mods
	for _, md := range mods {
		for _, m := range md.Mods {
			a.Sheet.Add(stats.Modifier{
				Stat:   m.Stat,
				Layer:  m.Layer,
				Value:  m.Value,
				Tags:   m.Tags,
				Source: MonsterModSource,
			})
		}
	}
}

// SetLevel sets the actor's level (clamped to ≥1) and rebuilds its
// per-level growth modifiers: Def.PerLevel scaled by (level-1) under
// LevelModSource. Pools are not touched — level-up rewards are the caller's
// policy, not bookkeeping.
func (a *Actor) SetLevel(level int) {
	if level < 1 {
		level = 1
	}
	a.Level = level
	a.Sheet.RemoveSource(LevelModSource)
	if level == 1 {
		return
	}
	for _, m := range a.Def.PerLevel {
		a.Sheet.Add(stats.Modifier{
			Stat:   m.Stat,
			Layer:  m.Layer,
			Value:  fm.Fixed(int64(m.Value) * int64(level-1)),
			Tags:   m.Tags,
			Source: LevelModSource,
		})
	}
}

// AddItemMods grants an equipped item's implicit and affix modifiers on the
// actor's sheet, sourced by the item's ID so a later unequip (or zone exit)
// can remove them cleanly with RemoveSource.
func (a *Actor) AddItemMods(item *Item) {
	if imp := item.Base.Implicit; imp != nil {
		a.Sheet.Add(stats.Modifier{
			Stat:   imp.Stat,
			Layer:  imp.Layer,
			Value:  item.Implicit,
			Tags:   imp.Tags,
			Source: uint64(item.ID),
		})
	}
	for _, af := range item.Affixes {
		a.Sheet.Add(stats.Modifier{
			Stat:   af.Def.Stat,
			Layer:  af.Def.Layer,
			Value:  af.Value,
			Tags:   af.Def.Tags,
			Source: uint64(item.ID),
		})
	}
}

func (a *Actor) MaxLife() fm.Fixed { return a.Sheet.Eval(stats.Life, stats.TagSet{}) }
func (a *Actor) MaxMana() fm.Fixed { return a.Sheet.Eval(stats.Mana, stats.TagSet{}) }
func (a *Actor) MaxES() fm.Fixed   { return a.Sheet.Eval(stats.EnergyShield, stats.TagSet{}) }

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
	// Implicit is the rolled value of Base.Implicit; zero (and meaningless)
	// when the base has none.
	Implicit fm.Fixed
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
