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
}

type ActorDef struct {
	ID     string
	Name   string
	Team   Team
	Radius fm.Fixed

	BaseStats [stats.StatCount]fm.Fixed

	Skills []string // skill IDs this actor may use
	// AI behavior key; "" means externally controlled (player) or inert.
	AI          string
	AggroRadius fm.Fixed
	LootTable   string // "" drops nothing
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
	FamilyRing SlotFamily = iota
	FamilyBelt
)

type BaseItemDef struct {
	ID   string
	Name string
	Slot SlotFamily
}

type LootTableDef struct {
	ID         string
	DropChance fm.Fixed
	Bases      []string
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
}
