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
	// SkillChain is hitscan: at the effect point it instantly strikes the
	// enemy nearest the aim point (within Range of the caster, LoS-gated),
	// then chains to Chains more nearby enemies. No projectile exists.
	SkillChain
	// SkillSummon queues SummonCount minions of SummonDef at the effect
	// point (spawn queue; they materialize the same tick, act the next),
	// owned by the caster at the gem's level. Past SummonCap, the oldest
	// minion of that def despawns quietly — no death, no loot, no XP.
	SkillSummon
	// SkillStaged runs a scripted sequence of stages (SkillDef.Stages)
	// instead of the windup/effect/recovery arc: each stage is a tick
	// countdown ending in an effect at an aim point locked when the stage
	// begins. This is how telegraphed multi-stage boss attacks are authored —
	// the telegraph shows the locked zone, and you dodge by leaving it
	// before the countdown ends. Recovery is just a trailing effect-less
	// stage. The caster is committed for the whole sequence.
	SkillStaged
	// SkillAura toggles the gem's aura at the effect point: while on, the
	// caster reserves part of max mana (Reserve) and the AuraMods package
	// sits on the caster's sheet and every owned minion's — no radius, no
	// duration, no RNG. Gem-only: monsters can't run auras.
	SkillAura
	// SkillCurse hexes every hostile within AoERadius of the aim point
	// (clamped to Range from the caster) with the CurseBuff — a Curse
	// BuffDef on the ordinary pending-buff queue. One curse per target:
	// applying a second evicts the first. No hits, no RNG.
	SkillCurse
	// SkillBlink teleports the caster toward the aim point: clamped to
	// Range, stopped by walls (furthest clear, walkable landing along the
	// line). Usually cooldown-gated — the reposition you can't spam.
	SkillBlink
)

// StageEffect is what fires when a stage's countdown ends.
type StageEffect uint8

const (
	// StageNone fires nothing — a pause between hits, or the recovery tail.
	StageNone StageEffect = iota
	// StageBlast hits every enemy within Radius of the stage's aim point
	// (measured to their circle edge, walls ignored like novas).
	StageBlast
	// StageRing fires the skill's projectiles in a circle around the caster,
	// one every RingStep fan steps (12° each), the first aimed at the
	// stage's aim point.
	StageRing
)

// StageAimKind is where a stage's aim point locks when the stage begins.
type StageAimKind uint8

const (
	// StageAimTarget locks onto the action target's position at stage start
	// (the attack tracks a moving player between stages); falls back to the
	// cast's AimPoint when the target is gone.
	StageAimTarget StageAimKind = iota
	// StageAimSelf locks onto the caster's position at stage start.
	StageAimSelf
	// StageAimPoint keeps the cast's original AimPoint for every stage.
	StageAimPoint
)

