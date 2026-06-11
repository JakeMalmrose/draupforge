// Package content holds the game's data tables as typed Go literals —
// compile-time checked, zero parsers. Definitions are pure data the sim
// consumes; the sim never imports this package, hosts wire it in.
package content

import (
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
	return db
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

	return []*core.SkillDef{fireball, slam, frostNova, spark, boneArrow, adrenaline}
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
		InventorySize: 20,
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
		LootTable:   "zombie_drops",
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
		PreferredRange: fm.FromInt(12),
		LootTable:      "zombie_drops",
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
		LootTable: "zombie_drops",
	}

	return []*core.ActorDef{player, zombie, archer, dummy}
}

func affixDefs() []*core.AffixDef {
	return []*core.AffixDef{
		{
			ID: "flat_fire_damage", Group: "added_fire", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagFire),
			Min: fm.FromInt(2), Max: fm.FromInt(5), Weight: 100,
		},
		{
			ID: "flat_life", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(25), Weight: 100,
		},
		{
			ID: "flat_armour", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(40), Weight: 80,
		},
		{
			ID: "flat_cold_damage", Group: "added_cold", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagCold),
			Min: fm.FromInt(2), Max: fm.FromInt(5), Weight: 100,
		},
		{
			ID: "flat_lightning_damage", Group: "added_lightning", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagLightning),
			Min: fm.FromInt(1), Max: fm.FromInt(7), Weight: 100,
		},
		{
			ID: "fire_resistance", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100, // 10–20%
		},
		{
			ID: "cold_resistance", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100, // 10–20%
		},
		{
			ID: "lightning_resistance", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100, // 10–20%
		},
		{
			ID: "crit_chance", Group: "crit", Kind: core.Suffix,
			Stat: stats.CritChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(10), Max: fm.FromMilli(30), Weight: 60, // 1–3%
		},
		{
			ID: "increased_cast_speed", Group: "cast_speed", Kind: core.Suffix,
			Stat: stats.CastSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 60, // 5–10%
		},
	}
}

func baseItemDefs() []*core.BaseItemDef {
	return []*core.BaseItemDef{
		{ID: "rusty_sword", Name: "Rusty Sword", Slot: core.FamilyWeapon},
		{ID: "wooden_shield", Name: "Wooden Shield", Slot: core.FamilyOffhand},
		{ID: "leather_cap", Name: "Leather Cap", Slot: core.FamilyHelmet},
		{ID: "leather_vest", Name: "Leather Vest", Slot: core.FamilyBody},
		{ID: "leather_gloves", Name: "Leather Gloves", Slot: core.FamilyGloves},
		{ID: "leather_boots", Name: "Leather Boots", Slot: core.FamilyBoots},
		{ID: "bone_amulet", Name: "Bone Amulet", Slot: core.FamilyAmulet},
		{ID: "iron_ring", Name: "Iron Ring", Slot: core.FamilyRing},
		{ID: "leather_belt", Name: "Leather Belt", Slot: core.FamilyBelt},
	}
}

func lootTableDefs() []*core.LootTableDef {
	return []*core.LootTableDef{
		{
			ID:         "zombie_drops",
			DropChance: fm.One, // always, while loot is the thing being proven
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
	}
}
