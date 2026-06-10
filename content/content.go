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
	return db
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

	return []*core.SkillDef{fireball, slam}
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
		Skills: []string{"fireball"},
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

	return []*core.ActorDef{player, zombie, dummy}
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
			ID: "fire_resistance", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
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
		{ID: "iron_ring", Name: "Iron Ring"},
		{ID: "leather_belt", Name: "Leather Belt"},
	}
}

func lootTableDefs() []*core.LootTableDef {
	return []*core.LootTableDef{
		{
			ID:         "zombie_drops",
			DropChance: fm.One, // always, while loot is the thing being proven
			Bases:      []string{"iron_ring", "leather_belt"},
		},
	}
}
