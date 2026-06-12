package core

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// TicksPerSecond is the fixed simulation rate. All content durations are
// authored in ticks; seconds exist only at authoring/display boundaries.
const TicksPerSecond = 30

type Team uint8

const (
	TeamNone Team = iota
	TeamPlayers
	TeamMonsters
)

type DamageType uint8

const (
	Physical DamageType = iota
	Fire
	Cold
	Lightning
	Chaos

	DamageTypeCount
)

func (d DamageType) Tag() stats.Tag {
	switch d {
	case Physical:
		return stats.TagPhysical
	case Fire:
		return stats.TagFire
	case Cold:
		return stats.TagCold
	case Lightning:
		return stats.TagLightning
	default:
		return stats.TagChaos
	}
}

func (d DamageType) String() string {
	switch d {
	case Physical:
		return "physical"
	case Fire:
		return "fire"
	case Cold:
		return "cold"
	case Lightning:
		return "lightning"
	default:
		return "chaos"
	}
}

type SkillKind uint8

const (
	SkillProjectile SkillKind = iota
	SkillMelee
	// SkillNova hits every hostile actor within AoERadius of the caster at
	// the effect point. Self-centered; AimPoint is ignored.
	SkillNova
	// SkillBuff applies the skill's SelfBuff to the caster at the effect
	// point. No hits, no targets, no RNG.
	SkillBuff
)

type SkillDef struct {
	ID   string
	Name string
	Kind SkillKind
	Tags stats.TagSet

	// Base damage rolled per hit; zero max means the type isn't rolled.
	BaseMin, BaseMax [DamageTypeCount]fm.Fixed
	// Effectiveness scales flat added damage from gear/buffs (One = 100%).
	Effectiveness fm.Fixed

	ManaCost fm.Fixed
	// Tick counts at base speed; scaled by SpeedStat at use time.
	WindupTicks, RecoveryTicks uint32
	SpeedStat                  stats.StatID // CastSpeed or AttackSpeed

	// Melee: reach measured between collision circle edges.
	Range fm.Fixed

	// Projectile fields.
	ProjSpeed  fm.Fixed // units per second
	ProjTTL    uint32   // ticks
	ProjRadius fm.Fixed

	// Nova field: blast radius measured from the caster's center to the
	// target's circle edge.
	AoERadius fm.Fixed

	// Base chance for fire-damage hits to ignite, before IgniteChance stats.
	IgniteChance fm.Fixed
	// Base chance for lightning-damage hits to shock, before ShockChance
	// stats. Chill has no chance: every cold-damage hit chills, magnitude
	// scaled by hit size.
	ShockChance fm.Fixed

	// SelfBuff names the BuffDef a SkillBuff skill applies to its caster.
	SelfBuff string
}

// BuffMod is one modifier a buff grants, exactly as authored — buffs are
// fixed packages, unlike ailments whose magnitude scales with the hit.
type BuffMod struct {
	Stat  stats.StatID
	Layer stats.Layer
	Value fm.Fixed
	Tags  stats.TagSet
}

// BuffDef is a content-defined timed status: a package of stat modifiers
// with a duration. Reapplication refreshes the timer; one instance per def
// per actor.
type BuffDef struct {
	ID            string
	Name          string
	DurationTicks uint32
	Mods          []BuffMod
}

// ModSource is the sheet source a buff's modifiers are granted under. The
// top two bits mark buff-space — disjoint from entity IDs (high bit clear)
// and ailment sources (bit 62 clear) — and the rest is an FNV-1a of the
// buff ID, so the source survives content reordering and save/restore.
// content.DB() asserts no two buffs collide.
func (b *BuffDef) ModSource() uint64 {
	h := uint64(fnvOffset)
	for i := 0; i < len(b.ID); i++ {
		h ^= uint64(b.ID[i])
		h *= fnvPrime
	}
	return 3<<62 | h&^(uint64(3)<<62)
}

type ActorDef struct {
	ID     string
	Name   string
	Team   Team
	Radius fm.Fixed

	BaseStats [stats.StatCount]fm.Fixed

	// Level the actor spawns at (0 means 1). Per-level growth comes from
	// PerLevel; players then climb via XP, monsters stay where content (or
	// a future floor-scaling spawner) puts them.
	Level int
	// XPValue is the XP granted to this actor's killer (0 = none).
	XPValue int64
	// PerLevel is the growth package: each modifier is added to the sheet
	// scaled by (level-1), under LevelModSource.
	PerLevel []BuffMod

	Skills []string // skill IDs this actor may use
	// AI behavior key; "" means externally controlled (player) or inert.
	AI          string
	AggroRadius fm.Fixed
	// PreferredRange is the engagement distance for ranged behaviors: shoot
	// inside it, retreat when an enemy closes within a third of it.
	PreferredRange fm.Fixed
	LootTable      string // "" drops nothing
	// InventorySize is the bag capacity; 0 means the actor carries nothing.
	InventorySize int
}

type AffixKind uint8

const (
	Prefix AffixKind = iota
	Suffix
)

type AffixDef struct {
	ID    string
	Group string // at most one affix per group on an item
	Kind  AffixKind

	// The modifier this affix grants when the item is equipped.
	Stat  stats.StatID
	Layer stats.Layer
	Tags  stats.TagSet

	Min, Max fm.Fixed
	Weight   uint32
}

// SlotFamily is the kind of slot a base item occupies; families with
// multiple concrete slots (rings) are resolved at equip time.
type SlotFamily uint8

const (
	FamilyWeapon SlotFamily = iota
	FamilyOffhand
	FamilyHelmet
	FamilyBody
	FamilyGloves
	FamilyBoots
	FamilyAmulet
	FamilyRing
	FamilyBelt
)

// ImplicitDef is the modifier a base item type always carries, rolled in
// [Min, Max] when the item drops. ID is a display key for clients (and the
// save format does not need it — the value lives on the Item, the def on
// the base).
type ImplicitDef struct {
	ID    string
	Stat  stats.StatID
	Layer stats.Layer
	Tags  stats.TagSet

	Min, Max fm.Fixed
}

type BaseItemDef struct {
	ID       string
	Name     string
	Slot     SlotFamily
	Implicit *ImplicitDef // nil = no implicit
}

type LootTableDef struct {
	ID         string
	DropChance fm.Fixed
	Bases      []string
	// RarityWeights is the normal/magic/rare weighted draw, indexed by
	// Rarity. All-zero falls back to normal-only — a content table should
	// always set it.
	RarityWeights [3]uint32
}

// ContentDB is the registry of definitions the world runs against. The sim
// only reads it; content authoring lives outside sim/. Maps are lookup-only —
// Affixes is a slice because loot rolling iterates it (weighted, in order).
type ContentDB struct {
	Skills     map[string]*SkillDef
	Actors     map[string]*ActorDef
	Affixes    []*AffixDef
	BaseItems  map[string]*BaseItemDef
	LootTables map[string]*LootTableDef
	Buffs      map[string]*BuffDef
}
