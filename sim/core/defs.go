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
	// LeashRadius bounds the actor's territory around Home: it only engages
	// enemies standing within this range of Home, and walks back home when
	// nothing qualifies. 0 = unleashed. Keep it above AggroRadius so fights
	// that start inside the territory can play out.
	LeashRadius fm.Fixed
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

	// Families is the slot families this affix can roll on; nil means any
	// slot. content.DB() asserts every family keeps a pool deep enough to
	// fill a rare.
	Families []SlotFamily
}

// AllowedOn reports whether the affix can roll on an item of family f.
func (af *AffixDef) AllowedOn(f SlotFamily) bool {
	if len(af.Families) == 0 {
		return true
	}
	for _, allowed := range af.Families {
		if allowed == f {
			return true
		}
	}
	return false
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
// MonsterModDef is one rarity modifier a magic or rare monster can spawn
// with: a named, permanent package of stat modifiers. Unlike a BuffDef it
// has no duration and is never removed, so every instance shares one sheet
// source (MonsterModSource).
type MonsterModDef struct {
	ID   string
	Name string // display fragment: "Fleet", "Brawny"
	Mods []BuffMod
}

type ContentDB struct {
	Skills     map[string]*SkillDef
	Actors     map[string]*ActorDef
	Affixes    []*AffixDef
	BaseItems  map[string]*BaseItemDef
	LootTables map[string]*LootTableDef
	Buffs      map[string]*BuffDef
	// MonsterMods is ordered — rarity rolls index into it, so reordering
	// is a replay-relevant change (same rule as the affix table).
	MonsterMods []*MonsterModDef
}

// MonsterMod resolves a rarity-modifier ID; nil if unknown. Linear scan —
// the table is a handful of entries and saves resolve it once at load.
func (db *ContentDB) MonsterMod(id string) *MonsterModDef {
	for _, m := range db.MonsterMods {
		if m.ID == id {
			return m
		}
	}
	return nil
}
