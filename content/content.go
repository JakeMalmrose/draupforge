// Package content holds the game's data tables as typed Go literals —
// compile-time checked, zero parsers. Definitions are pure data the sim
// consumes; the sim never imports this package, hosts wire it in.
package content

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// DB builds the v1 content set: the vertical-slice skills, actors, and loot.
func DB() *core.ContentDB {
	db := &core.ContentDB{
		Skills:     map[string]*core.SkillDef{},
		Actors:     map[string]*core.ActorDef{},
		BaseItems:  map[string]*core.BaseItemDef{},
		LootTables: map[string]*core.LootTableDef{},
	}
	for _, sk := range skillDefs() {
		db.Skills[sk.ID] = sk
	}
	for _, a := range actorDefs() {
		db.Actors[a.ID] = a
	}
	db.Affixes = affixDefs()
	for _, b := range baseItemDefs() {
		db.BaseItems[b.ID] = b
	}
	// Per-slot pools must stay deep enough to fill a rare (3 prefixes + 3
	// suffixes from distinct groups) on every family a base occupies —
	// starving a pool is a content bug, caught here instead of at drop time.
	for _, b := range db.BaseItems {
		groups := [2]map[string]bool{{}, {}}
		for _, af := range db.Affixes {
			if af.AllowedOn(b.Slot) {
				groups[af.Kind][af.Group] = true
			}
		}
		if len(groups[core.Prefix]) < 3 || len(groups[core.Suffix]) < 3 {
			panic("content: affix pool too shallow for " + b.ID)
		}
	}
	for _, t := range lootTableDefs() {
		db.LootTables[t.ID] = t
	}
	db.Buffs = map[string]*core.BuffDef{}
	sources := map[uint64]string{}
	for _, b := range buffDefs() {
		// ModSource hashes the ID — astronomically unlikely to collide, but
		// a collision would silently merge two buffs' modifiers, so check.
		if other, dup := sources[b.ModSource()]; dup {
			panic("content: buff mod-source collision: " + b.ID + " vs " + other)
		}
		sources[b.ModSource()] = b.ID
		db.Buffs[b.ID] = b
	}
	db.MonsterMods = monsterModDefs()
	seen := map[string]bool{}
	for _, m := range db.MonsterMods {
		if m.ID == "" || len(m.Mods) == 0 {
			panic("content: monster mod " + m.ID + " is empty")
		}
		if seen[m.ID] {
			panic("content: duplicate monster mod " + m.ID)
		}
		seen[m.ID] = true
	}
	db.Passives = passiveDefs()
	seenPassive := map[string]bool{}
	milestoneForks := map[int]int{}
	for _, p := range db.Passives {
		if p.ID == "" || len(p.Mods) == 0 || p.Milestone < 2 {
			panic("content: passive " + p.ID + " is malformed")
		}
		if seenPassive[p.ID] {
			panic("content: duplicate passive " + p.ID)
		}
		seenPassive[p.ID] = true
		milestoneForks[p.Milestone]++
	}
	for m, n := range milestoneForks {
		if n < 2 {
			panic(fmt.Sprintf("content: milestone %d has %d fork(s); a choice needs at least 2", m, n))
		}
	}
	return db
}