// SkillStage is one step of a staged skill: wait Ticks (the telegraph
// window — bound at use time like RecoveryTicks), then fire Effect at the
// stage's locked aim. A zero-tick stage fires one tick after its
// predecessor (the countdown is checked once per tick).
type SkillStage struct {
	Ticks  uint32
	Effect StageEffect
	Aim    StageAimKind
	// Radius is the blast's hit radius — and the telegraph radius the
	// client renders. Zero on a ring stage means no ground telegraph.
	Radius fm.Fixed
	// DamageScale multiplies the stage's hits (via Hit.AreaScale); zero
	// means full damage. Lets a finisher stage hit harder than the setup.
	DamageScale fm.Fixed
	// RingStep is the fan-step spacing between ring projectiles (1–3, i.e.
	// 12°–36°); a full circle is 30 fan steps, so step 3 fires 10.
	RingStep int
	// RingSkew rotates the whole ring by this many fan steps, so a second
	// ring's spokes can bisect the first ring's gaps.
	RingSkew int
}

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

	// Melee: reach measured between collision circle edges. Chain skills:
	// the target-acquisition range from the caster.
	Range fm.Fixed

	// Projectile fields.
	ProjSpeed  fm.Fixed // units per second
	ProjTTL    uint32   // ticks
	ProjRadius fm.Fixed
	// ExplodeRadius makes projectile impacts detonate: every other enemy
	// within it of the impact point takes the hit again, scaled down
	// linearly with distance (Hit.AreaScale). Zero = no explosion.
	ExplodeRadius fm.Fixed
	// Bounce reflects the projectile off walls instead of dying, until its
	// TTL runs out.
	Bounce bool
	// WigglePeriod nudges the projectile's heading by a small random angle
	// (combat stream) every WigglePeriod ticks of flight. Zero = flies true.
	WigglePeriod uint32

	// Chains is a chain skill's extra targets after the first (supports'
	// chain counts add on top).
	Chains int

	// Nova field: blast radius measured from the caster's center to the
	// target's circle edge.
	AoERadius fm.Fixed

	// Base chance for fire-damage hits to ignite, before IgniteChance stats.
	IgniteChance fm.Fixed
	// Base chance for lightning-damage hits to shock, before ShockChance
	// stats. Chill has no chance: every cold-damage hit chills, magnitude
	// scaled by hit size.
	ShockChance fm.Fixed
	// Base chance for physical-damage hits to bleed, before BleedChance
	// stats. Unlike ignite/shock, the roll happens only when the total
	// chance is positive — physical damage is everywhere, and a draw per
	// phys hit would shift every replay (block's conditional-consumption
	// discipline).
	BleedChance fm.Fixed
	// Base chance for hits carrying physical or chaos damage to poison,
	// before PoisonChance stats. Same conditional-consumption rule as
	// bleed. Poison stacks: every application is its own DoT instance.
	PoisonChance fm.Fixed

	// Aura fields (SkillAura): Reserve is the fraction of max mana held
	// while the aura runs (a More(-Reserve) mod on Mana, removed at toggle
	// off); AuraMods is the package granted to the caster and every owned
	// minion, values scaled by GemAuraScale of the gem's level.
	Reserve  fm.Fixed
	AuraMods []BuffMod

	// Channel fields: ChannelTicks > 0 makes the skill channelled — after
	// the windup fires the first effect, the action holds in PhaseChannel
	// and repeats the effect every interval (bound at use time, speed-
	// scaled) as long as ChannelMana is paid per repeat. The channel
	// breaks on a new move/skill command, a stop, a stun, or an unpaid
	// repeat. ManaCost is still the up-front cost of starting.
	ChannelTicks uint32
	ChannelMana  fm.Fixed

	// CooldownTicks > 0 gates re-use: the command validator refuses the
	// skill while the caster's cooldown for it runs. Started on cast
	// acceptance, never speed-scaled, zone-local (transfers arrive clear).
	CooldownTicks uint32

	// SelfBuff names the BuffDef a SkillBuff skill applies to its caster.
	SelfBuff string
	// CurseBuff names the Curse BuffDef a SkillCurse skill lands on every
	// hostile in its area.
	CurseBuff string

	// Summon fields (SkillSummon): which def, how many per cast, and the
	// per-caster cap for that def. SummonTTL > 0 makes the minions
	// short-lived: they despawn quietly (no death, no loot, no XP) that
	// many ticks after materializing.
	SummonDef   string
	SummonCount int
	SummonCap   int
	SummonTTL   uint32

	// Stages is a SkillStaged skill's sequence, in play order. Staged
	// skills leave WindupTicks/RecoveryTicks zero — every phase, recovery
	// included, is a stage.
	Stages []SkillStage

	// Cuttable marks skills an uncut skill gem's draft can offer — the
	// player-appropriate subset of the table.
	Cuttable bool
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
	// Curse marks a hex: it lands on enemies, counts against the
	// one-curse-per-target cap (newest evicts), and renders as a curse,
	// not a buff. Same machinery otherwise.
	Curse bool
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

