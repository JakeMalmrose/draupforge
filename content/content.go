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

	return []*core.ActorDef{player, zombie, archer, dummy}
}

// affixDefs is the global affix pool. Slice order feeds the weighted roll —
// reordering or inserting mid-list is a replay-relevant change (re-record
// goldens), so append new affixes at the end of their kind block. Groups
// with a "_greater" entry are tiered: one group, two rarity-of-roll bands.
func affixDefs() []*core.AffixDef {
	return []*core.AffixDef{
		// --- prefixes: damage
		{
			ID: "flat_fire_damage", Group: "added_fire", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagFire),
			Min: fm.FromInt(2), Max: fm.FromInt(5), Weight: 100,
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
			ID: "flat_phys_damage", Group: "added_phys", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagPhysical),
			Min: fm.FromInt(2), Max: fm.FromInt(4), Weight: 90,
		},
		{
			ID: "increased_spell_damage", Group: "spell_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagSpell),
			Min: fm.FromMilli(80), Max: fm.FromMilli(150), Weight: 70, // 8–15%
		},
		{
			ID: "increased_fire_damage", Group: "fire_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagFire),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 60, // 10–20%
		},
		{
			ID: "increased_cold_damage", Group: "cold_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagCold),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 60,
		},
		{
			ID: "increased_lightning_damage", Group: "lightning_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagLightning),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 60,
		},
		// --- prefixes: defences and pools
		{
			ID: "flat_life", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(25), Weight: 100,
		},
		{
			ID: "flat_life_greater", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(26), Max: fm.FromInt(45), Weight: 35,
		},
		{
			ID: "flat_mana", Group: "mana", Kind: core.Prefix,
			Stat: stats.Mana, Layer: stats.LayerFlat,
			Min: fm.FromInt(8), Max: fm.FromInt(18), Weight: 90,
		},
		{
			ID: "flat_armour", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(40), Weight: 80,
		},
		{
			ID: "flat_armour_greater", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(41), Max: fm.FromInt(75), Weight: 25,
		},
		{
			ID: "flat_evasion", Group: "evasion", Kind: core.Prefix,
			Stat: stats.Evasion, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(40), Weight: 80,
		},
		{
			ID: "flat_energy_shield", Group: "energy_shield", Kind: core.Prefix,
			Stat: stats.EnergyShield, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(25), Weight: 70,
		},
		{
			ID: "life_regen", Group: "life_regen", Kind: core.Prefix,
			Stat: stats.LifeRegen, Layer: stats.LayerFlat,
			Min: fm.FromInt(1), Max: fm.FromInt(3), Weight: 60,
		},
		{
			ID: "mana_regen", Group: "mana_regen", Kind: core.Prefix,
			Stat: stats.ManaRegen, Layer: stats.LayerFlat,
			Min: fm.FromMilli(500), Max: fm.FromMilli(1500), Weight: 60,
		},
		// --- suffixes: resistances
		{
			ID: "fire_resistance", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100, // 10–20%
		},
		{
			ID: "fire_resistance_greater", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(210), Max: fm.FromMilli(300), Weight: 30, // 21–30%
		},
		{
			ID: "cold_resistance", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100,
		},
		{
			ID: "cold_resistance_greater", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(210), Max: fm.FromMilli(300), Weight: 30,
		},
		{
			ID: "lightning_resistance", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Weight: 100,
		},
		{
			ID: "lightning_resistance_greater", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(210), Max: fm.FromMilli(300), Weight: 30,
		},
		{
			ID: "chaos_resistance", Group: "chaos_res", Kind: core.Suffix,
			Stat: stats.ChaosRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(150), Weight: 40, // 5–15%
		},
		// --- suffixes: offense and utility
		{
			ID: "crit_chance", Group: "crit", Kind: core.Suffix,
			Stat: stats.CritChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(10), Max: fm.FromMilli(30), Weight: 60, // 1–3%
		},
		{
			ID: "crit_multi", Group: "crit_multi", Kind: core.Suffix,
			Stat: stats.CritMulti, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(250), Weight: 40, // +10–25%
		},
		{
			ID: "increased_cast_speed", Group: "cast_speed", Kind: core.Suffix,
			Stat: stats.CastSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 60, // 5–10%
		},
		{
			ID: "increased_attack_speed", Group: "attack_speed", Kind: core.Suffix,
			Stat: stats.AttackSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 60,
		},
		{
			ID: "increased_move_speed", Group: "move_speed", Kind: core.Suffix,
			Stat: stats.MoveSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(40), Max: fm.FromMilli(80), Weight: 40, // 4–8%
		},
		{
			ID: "flat_accuracy", Group: "accuracy", Kind: core.Suffix,
			Stat: stats.Accuracy, Layer: stats.LayerFlat,
			Min: fm.FromInt(20), Max: fm.FromInt(50), Weight: 70,
		},
		{
			ID: "ignite_chance", Group: "ignite_chance", Kind: core.Suffix,
			Stat: stats.IgniteChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 40, // 5–10%
		},
		{
			ID: "shock_chance", Group: "shock_chance", Kind: core.Suffix,
			Stat: stats.ShockChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Weight: 40,
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
	}
}