// passiveDefs is the milestone-choice table: at each milestone level the
// player takes exactly one fork, permanently. Keep forks per milestone
// meaningfully different — this is the build-identity lever.
func passiveDefs() []*core.PassiveDef {
	return []*core.PassiveDef{
		{
			ID: "iron_constitution", Name: "Iron Constitution", Milestone: 5,
			Desc: "+40 life, +25 armour",
			Mods: []core.BuffMod{
				{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(40)},
				{Stat: stats.Armour, Layer: stats.LayerFlat, Value: fm.FromInt(25)},
			},
		},
		{
			ID: "keen_eye", Name: "Keen Eye", Milestone: 5,
			Desc: "+40 accuracy, +2% crit chance",
			Mods: []core.BuffMod{
				{Stat: stats.Accuracy, Layer: stats.LayerFlat, Value: fm.FromInt(40)},
				{Stat: stats.CritChance, Layer: stats.LayerFlat, Value: fm.FromMilli(20)},
			},
		},
		{
			ID: "clear_mind", Name: "Clear Mind", Milestone: 5,
			Desc: "+20 mana, 10% increased cast speed",
			Mods: []core.BuffMod{
				{Stat: stats.Mana, Layer: stats.LayerFlat, Value: fm.FromInt(20)},
				{Stat: stats.CastSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(100)},
			},
		},
		{
			ID: "executioner", Name: "Executioner", Milestone: 10,
			Desc: "20% increased damage",
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(200)},
			},
		},
		{
			ID: "shadow_step", Name: "Shadow Step", Milestone: 10,
			Desc: "8% increased move speed, +40 evasion",
			Mods: []core.BuffMod{
				{Stat: stats.MoveSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(80)},
				{Stat: stats.Evasion, Layer: stats.LayerFlat, Value: fm.FromInt(40)},
			},
		},
		{
			ID: "spellweaver", Name: "Spellweaver", Milestone: 10,
			Desc: "15% increased spell damage, +1 mana regen",
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagSpell), Value: fm.FromMilli(150)},
				{Stat: stats.ManaRegen, Layer: stats.LayerFlat, Value: fm.One},
			},
		},
	}
}

// monsterModDefs is the rarity-modifier pool: what a magic (one mod) or
// rare (two distinct mods) monster spawns with. Slice order feeds rarity
// rolls — reordering is a replay-relevant change, same as the affix table.
func monsterModDefs() []*core.MonsterModDef {
	return []*core.MonsterModDef{
		{
			ID: "fleet", Name: "Fleet",
			Mods: []core.BuffMod{
				{Stat: stats.MoveSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(400)},
				{Stat: stats.AttackSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(200)},
				{Stat: stats.CastSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(200)},
			},
		},
		{
			ID: "brawny", Name: "Brawny",
			Mods: []core.BuffMod{
				{Stat: stats.Life, Layer: stats.LayerIncreased, Value: fm.FromMilli(800)},
			},
		},
		{
			ID: "deadly", Name: "Deadly",
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(350)},
				{Stat: stats.CritChance, Layer: stats.LayerFlat, Value: fm.FromMilli(100)},
			},
		},
		{
			ID: "stalwart", Name: "Stalwart",
			Mods: []core.BuffMod{
				{Stat: stats.Armour, Layer: stats.LayerFlat, Value: fm.FromInt(60)},
				{Stat: stats.FireRes, Layer: stats.LayerFlat, Value: fm.FromMilli(300)},
				{Stat: stats.ColdRes, Layer: stats.LayerFlat, Value: fm.FromMilli(300)},
				{Stat: stats.LightningRes, Layer: stats.LayerFlat, Value: fm.FromMilli(300)},
			},
		},
	}
}

func buffDefs() []*core.BuffDef {
	return []*core.BuffDef{
		{
			ID:            "adrenaline",
			Name:          "Adrenaline",
			DurationTicks: 4 * core.TicksPerSecond,
			Mods: []core.BuffMod{
				{Stat: stats.MoveSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(300)},
				{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(200)},
			},
		},
		{
			// The life flask's sip: a strong regen burst, PoE1-style recovery
			// (100 life over 4s at base scale). Charges gate it, not mana.
			ID:            "life_flask",
			Name:          "Life Flask",
			DurationTicks: 4 * core.TicksPerSecond,
			Mods: []core.BuffMod{
				{Stat: stats.LifeRegen, Layer: stats.LayerFlat, Value: fm.FromInt(25)},
			},
		},
		{
			ID:            "mana_flask",
			Name:          "Mana Flask",
			DurationTicks: 4 * core.TicksPerSecond,
			Mods: []core.BuffMod{
				{Stat: stats.ManaRegen, Layer: stats.LayerFlat, Value: fm.FromInt(15)},
			},
		},
		{
			// Arrival protection after a death eject: hits deal nothing while
			// you reorient (DamageTaken overridden to zero also starves
			// ailments — their magnitudes scale off dealt damage). The host
			// grants it; no skill casts it.
			ID:            "portal_grace",
			Name:          "Portal Grace",
			DurationTicks: 5 * core.TicksPerSecond / 2, // 2.5s
			Mods: []core.BuffMod{
				{Stat: stats.DamageTaken, Layer: stats.LayerOverride, Value: 0},
			},
		},
	}
}