// AuraModSource is the sheet source an aura's modifiers (and its caster's
// reservation) are granted under: bits 63+59 mark aura-space — bit 62 clear
// keeps it out of buff- and growth-space, bits 61/60 clear out of monster-
// mod- and passive-space, and the low-bits FNV of the skill ID survives
// content reordering and save/restore. content.DB() asserts no two auras
// collide.
func (sk *SkillDef) AuraModSource() uint64 {
	h := uint64(fnvOffset)
	for i := 0; i < len(sk.ID); i++ {
		h ^= uint64(sk.ID[i])
		h *= fnvPrime
	}
	return 1<<63 | 1<<59 | h&^(uint64(0x1F)<<59)
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
	// Flasks names the buff each flask slot applies when drunk (PoE1-style
	// recovery: a charges-gated regen burst). Empty = the actor has none.
	// Slot order is wire/command order.
	Flasks []string
	// StartingGems are skill IDs cut as level-1 gems at spawn — how a
	// scenario-scripted actor can act at all in a gems-only world.
	// Monsters don't need them; their Def.Skills work directly.
	StartingGems []string
	// StartingUncut grants this many level-1 uncut skill gems (draft of
	// three pre-rolled from the loot stream) into the bag at spawn — a
	// fresh character picks its own first skill instead of inheriting one.
	// Granted by sim.Spawn, not at injection: transfers carry the bag.
	StartingUncut int
	// DeathSpawnDef/DeathSpawnCount: on death, this many adds of that def
	// are queued at the corpse (fixed offsets, the dier's level) and
	// materialize at the tick's spawn phase — the splitter archetype.
	// content.DB() rejects cyclic chains; the adds grant their own XP.
	DeathSpawnDef   string
	DeathSpawnCount int
	// StunImmune keeps big hits from interrupting this actor's actions —
	// bosses set it so a crit can't cancel a telegraphed set-piece.
	StunImmune bool
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
	// Step quantizes the roll: values land on Min + k·Step, so a resist
	// rolls whole percents and a flat armour whole points — the tooltip
	// never has to lie about sub-display precision. 0 = raw milli rolls.
	Step   fm.Fixed
	Weight uint32

	// ILvl is the minimum item level this affix can roll on (0 = always).
	// Higher tiers of a group carry higher gates, so deeper drops unlock
	// strictly better affixes. Every group must keep an ILvl-0 base tier
	// so low-level items never starve (content.DB() asserts the depth).
	ILvl int

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
	Step     fm.Fixed // roll quantum, like AffixDef.Step (0 = raw milli)
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
	// Uncut-gem drop chances per kill, in permille, scaled by the dier's
	// rarity like orbs (×2 magic, ×3 rare). One combined draw decides
	// skill-or-support; both zero consumes no RNG.
	SkillGemPermille, SupportGemPermille uint32
	// UniquePermille is the chance a drop upgrades to a unique (uniform
	// pick from Content.Uniques, which overrides the base). Zero consumes
	// no RNG.
	UniquePermille uint32
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

// UniqueDef is a chase item: a fixed identity on a fixed base with a fixed
// modifier package — including stats no affix can roll (extra projectiles,
// extra chains), which is what makes one build-defining rather than just
// bigger. ModLines is the authored display text the client shows verbatim.
type UniqueDef struct {
	ID       string
	Name     string
	Desc     string // flavor line
	Base     string // BaseItemDef ID this unique always drops as
	Mods     []BuffMod
	ModLines []string
}

// PassiveDef is one fork of a level-milestone choice: a named, permanent
// package of stat modifiers. Reaching Milestone unlocks the pick; an actor
// takes at most one passive per milestone, and it never comes off.
type PassiveDef struct {
	ID        string
	Name      string
	Desc      string // one line the chooser UI shows
	Milestone int    // level that unlocks this fork
	Mods      []BuffMod
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
	// Passives is ordered for stable presentation; lookups go by ID.
	Passives []*PassiveDef
	// Supports is ordered — uncut-gem drafts roll indices into it, so
	// reordering is replay-relevant like the affix table.
	Supports []*SupportDef
	// Cuttable is the ordered draft pool for uncut skill gems: every skill
	// with Cuttable set, in table order. Built by the content package.
	Cuttable []*SkillDef
	// Uniques is ordered — unique drops roll indices into it, so
	// reordering is replay-relevant like the affix table.
	Uniques []*UniqueDef
}

// Unique resolves a unique-item ID; nil if unknown. Linear scan, same
// reasoning as MonsterMod.
func (db *ContentDB) Unique(id string) *UniqueDef {
	for _, u := range db.Uniques {
		if u.ID == id {
			return u
		}
	}
	return nil
}

// Support resolves a support-gem ID; nil if unknown. Linear scan, same
// reasoning as MonsterMod.
func (db *ContentDB) Support(id string) *SupportDef {
	for _, s := range db.Supports {
		if s.ID == id {
			return s
		}
	}
	return nil
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

// Passive resolves a passive ID; nil if unknown. Linear scan, same
// reasoning as MonsterMod.
func (db *ContentDB) Passive(id string) *PassiveDef {
	for _, p := range db.Passives {
		if p.ID == id {
			return p
		}
	}
	return nil
}