func skillDefs() []*core.SkillDef {
	fireball := &core.SkillDef{
		ID:            "fireball",
		Name:          "Fireball",
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagSpell, stats.TagProjectile, stats.TagFire),
		Effectiveness: fm.One,
		ManaCost:      fm.FromInt(10),
		WindupTicks:   15, // 0.5s base cast
		RecoveryTicks: 6,
		SpeedStat:     stats.CastSpeed,
		ProjSpeed:     fm.FromInt(20),
		ProjTTL:       60, // 2s flight
		ProjRadius:    fm.FromMilli(400),
		IgniteChance:  fm.FromMilli(250),
	}
	fireball.BaseMin[core.Fire] = fm.FromInt(20)
	fireball.BaseMax[core.Fire] = fm.FromInt(30)

	slam := &core.SkillDef{
		ID:            "zombie_slam",
		Name:          "Zombie Slam",
		Kind:          core.SkillMelee,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   18, // 0.6s telegraph
		RecoveryTicks: 12,
		SpeedStat:     stats.AttackSpeed,
		Range:         fm.FromMilli(1800),
	}
	slam.BaseMin[core.Physical] = fm.FromInt(8)
	slam.BaseMax[core.Physical] = fm.FromInt(12)

	frostNova := &core.SkillDef{
		ID:            "frost_nova",
		Name:          "Frost Nova",
		Kind:          core.SkillNova,
		Tags:          stats.T(stats.TagSpell, stats.TagCold),
		Effectiveness: fm.FromMilli(800), // AoE pays an added-damage tax
		ManaCost:      fm.FromInt(15),
		WindupTicks:   12, // 0.4s base cast
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		AoERadius:     fm.FromInt(4),
	}
	frostNova.BaseMin[core.Cold] = fm.FromInt(12)
	frostNova.BaseMax[core.Cold] = fm.FromInt(18)

	spark := &core.SkillDef{
		ID:            "spark",
		Name:          "Spark",
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagSpell, stats.TagProjectile, stats.TagLightning),
		Effectiveness: fm.One,
		ManaCost:      fm.FromInt(6),
		WindupTicks:   9, // 0.3s base cast: the fast, spammy option
		RecoveryTicks: 5,
		SpeedStat:     stats.CastSpeed,
		ProjSpeed:     fm.FromInt(28),
		ProjTTL:       40,
		ProjRadius:    fm.FromMilli(300),
		ShockChance:   fm.FromMilli(300), // 30%
	}
	// Lightning identity: wild rolls. Averages below fireball, pays for the
	// faster cast and the shock upside.
	spark.BaseMin[core.Lightning] = fm.FromInt(3)
	spark.BaseMax[core.Lightning] = fm.FromInt(28)

	boneArrow := &core.SkillDef{
		ID:            "bone_arrow",
		Name:          "Bone Arrow",
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagAttack, stats.TagProjectile, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   12, // 0.4s draw — dodgeable at range
		RecoveryTicks: 9,
		SpeedStat:     stats.AttackSpeed,
		ProjSpeed:     fm.FromInt(16),
		ProjTTL:       50,
		ProjRadius:    fm.FromMilli(350),
	}
	boneArrow.BaseMin[core.Physical] = fm.FromInt(5)
	boneArrow.BaseMax[core.Physical] = fm.FromInt(9)

	claws := &core.SkillDef{
		ID:            "ghoul_claws",
		Name:          "Ghoul Claws",
		Kind:          core.SkillMelee,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   9, // 0.3s — fast, shallow cuts
		RecoveryTicks: 6,
		SpeedStat:     stats.AttackSpeed,
		Range:         fm.FromMilli(1500),
	}
	claws.BaseMin[core.Physical] = fm.FromInt(3)
	claws.BaseMax[core.Physical] = fm.FromInt(6)

	arcBolt := &core.SkillDef{
		ID:            "arc_bolt",
		Name:          "Arc Bolt",
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagSpell, stats.TagProjectile, stats.TagLightning),
		Effectiveness: fm.One,
		WindupTicks:   18, // 0.6s channelled crackle — dodgeable, unlike its shock
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		ProjSpeed:     fm.FromInt(18),
		ProjTTL:       50,
		ProjRadius:    fm.FromMilli(350),
		ShockChance:   fm.FromMilli(350),
	}
	// Same wild-roll lightning identity as spark, hitting harder on average.
	arcBolt.BaseMin[core.Lightning] = fm.FromInt(4)
	arcBolt.BaseMax[core.Lightning] = fm.FromInt(22)

	colossusSlam := &core.SkillDef{
		ID:            "colossus_slam",
		Name:          "Colossus Slam",
		Kind:          core.SkillNova,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   36, // 1.2s telegraph — the whole fight is "move now"
		RecoveryTicks: 24,
		SpeedStat:     stats.AttackSpeed,
		AoERadius:     fm.FromMilli(3500),
	}
	colossusSlam.BaseMin[core.Physical] = fm.FromInt(25)
	colossusSlam.BaseMax[core.Physical] = fm.FromInt(35)

	boneVolley := &core.SkillDef{
		ID:            "bone_volley",
		Name:          "Bone Volley",
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagAttack, stats.TagProjectile, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   24, // 0.8s draw
		RecoveryTicks: 12,
		SpeedStat:     stats.AttackSpeed,
		ProjSpeed:     fm.FromInt(14),
		ProjTTL:       70,
		ProjRadius:    fm.FromMilli(550), // a fat bone — harder to sidestep than an arrow
	}
	boneVolley.BaseMin[core.Physical] = fm.FromInt(12)
	boneVolley.BaseMax[core.Physical] = fm.FromInt(18)

	adrenaline := &core.SkillDef{
		ID:            "adrenaline",
		Name:          "Adrenaline",
		Kind:          core.SkillBuff,
		Tags:          stats.T(stats.TagSpell),
		ManaCost:      fm.FromInt(15),
		WindupTicks:   6, // 0.2s — a quick shout, not a cast
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		SelfBuff:      "adrenaline",
	}

	return []*core.SkillDef{fireball, slam, frostNova, spark, boneArrow, adrenaline, claws, arcBolt, colossusSlam, boneVolley}
}

func baseStats(pairs map[stats.StatID]fm.Fixed) [stats.StatCount]fm.Fixed {
	var b [stats.StatCount]fm.Fixed
	// Multiplier-shaped stats default to One for every actor.
	b[stats.DamageTaken] = fm.One
	b[stats.AttackSpeed] = fm.One
	b[stats.CastSpeed] = fm.One
	b[stats.CritMulti] = fm.FromMilli(1500)
	// Write-only map fill: iteration order can't affect the result.
	for k, v := range pairs {
		b[k] = v
	}
	return b
}

func actorDefs() []*core.ActorDef {
	player := &core.ActorDef{
		ID:     "player",
		Name:   "Exile",
		Team:   core.TeamPlayers,
		Radius: fm.FromMilli(500),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(100),
			stats.Mana:       fm.FromInt(50),
			stats.ManaRegen:  fm.FromInt(2),
			stats.MoveSpeed:  fm.FromInt(5),
			stats.Accuracy:   fm.FromInt(100),
			stats.Evasion:    fm.FromInt(50),
			stats.Armour:     fm.FromInt(20),
			stats.CritChance: fm.FromMilli(50), // 5%
		}),
		Skills:        []string{"fireball", "frost_nova", "spark", "adrenaline"},
		Flasks:        []string{"life_flask", "mana_flask"},
		InventorySize: 20,
		Level:         1,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(12)},
			{Stat: stats.Mana, Layer: stats.LayerFlat, Value: fm.FromInt(6)},
			{Stat: stats.Accuracy, Layer: stats.LayerFlat, Value: fm.FromInt(10)},
		},
	}

	zombie := &core.ActorDef{
		ID:     "zombie",
		Name:   "Shambling Zombie",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(600),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(60),
			stats.MoveSpeed:  fm.FromInt(3),
			stats.Accuracy:   fm.FromInt(80),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:      []string{"zombie_slam"},
		AI:          "melee_chaser",
		AggroRadius: fm.FromInt(15),
		LeashRadius: fm.FromInt(20),
		LootTable:   "zombie_drops",
		Level:       1,
		XPValue:     20,
		// Growth so a future floor-scaling spawner can hand out level-N packs.
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(8)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(40)},
		},
	}

	// Squishy backline: keeps its distance and plinks arrows; falls fast
	// once you reach it. Exists to make rooms and corners matter.
	archer := &core.ActorDef{
		ID:     "skeleton_archer",
		Name:   "Skeleton Archer",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(500),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(35),
			stats.MoveSpeed:  fm.FromInt(4),
			stats.Accuracy:   fm.FromInt(90),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:         []string{"bone_arrow"},
		AI:             "ranged_kiter",
		AggroRadius:    fm.FromInt(18),
		LeashRadius:    fm.FromInt(24), // kites within a wider territory than the zombie
		PreferredRange: fm.FromInt(12),
		LootTable:      "archer_drops",
		Level:          1,
		XPValue:        30, // squishier but trickier than the zombie
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(5)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(40)},
		},
	}

	// Stationary target for tests and tuning; drops like a zombie.
	dummy := &core.ActorDef{
		ID:     "training_dummy",
		Name:   "Training Dummy",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(600),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life: fm.FromInt(80),
		}),
		LootTable: "dummy_drops",
		Level:     1,
		XPValue:   10,
	}

	// Fast, fragile, and hungry: outruns a walking player, dies to a stiff
	// breeze. Exists to force target priority — ignore it and it's on you.
	ghoul := &core.ActorDef{
		ID:     "ghoul",
		Name:   "Grave Ghoul",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(450),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(25),
			stats.MoveSpeed:  fm.FromMilli(5500),
			stats.Accuracy:   fm.FromInt(75),
			stats.CritChance: fm.FromMilli(80),
		}),
		Skills:      []string{"ghoul_claws"},
		AI:          "melee_chaser",
		AggroRadius: fm.FromInt(14),
		LeashRadius: fm.FromInt(18),
		LootTable:   "ghoul_drops",
		Level:       1,
		XPValue:     15,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(4)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(50)},
		},
	}

	// Caster backline: slower and tougher than the archer, and its bolts
	// shock — standing still near one gets expensive fast.
	mage := &core.ActorDef{
		ID:     "skeleton_mage",
		Name:   "Skeleton Mage",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(500),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(45),
			stats.MoveSpeed:  fm.FromMilli(3500),
			stats.Accuracy:   fm.FromInt(85),
			stats.CritChance: fm.FromMilli(60),
		}),
		Skills:         []string{"arc_bolt"},
		AI:             "ranged_kiter",
		AggroRadius:    fm.FromInt(16),
		LeashRadius:    fm.FromInt(22),
		PreferredRange: fm.FromInt(10),
		LootTable:      "mage_drops",
		Level:          1,
		XPValue:        35,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(6)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(45)},
		},
	}

	// The floor guardian: a slow heavyweight parked on the stairs every
	// few floors. Both attacks are heavily telegraphed — the fight is about
	// moving, not stat-checking. Always spawned rare (SpawnRareLeveled).
	colossus := &core.ActorDef{
		ID:     "bone_colossus",
		Name:   "Bone Colossus",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(1100),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(350),
			stats.MoveSpeed:  fm.FromMilli(2200),
			stats.Accuracy:   fm.FromInt(120),
			stats.Armour:     fm.FromInt(40),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:      []string{"colossus_slam", "bone_volley"},
		AI:          "boss_brute",
		AggroRadius: fm.FromInt(18),
		LeashRadius: fm.FromInt(14), // a guardian guards; it won't chase across the floor
		LootTable:   "boss_drops",
		Level:       1,
		XPValue:     300,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(30)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(45)},
		},
	}

	return []*core.ActorDef{player, zombie, archer, dummy, ghoul, mage, colossus}
}

// affixDefs is the global affix pool. Slice order feeds the weighted roll —
// reordering or inserting mid-list is a replay-relevant change (re-record
// goldens), so append new affixes at the end of their kind block. Groups
// with a "_greater" entry are tiered: one group, two rarity-of-roll bands.
func affixDefs() []*core.AffixDef {
	// Per-slot pools: which families each affix can roll on, PoE-flavored —
	// damage lives on weapons (and rings, flat), defences on armour pieces,
	// movement speed on boots alone. DB() asserts every family keeps at
	// least 3 prefix and 3 suffix groups, enough to fill a rare.
	var (
		flatDmg  = []core.SlotFamily{core.FamilyWeapon, core.FamilyRing}
		incEle   = []core.SlotFamily{core.FamilyWeapon, core.FamilyAmulet, core.FamilyRing}
		caster   = []core.SlotFamily{core.FamilyWeapon, core.FamilyAmulet}
		armours  = []core.SlotFamily{core.FamilyOffhand, core.FamilyHelmet, core.FamilyBody, core.FamilyGloves, core.FamilyBoots, core.FamilyBelt}
		evasions = []core.SlotFamily{core.FamilyOffhand, core.FamilyHelmet, core.FamilyBody, core.FamilyGloves, core.FamilyBoots}
		esSlots  = []core.SlotFamily{core.FamilyOffhand, core.FamilyHelmet, core.FamilyBody, core.FamilyGloves, core.FamilyBoots, core.FamilyBelt, core.FamilyAmulet}
		lifes    = []core.SlotFamily{core.FamilyOffhand, core.FamilyHelmet, core.FamilyBody, core.FamilyGloves, core.FamilyBoots, core.FamilyBelt, core.FamilyAmulet, core.FamilyRing}
		manas    = []core.SlotFamily{core.FamilyOffhand, core.FamilyHelmet, core.FamilyGloves, core.FamilyBoots, core.FamilyAmulet, core.FamilyRing}
		regens   = []core.SlotFamily{core.FamilyHelmet, core.FamilyBody, core.FamilyAmulet, core.FamilyRing}
		resists  = []core.SlotFamily{core.FamilyOffhand, core.FamilyHelmet, core.FamilyBody, core.FamilyGloves, core.FamilyBoots, core.FamilyBelt, core.FamilyAmulet, core.FamilyRing}
		crits    = []core.SlotFamily{core.FamilyWeapon, core.FamilyAmulet}
		attacks  = []core.SlotFamily{core.FamilyWeapon, core.FamilyGloves}
		accs     = []core.SlotFamily{core.FamilyWeapon, core.FamilyHelmet, core.FamilyGloves, core.FamilyRing}
		procs    = []core.SlotFamily{core.FamilyWeapon, core.FamilyGloves, core.FamilyAmulet, core.FamilyRing}
		boots    = []core.SlotFamily{core.FamilyBoots}
	)
	return []*core.AffixDef{
		// --- prefixes: damage
		{
			ID: "flat_fire_damage", Group: "added_fire", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagFire),
			Min: fm.FromInt(2), Max: fm.FromInt(5), Weight: 100,
			Families: flatDmg,
		},
		{
			ID: "flat_cold_damage", Group: "added_cold", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagCold),
			Min: fm.FromInt(2), Max: fm.FromInt(5), Weight: 100,
			Families: flatDmg,
		},
		{
			ID: "flat_lightning_damage", Group: "added_lightning", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagLightning),
			Min: fm.FromInt(1), Max: fm.FromInt(7), Weight: 100,
			Families: flatDmg,
		},
		{
			ID: "flat_phys_damage", Group: "added_phys", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagPhysical),
			Min: fm.FromInt(2), Max: fm.FromInt(4), Weight: 90,
			Families: flatDmg,
		},
		{
			ID: "increased_spell_damage", Group: "spell_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagSpell),
			Min: fm.FromMilli(80), Max: fm.FromMilli(150), Weight: 70, // 8–15%
			Families: caster,
		},
		{
			ID: "increased_fire_damage", Group: "fire_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagFire),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 60, // 10–20%
			Families: incEle,
		},
		{
			ID: "increased_cold_damage", Group: "cold_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagCold),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 60,
			Families: incEle,
		},
		{
			ID: "increased_lightning_damage", Group: "lightning_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagLightning),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 60,
			Families: incEle,
		},
		// --- prefixes: defences and pools
		{
			ID: "flat_life", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(25), Weight: 100,
			Families: lifes,
		},
		{
			ID: "flat_life_greater", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(26), Max: fm.FromInt(45), Weight: 35,
			Families: lifes,
		},
		{
			ID: "flat_mana", Group: "mana", Kind: core.Prefix,
			Stat: stats.Mana, Layer: stats.LayerFlat,
			Min: fm.FromInt(8), Max: fm.FromInt(18), Weight: 90,
			Families: manas,
		},
		{
			ID: "flat_armour", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(40), Weight: 80,
			Families: armours,
		},
		{
			ID: "flat_armour_greater", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(41), Max: fm.FromInt(75), Weight: 25,
			Families: armours,
		},
		{
			ID: "flat_evasion", Group: "evasion", Kind: core.Prefix,
			Stat: stats.Evasion, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(40), Weight: 80,
			Families: evasions,
		},
		{
			ID: "flat_energy_shield", Group: "energy_shield", Kind: core.Prefix,
			Stat: stats.EnergyShield, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(25), Weight: 70,
			Families: esSlots,
		},
		{
			ID: "life_regen", Group: "life_regen", Kind: core.Prefix,
			Stat: stats.LifeRegen, Layer: stats.LayerFlat,
			Min: fm.FromInt(1), Max: fm.FromInt(3), Weight: 60,
			Families: regens,
		},
		{
			ID: "mana_regen", Group: "mana_regen", Kind: core.Prefix,
			Stat: stats.ManaRegen, Layer: stats.LayerFlat,
			Min: fm.FromMilli(500), Max: fm.FromMilli(1500), Weight: 60,
			Families: regens,
		},
		// --- suffixes: resistances
		{
			ID: "fire_resistance", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100, // 10–20%
			Families: resists,
		},
		{
			ID: "fire_resistance_greater", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(210), Max: fm.FromMilli(300), Weight: 30, // 21–30%
			Families: resists,
		},
		{
			ID: "cold_resistance", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100,
			Families: resists,
		},
		{
			ID: "cold_resistance_greater", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(210), Max: fm.FromMilli(300), Weight: 30,
			Families: resists,
		},
		{
			ID: "lightning_resistance", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100,
			Families: resists,
		},
		{
			ID: "lightning_resistance_greater", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(210), Max: fm.FromMilli(300), Weight: 30,
			Families: resists,
		},
		{
			ID: "chaos_resistance", Group: "chaos_res", Kind: core.Suffix,
			Stat: stats.ChaosRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(150), Weight: 40, // 5–15%
			Families: resists,
		},
		// --- suffixes: offense and utility
		{
			ID: "crit_chance", Group: "crit", Kind: core.Suffix,
			Stat: stats.CritChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(10), Max: fm.FromMilli(30), Weight: 60, // 1–3%
			Families: crits,
		},
		{
			ID: "crit_multi", Group: "crit_multi", Kind: core.Suffix,
			Stat: stats.CritMulti, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(250), Weight: 40, // +10–25%
			Families: crits,
		},
		{
			ID: "increased_cast_speed", Group: "cast_speed", Kind: core.Suffix,
			Stat: stats.CastSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 60, // 5–10%
			Families: caster,
		},
		{
			ID: "increased_attack_speed", Group: "attack_speed", Kind: core.Suffix,
			Stat: stats.AttackSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 60,
			Families: attacks,
		},
		{
			ID: "increased_move_speed", Group: "move_speed", Kind: core.Suffix,
			Stat: stats.MoveSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(40), Max: fm.FromMilli(80), Weight: 40, // 4–8%
			Families: boots,
		},
		{
			ID: "flat_accuracy", Group: "accuracy", Kind: core.Suffix,
			Stat: stats.Accuracy, Layer: stats.LayerFlat,
			Min: fm.FromInt(20), Max: fm.FromInt(50), Weight: 70,
			Families: accs,
		},
		{
			ID: "ignite_chance", Group: "ignite_chance", Kind: core.Suffix,
			Stat: stats.IgniteChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 40, // 5–10%
			Families: procs,
		},
		{
			ID: "shock_chance", Group: "shock_chance", Kind: core.Suffix,
			Stat: stats.ShockChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 40,
			Families: procs,
		},
	}
}

// Every base carries an implicit — the guaranteed identity of the slot,
// rolled at drop time, on top of whatever affixes rarity grants.
func baseItemDefs() []*core.BaseItemDef {
	return []*core.BaseItemDef{
		{ID: "rusty_sword", Name: "Rusty Sword", Slot: core.FamilyWeapon, Implicit: &core.ImplicitDef{
			// Untagged increased damage so every build cares about its weapon.
			ID: "increased_damage", Stat: stats.Damage, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), // 5–10%
		}},
		{ID: "wooden_shield", Name: "Wooden Shield", Slot: core.FamilyOffhand, Implicit: &core.ImplicitDef{
			ID: "armour", Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(25),
		}},
		{ID: "leather_cap", Name: "Leather Cap", Slot: core.FamilyHelmet, Implicit: &core.ImplicitDef{
			ID: "evasion", Stat: stats.Evasion, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(20),
		}},
		{ID: "leather_vest", Name: "Leather Vest", Slot: core.FamilyBody, Implicit: &core.ImplicitDef{
			ID: "armour", Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(30),
		}},
		{ID: "leather_gloves", Name: "Leather Gloves", Slot: core.FamilyGloves, Implicit: &core.ImplicitDef{
			ID: "accuracy", Stat: stats.Accuracy, Layer: stats.LayerFlat,
			Min: fm.FromInt(20), Max: fm.FromInt(40),
		}},
		{ID: "leather_boots", Name: "Leather Boots", Slot: core.FamilyBoots, Implicit: &core.ImplicitDef{
			ID: "move_speed", Stat: stats.MoveSpeed, Layer: stats.LayerFlat,
			Min: fm.FromMilli(200), Max: fm.FromMilli(400), // +0.2–0.4 u/s
		}},
		{ID: "bone_amulet", Name: "Bone Amulet", Slot: core.FamilyAmulet, Implicit: &core.ImplicitDef{
			ID: "fire_resistance", Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), // 5–10%
		}},
		{ID: "iron_ring", Name: "Iron Ring", Slot: core.FamilyRing, Implicit: &core.ImplicitDef{
			ID: "mana", Stat: stats.Mana, Layer: stats.LayerFlat,
			Min: fm.FromInt(5), Max: fm.FromInt(10),
		}},
		{ID: "leather_belt", Name: "Leather Belt", Slot: core.FamilyBelt, Implicit: &core.ImplicitDef{
			ID: "life", Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(20),
		}},
	}
}

func lootTableDefs() []*core.LootTableDef {
	return []*core.LootTableDef{
		{
			// Frontline trash: drops a bit under half the time, mostly plain
			// gear with the occasional magic piece.
			ID:            "zombie_drops",
			DropChance:    fm.FromMilli(450),
			RarityWeights: [3]uint32{60, 32, 8},
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "leather_belt",
			},
		},
		{
			// Squishier but better-connected: rarer drops, jewelry-leaning,
			// noticeably better rarity odds.
			ID:            "archer_drops",
			DropChance:    fm.FromMilli(400),
			RarityWeights: [3]uint32{45, 40, 15},
			Bases: []string{
				"rusty_sword", "bone_amulet", "iron_ring", "leather_cap",
				"leather_gloves", "leather_boots",
			},
		},
		{
			// Test/tuning target keeps the old always-drops behavior and the
			// full base list, so loot work stays easy to exercise.
			ID:            "dummy_drops",
			DropChance:    fm.One,
			RarityWeights: [3]uint32{50, 35, 15},
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
		{
			// Swarm trash: drops rarely — a pack of ghouls shouldn't carpet
			// the floor — but leans quick gear (boots, gloves, blades).
			ID:            "ghoul_drops",
			DropChance:    fm.FromMilli(250),
			RarityWeights: [3]uint32{65, 30, 5},
			Bases: []string{
				"rusty_sword", "leather_boots", "leather_gloves", "leather_belt",
			},
		},
		{
			// The guardian's hoard: always drops, rare-heavy, full base list —
			// and the rare monster hooks add two more attempts on top.
			ID:            "boss_drops",
			DropChance:    fm.One,
			RarityWeights: [3]uint32{10, 45, 45},
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
		{
			// Caster elite: stingy but jewelry-leaning with real rarity odds —
			// the mage is the pack member worth focusing for loot too.
			ID:            "mage_drops",
			DropChance:    fm.FromMilli(350),
			RarityWeights: [3]uint32{35, 45, 20},
			Bases: []string{
				"bone_amulet", "iron_ring", "leather_cap", "wooden_shield",
			},
		},
	}
}
