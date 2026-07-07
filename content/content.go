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
		// Staged skills carry their whole timeline in Stages; the legacy
		// windup/recovery fields (and player cutting) must stay off them.
		// Mis-authored stage params would fire silently-wrong effects, so
		// they're content bugs caught here.
		if (sk.Kind == core.SkillStaged) != (len(sk.Stages) > 0) {
			panic("content: skill " + sk.ID + ": Stages and SkillStaged must come together")
		}
		if sk.Kind == core.SkillSummon &&
			(sk.SummonDef == "" || sk.SummonCount < 1 || sk.SummonCap < sk.SummonCount) {
			panic("content: summon skill " + sk.ID + " is malformed")
		}
		if sk.Kind != core.SkillStaged {
			continue
		}
		if sk.WindupTicks != 0 || sk.RecoveryTicks != 0 || sk.Cuttable {
			panic("content: staged skill " + sk.ID + " sets windup/recovery/cuttable")
		}
		total := uint32(0)
		for _, st := range sk.Stages {
			total += st.Ticks
			switch st.Effect {
			case core.StageBlast:
				if st.Radius <= 0 {
					panic("content: staged skill " + sk.ID + " has a radius-less blast")
				}
			case core.StageRing:
				if sk.ProjSpeed <= 0 || sk.ProjTTL == 0 || st.RingStep < 1 || st.RingStep > 3 {
					panic("content: staged skill " + sk.ID + " has a malformed ring stage")
				}
			}
		}
		if total == 0 {
			panic("content: staged skill " + sk.ID + " has a zero-tick timeline")
		}
	}
	for _, a := range actorDefs() {
		db.Actors[a.ID] = a
	}
	for _, sk := range skillDefs() {
		if sk.Kind == core.SkillSummon && db.Actors[sk.SummonDef] == nil {
			panic("content: summon skill " + sk.ID + " references unknown def " + sk.SummonDef)
		}
	}
	db.Affixes = affixDefs()
	for _, b := range baseItemDefs() {
		db.BaseItems[b.ID] = b
	}
	// Per-slot pools must stay deep enough to fill a rare (3 prefixes + 3
	// suffixes from distinct groups) on every family a base occupies —
	// starving a pool is a content bug, caught here instead of at drop time.
	// Only ILvl-0 (base tier) affixes count: a floor-1 item unlocks nothing
	// higher, so the base tiers alone must cover a rare or a low-level drop
	// starves.
	for _, b := range db.BaseItems {
		groups := [2]map[string]bool{{}, {}}
		for _, af := range db.Affixes {
			if af.ILvl == 0 && af.AllowedOn(b.Slot) {
				groups[af.Kind][af.Group] = true
			}
		}
		if len(groups[core.Prefix]) < 3 || len(groups[core.Suffix]) < 3 {
			panic("content: base-tier affix pool too shallow for " + b.ID)
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
	db.Supports = supportDefs()
	seenSupport := map[string]bool{}
	for _, s := range db.Supports {
		if s.ID == "" || s.Name == "" || s.ManaMult <= 0 {
			panic("content: support " + s.ID + " is malformed")
		}
		if seenSupport[s.ID] {
			panic("content: duplicate support " + s.ID)
		}
		seenSupport[s.ID] = true
	}
	// The draft pool for uncut skill gems, in skill-table order (replay-
	// relevant like the affix table). Both pools must cover a full draft.
	auraSrc := map[uint64]string{}
	for _, sk := range skillDefs() {
		if sk.Cuttable {
			db.Cuttable = append(db.Cuttable, db.Skills[sk.ID])
		}
		// Aura mod sources must be unique — an FNV collision would make two
		// auras share (and strip) each other's sheet mods.
		if sk.Kind == core.SkillAura {
			if other, ok := auraSrc[sk.AuraModSource()]; ok {
				panic("content: aura mod source collision: " + sk.ID + " vs " + other)
			}
			auraSrc[sk.AuraModSource()] = sk.ID
		}
	}
	if len(db.Cuttable) < core.GemDraftSize || len(db.Supports) < core.GemDraftSize {
		panic("content: gem draft pools too shallow")
	}
	for _, a := range db.Actors {
		for _, id := range a.StartingGems {
			sk := db.Skills[id]
			if sk == nil || !sk.Cuttable {
				panic("content: starting gem " + id + " on " + a.ID + " is not a cuttable skill")
			}
		}
		// Death-spawn chains must resolve and terminate: a cycle would grow
		// the population forever, one death at a time.
		seen := map[string]bool{a.ID: true}
		for cur := a; cur.DeathSpawnCount > 0; {
			if cur.DeathSpawnCount > 8 {
				panic("content: " + cur.ID + " death-spawns more than 8 adds")
			}
			next := db.Actors[cur.DeathSpawnDef]
			if next == nil {
				panic("content: " + cur.ID + " death-spawns unknown def " + cur.DeathSpawnDef)
			}
			if seen[next.ID] {
				panic("content: death-spawn cycle through " + next.ID)
			}
			seen[next.ID] = true
			cur = next
		}
	}
	db.Uniques = uniqueDefs()
	seenUnique := map[string]bool{}
	for _, u := range db.Uniques {
		if u.ID == "" || u.Name == "" || len(u.Mods) == 0 || len(u.ModLines) == 0 {
			panic("content: unique " + u.ID + " is malformed")
		}
		if db.BaseItems[u.Base] == nil {
			panic("content: unique " + u.ID + " references unknown base " + u.Base)
		}
		if seenUnique[u.ID] {
			panic("content: duplicate unique " + u.ID)
		}
		seenUnique[u.ID] = true
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

// uniqueDefs is the chase-item table. Slice order feeds unique-drop picks —
// reordering is a replay-relevant change, same as the affix table. Every
// unique should bend a build around itself: the shape stats
// (ExtraProjectiles/ExtraChains) exist for these and nothing else rolls
// them, and the downside is what makes wearing one a decision.
func uniqueDefs() []*core.UniqueDef {
	return []*core.UniqueDef{
		{
			ID: "stormweaver_band", Name: "Stormweaver Band", Base: "iron_ring",
			Desc: "The storm never asks where its bolts land.",
			Mods: []core.BuffMod{
				{Stat: stats.ExtraProjectiles, Layer: stats.LayerFlat, Value: fm.One},
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(-150)},
			},
			ModLines: []string{"+1 projectile", "15% less damage"},
		},
		{
			ID: "coil_of_the_hydra", Name: "Coil of the Hydra", Base: "bone_amulet",
			Desc: "Cut one head from a current and two more arc back.",
			Mods: []core.BuffMod{
				{Stat: stats.ExtraChains, Layer: stats.LayerFlat, Value: fm.FromInt(2)},
				{Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagLightning), Value: fm.FromMilli(150)},
			},
			ModLines: []string{"+2 chain targets", "15% increased lightning damage"},
		},
		{
			ID: "juggernauts_wall", Name: "Juggernaut's Wall", Base: "wooden_shield",
			Desc: "It has never moved for anyone. Neither will you.",
			Mods: []core.BuffMod{
				{Stat: stats.Armour, Layer: stats.LayerFlat, Value: fm.FromInt(120)},
				{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(40)},
				{Stat: stats.MoveSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(-100)},
			},
			ModLines: []string{"+120 armour", "+40 life", "10% reduced move speed"},
		},
		{
			ID: "bonelords_mark", Name: "Bonelord's Mark", Base: "bone_amulet",
			Desc: "The dead keep no count. He does.",
			Mods: []core.BuffMod{
				{Stat: stats.ExtraMinions, Layer: stats.LayerFlat, Value: fm.One},
				{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(20)},
				{Stat: stats.CastSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(-100)},
			},
			ModLines: []string{"+1 to summon capacity", "+20 life", "10% reduced cast speed"},
		},
		{
			ID: "windrunner_treads", Name: "Windrunner Treads", Base: "leather_boots",
			Desc: "Outrun the ground and the grave both.",
			Mods: []core.BuffMod{
				{Stat: stats.MoveSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(250)},
				{Stat: stats.Evasion, Layer: stats.LayerFlat, Value: fm.FromInt(30)},
				{Stat: stats.Life, Layer: stats.LayerMore, Value: fm.FromMilli(-100)},
			},
			ModLines: []string{"25% increased move speed", "+30 evasion", "10% less life"},
		},
	}
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
		Cuttable:      true,
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagSpell, stats.TagProjectile, stats.TagFire),
		Effectiveness: fm.One,
		ManaCost:      fm.FromInt(10),
		WindupTicks:   15, // 0.5s base cast
		RecoveryTicks: 6,
		SpeedStat:     stats.CastSpeed,
		ProjSpeed:     fm.FromInt(20),
		ProjTTL:       21, // 0.7s flight ≈ 14u — a skill shot, not a sniper rifle
		ProjRadius:    fm.FromMilli(400),
		ExplodeRadius: fm.FromInt(2), // impact splash, linear falloff to the edge
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
		Cuttable:      true,
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
		Cuttable:      true,
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagSpell, stats.TagProjectile, stats.TagLightning),
		Effectiveness: fm.One,
		ManaCost:      fm.FromInt(6),
		WindupTicks:   9, // 0.3s base cast: the fast, spammy option
		RecoveryTicks: 5,
		SpeedStat:     stats.CastSpeed,
		ProjSpeed:     fm.FromInt(28),
		ProjTTL:       45, // 1.5s — duration is spark's budget, not reach
		ProjRadius:    fm.FromMilli(300),
		Bounce:        true,              // ricochets off walls until the duration runs out
		WigglePeriod:  12,                // heading drifts ~2.5×/s — sparks wander, not beam
		ShockChance:   fm.FromMilli(300), // 30%
	}
	// Lightning identity: wild rolls. Averages below fireball, pays for the
	// faster cast and the shock upside.
	spark.BaseMin[core.Lightning] = fm.FromInt(3)
	spark.BaseMax[core.Lightning] = fm.FromInt(28)

	boneArrow := &core.SkillDef{
		ID:            "bone_arrow",
		Name:          "Bone Arrow",
		Cuttable:      true, // the player-legal physical attack option
		Kind:          core.SkillProjectile,
		Tags:          stats.T(stats.TagAttack, stats.TagProjectile, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   12, // 0.4s draw — dodgeable at range
		RecoveryTicks: 9,
		SpeedStat:     stats.AttackSpeed,
		ProjSpeed:     fm.FromInt(16),
		ProjTTL:       30, // 1s ≈ 16u — outranges the archer's 12u kite band, barely
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
		BleedChance:   fm.FromMilli(250), // torn flesh — the monster that teaches bleed
	}
	claws.BaseMin[core.Physical] = fm.FromInt(3)
	claws.BaseMax[core.Physical] = fm.FromInt(6)

	arcBolt := &core.SkillDef{
		ID:            "arc_bolt",
		Name:          "Arc Bolt",
		Kind:          core.SkillProjectile, // monster-only: the mage's dodgeable bolt (players get arc)
		Tags:          stats.T(stats.TagSpell, stats.TagProjectile, stats.TagLightning),
		Effectiveness: fm.One,
		WindupTicks:   18, // 0.6s channelled crackle — dodgeable, unlike its shock
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		ProjSpeed:     fm.FromInt(18),
		ProjTTL:       27, // 0.9s ≈ 16u — comfortably past the mage's 10u stand-off
		ProjRadius:    fm.FromMilli(350),
		ShockChance:   fm.FromMilli(350),
	}
	// Same wild-roll lightning identity as spark, hitting harder on average.
	arcBolt.BaseMin[core.Lightning] = fm.FromInt(4)
	arcBolt.BaseMax[core.Lightning] = fm.FromInt(22)

	arc := &core.SkillDef{
		ID:            "arc",
		Name:          "Arc",
		Cuttable:      true, // the player's chain lightning — arc_bolt's gem slot
		Kind:          core.SkillChain,
		Tags:          stats.T(stats.TagSpell, stats.TagLightning),
		Effectiveness: fm.One,
		ManaCost:      fm.FromInt(13),
		WindupTicks:   15, // 0.5s — fireball cadence, pack-clearing payoff
		RecoveryTicks: 8,
		SpeedStat:     stats.CastSpeed,
		Range:         fm.FromInt(12), // acquisition reach from the caster
		Chains:        2,              // three targets zapped per cast, base
		ShockChance:   fm.FromMilli(350),
	}
	// Wild lightning rolls; per-target it sits between spark and fireball —
	// the guaranteed multi-hit is the power budget.
	arc.BaseMin[core.Lightning] = fm.FromInt(4)
	arc.BaseMax[core.Lightning] = fm.FromInt(26)

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
		ProjTTL:       36,                // 1.2s ≈ 17u — a lob, not an artillery barrage
		ProjRadius:    fm.FromMilli(550), // a fat bone — harder to sidestep than an arrow
	}
	boneVolley.BaseMin[core.Physical] = fm.FromInt(12)
	boneVolley.BaseMax[core.Physical] = fm.FromInt(18)

	adrenaline := &core.SkillDef{
		ID:            "adrenaline",
		Name:          "Adrenaline",
		Cuttable:      true,
		Kind:          core.SkillBuff,
		Tags:          stats.T(stats.TagSpell),
		ManaCost:      fm.FromInt(15),
		WindupTicks:   6, // 0.2s — a quick shout, not a cast
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		SelfBuff:      "adrenaline",
	}

	// --- The Barrow King's staged arsenal. Every attack is a readable
	// telegraph sequence: the fight is about moving on cue, not stats.

	// Triple slam: two tracked hits then a bigger, harder finisher. Each
	// zone locks where you STAND when its telegraph appears — keep moving.
	barrowSlam := &core.SkillDef{
		ID:            "barrow_slam",
		Name:          "Barrow Slam",
		Kind:          core.SkillStaged,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.One,
		SpeedStat:     stats.AttackSpeed,
		Range:         fm.FromInt(6), // AI engages inside this
		Stages: []core.SkillStage{
			{Ticks: 24, Effect: core.StageBlast, Aim: core.StageAimTarget, Radius: fm.FromMilli(2200)},
			{Ticks: 15, Effect: core.StageBlast, Aim: core.StageAimTarget, Radius: fm.FromMilli(2200)},
			{Ticks: 21, Effect: core.StageBlast, Aim: core.StageAimTarget, Radius: fm.FromMilli(3200), DamageScale: fm.FromMilli(1500)},
			{Ticks: 27}, // recovery: the punish window
		},
	}
	barrowSlam.BaseMin[core.Physical] = fm.FromInt(16)
	barrowSlam.BaseMax[core.Physical] = fm.FromInt(24)

	// Ring volley: one circle of slow bones, gaps to slip through.
	graveVolley := &core.SkillDef{
		ID:            "grave_volley",
		Name:          "Grave Volley",
		Kind:          core.SkillStaged,
		Tags:          stats.T(stats.TagAttack, stats.TagProjectile, stats.TagPhysical),
		Effectiveness: fm.One,
		SpeedStat:     stats.AttackSpeed,
		ProjSpeed:     fm.FromInt(11),
		ProjTTL:       48, // 1.6s ≈ 18u — crosses the arena, dodged not outrun
		ProjRadius:    fm.FromMilli(500),
		Stages: []core.SkillStage{
			{Ticks: 27, Effect: core.StageRing, Aim: core.StageAimTarget, RingStep: 3, Radius: fm.FromMilli(2500)},
			{Ticks: 21}, // recovery
		},
	}
	graveVolley.BaseMin[core.Physical] = fm.FromInt(10)
	graveVolley.BaseMax[core.Physical] = fm.FromInt(16)

	// Enraged volley: two rings, the second skewed to bisect the first's
	// gaps — standing still eats the second ring.
	graveStorm := &core.SkillDef{
		ID:            "grave_storm",
		Name:          "Grave Storm",
		Kind:          core.SkillStaged,
		Tags:          stats.T(stats.TagAttack, stats.TagProjectile, stats.TagPhysical),
		Effectiveness: fm.One,
		SpeedStat:     stats.AttackSpeed,
		ProjSpeed:     fm.FromInt(11),
		ProjTTL:       48,
		ProjRadius:    fm.FromMilli(500),
		Stages: []core.SkillStage{
			{Ticks: 24, Effect: core.StageRing, Aim: core.StageAimTarget, RingStep: 3, Radius: fm.FromMilli(2500)},
			{Ticks: 12, Effect: core.StageRing, Aim: core.StageAimTarget, RingStep: 3, RingSkew: 1, Radius: fm.FromMilli(2500)},
			{Ticks: 27}, // recovery
		},
	}
	graveStorm.BaseMin[core.Physical] = fm.FromInt(10)
	graveStorm.BaseMax[core.Physical] = fm.FromInt(16)

	// The first minion skill — rides the spawn queue. Skeletons fight at
	// the gem's level, heel to their summoner, and their kills credit the
	// player (XP, orbs, flask charges).
	summonSkeleton := &core.SkillDef{
		ID:            "summon_skeleton",
		Name:          "Summon Skeleton",
		Cuttable:      true,
		Kind:          core.SkillSummon,
		Tags:          stats.T(stats.TagSpell),
		ManaCost:      fm.FromInt(25),
		WindupTicks:   15, // 0.5s rite
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		SummonDef:     "skeleton_warrior",
		SummonCount:   1,
		SummonCap:     3,
	}

	// The melee build's bread and butter — without it, the cuttable pool
	// had no melee at all. A spin in place: every enemy in reach takes a
	// full weapon-scaled hit.
	sweep := &core.SkillDef{
		ID:            "sweep",
		Name:          "Sweep",
		Cuttable:      true,
		Kind:          core.SkillNova,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.FromMilli(1100), // melee premium: flat adds hit harder here
		ManaCost:      fm.FromInt(7),
		WindupTicks:   11, // ~0.37s swing
		RecoveryTicks: 7,
		SpeedStat:     stats.AttackSpeed,
		AoERadius:     fm.FromMilli(2700),
	}
	sweep.BaseMin[core.Physical] = fm.FromInt(9)
	sweep.BaseMax[core.Physical] = fm.FromInt(14)

	// --- The Grave Tyrant's kit: the floor-10 apex fight, composing the
	// staged machinery with the spawn queue.

	// Expanding ripples from where the Tyrant stands: ring one is a
	// warning, ring three owns the room. Dodge by leaving — or by stepping
	// back in behind a ring that already broke.
	tyrantQuake := &core.SkillDef{
		ID:            "tyrant_quake",
		Name:          "Tyrant's Quake",
		Kind:          core.SkillStaged,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.One,
		SpeedStat:     stats.AttackSpeed,
		Range:         fm.FromInt(7), // AI engages inside this
		Stages: []core.SkillStage{
			{Ticks: 24, Effect: core.StageBlast, Aim: core.StageAimSelf, Radius: fm.FromMilli(2500)},
			{Ticks: 15, Effect: core.StageBlast, Aim: core.StageAimSelf, Radius: fm.FromMilli(4500)},
			{Ticks: 15, Effect: core.StageBlast, Aim: core.StageAimSelf, Radius: fm.FromMilli(6500), DamageScale: fm.FromMilli(1400)},
			{Ticks: 30}, // recovery: the punish window
		},
	}
	tyrantQuake.BaseMin[core.Physical] = fm.FromInt(18)
	tyrantQuake.BaseMax[core.Physical] = fm.FromInt(26)

	// The Tyrant doesn't fight alone for long.
	raiseThralls := &core.SkillDef{
		ID:            "raise_thralls",
		Name:          "Raise Thralls",
		Kind:          core.SkillSummon,
		Tags:          stats.T(stats.TagSpell),
		WindupTicks:   24, // 0.8s rite — your window to reposition
		RecoveryTicks: 12,
		SpeedStat:     stats.CastSpeed,
		SummonDef:     "risen_thrall",
		SummonCount:   3,
		SummonCap:     6,
	}

	// The marksman: the first ranged minion — a skeleton with a bow that
	// shoots over the warriors' shoulders. Two of them, durable.
	summonMarksman := &core.SkillDef{
		ID:            "summon_marksman",
		Name:          "Summon Marksman",
		Cuttable:      true,
		Kind:          core.SkillSummon,
		Tags:          stats.T(stats.TagSpell),
		ManaCost:      fm.FromInt(30),
		WindupTicks:   15, // 0.5s rite, same cadence as the warrior
		RecoveryTicks: 9,
		SpeedStat:     stats.CastSpeed,
		SummonDef:     "skeleton_marksman",
		SummonCount:   1,
		SummonCap:     2,
	}

	// The raging spirit: a short-lived flaming skull that exists only to
	// bite something. Cheap and fast to cast — the button you mash — with
	// the 8s lifespan doing the balancing (cap 5, so a spam keeps ~5 up).
	summonSpirit := &core.SkillDef{
		ID:            "summon_raging_spirit",
		Name:          "Summon Raging Spirit",
		Cuttable:      true,
		Kind:          core.SkillSummon,
		Tags:          stats.T(stats.TagSpell, stats.TagFire),
		ManaCost:      fm.FromInt(12),
		WindupTicks:   9, // 0.3s — spammable
		RecoveryTicks: 6,
		SpeedStat:     stats.CastSpeed,
		SummonDef:     "raging_spirit",
		SummonCount:   1,
		SummonCap:     5,
		SummonTTL:     8 * core.TicksPerSecond,
	}

	// The spirit's bite — fire-flavored fast melee, minion-only.
	spiritBite := &core.SkillDef{
		ID:            "spirit_bite",
		Name:          "Spirit Bite",
		Kind:          core.SkillMelee,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagFire),
		Effectiveness: fm.One,
		WindupTicks:   9, // 0.3s snap
		RecoveryTicks: 6,
		SpeedStat:     stats.AttackSpeed,
		Range:         fm.FromMilli(1500),
	}
	spiritBite.BaseMin[core.Fire] = fm.FromInt(4)
	spiritBite.BaseMax[core.Fire] = fm.FromInt(7)

	// Auras: toggled reservation gems. No radius — an aura covers the
	// caster and every minion they own, wherever those stand.
	anger := &core.SkillDef{
		ID:       "anger",
		Name:     "Anger",
		Cuttable: true,
		Kind:     core.SkillAura,
		Tags:     stats.T(stats.TagSpell, stats.TagFire),
		// The toggle is a short cast; the price is the reservation.
		WindupTicks:   9,
		RecoveryTicks: 6,
		SpeedStat:     stats.CastSpeed,
		Reserve:       fm.FromMilli(350), // 35% of max mana while it burns
		AuraMods: []core.BuffMod{
			// Flat fire on every hit — the classic offense aura, and a
			// skeleton army's favorite campfire.
			{Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagFire), Value: fm.FromInt(5)},
		},
	}

	determination := &core.SkillDef{
		ID:            "determination",
		Name:          "Determination",
		Cuttable:      true,
		Kind:          core.SkillAura,
		Tags:          stats.T(stats.TagSpell),
		WindupTicks:   9,
		RecoveryTicks: 6,
		SpeedStat:     stats.CastSpeed,
		Reserve:       fm.FromMilli(350),
		AuraMods: []core.BuffMod{
			// The defensive counterpart: half again your armour.
			{Stat: stats.Armour, Layer: stats.LayerIncreased, Value: fm.FromMilli(500)},
		},
	}

	// The carrion husk's own slam: zombie_slam's arc with rot on it — the
	// monster that teaches poison (stacking chaos DoT).
	putridSlam := &core.SkillDef{
		ID:            "putrid_slam",
		Name:          "Putrid Slam",
		Kind:          core.SkillMelee,
		Tags:          stats.T(stats.TagAttack, stats.TagMelee, stats.TagPhysical),
		Effectiveness: fm.One,
		WindupTicks:   18, // 0.6s telegraph, like the zombie's
		RecoveryTicks: 12,
		SpeedStat:     stats.AttackSpeed,
		Range:         fm.FromMilli(1800),
		PoisonChance:  fm.FromMilli(300),
	}
	putridSlam.BaseMin[core.Physical] = fm.FromInt(8)
	putridSlam.BaseMax[core.Physical] = fm.FromInt(12)

	return []*core.SkillDef{fireball, slam, frostNova, spark, boneArrow, adrenaline, claws, arcBolt, arc, colossusSlam, boneVolley, barrowSlam, graveVolley, graveStorm, summonSkeleton, sweep, tyrantQuake, raiseThralls, summonMarksman, summonSpirit, spiritBite, putridSlam, anger, determination}
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
		// Gems-only: no innate skills. A fresh exile wakes with one uncut
		// skill gem — pick your own starter from its draft of three;
		// everything after that comes from uncut drops.
		StartingUncut: 1,
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
		StunImmune:  true,
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

	// The floor-milestone boss: every 5th floor, parked on the stairs.
	// Slower and far tougher than the colossus, and every attack is a
	// staged telegraph sequence — the first fight that's about the player
	// playing well. Always spawned rare; below half life it turns to
	// grave_storm (the AI reads life, no state).
	barrowKing := &core.ActorDef{
		ID:     "barrow_king",
		Name:   "The Barrow King",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(1300),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(550),
			stats.MoveSpeed:  fm.FromMilli(2400),
			stats.Accuracy:   fm.FromInt(140),
			stats.Armour:     fm.FromInt(50),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:      []string{"barrow_slam", "grave_volley", "grave_storm"},
		AI:          "boss_king",
		StunImmune:  true,
		AggroRadius: fm.FromInt(20),
		LeashRadius: fm.FromInt(16), // holds its barrow like the colossus holds stairs
		LootTable:   "king_drops",
		Level:       1,
		XPValue:     900,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(40)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(45)},
		},
	}

	// The splitter: a slow, swollen lurcher that bursts into two ghouls on
	// death — killing it is a commitment, not a checkbox. Rides the spawn
	// queue (RISKS #2 made real).
	husk := &core.ActorDef{
		ID:     "carrion_husk",
		Name:   "Carrion Husk",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(750),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(85),
			stats.MoveSpeed:  fm.FromMilli(2600),
			stats.Accuracy:   fm.FromInt(80),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:          []string{"putrid_slam"},
		AI:              "melee_chaser",
		AggroRadius:     fm.FromInt(14),
		LeashRadius:     fm.FromInt(18),
		LootTable:       "ghoul_drops",
		Level:           1,
		XPValue:         25, // the ghouls inside pay their own way
		DeathSpawnDef:   "ghoul",
		DeathSpawnCount: 2,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(10)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(40)},
		},
	}

	// The player's skeleton: fast enough to keep up, cheap enough to lose.
	// TeamPlayers makes every hostility check just work; the Owner link
	// (set at summon) handles credit and the heel behavior.
	skeleton := &core.ActorDef{
		ID:     "skeleton_warrior",
		Name:   "Skeleton Warrior",
		Team:   core.TeamPlayers,
		Radius: fm.FromMilli(450),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(45),
			stats.MoveSpeed:  fm.FromMilli(5200),
			stats.Accuracy:   fm.FromInt(90),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:      []string{"ghoul_claws"},
		AI:          "minion_melee",
		AggroRadius: fm.FromInt(9),
		Level:       1,
		XPValue:     0, // killing someone's skeleton pays nothing
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(7)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(50)},
		},
	}

	// The apex fight: every 10th floor. Nearly immobile — the quake owns
	// the ground around it and the thralls chase for it. Kill the adds and
	// punish the recovery, or drown.
	tyrant := &core.ActorDef{
		ID:     "grave_tyrant",
		Name:   "The Grave Tyrant",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(1400),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(900),
			stats.MoveSpeed:  fm.FromMilli(1800),
			stats.Accuracy:   fm.FromInt(150),
			stats.Armour:     fm.FromInt(70),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:      []string{"tyrant_quake", "raise_thralls", "bone_volley"},
		AI:          "boss_tyrant",
		StunImmune:  true,
		AggroRadius: fm.FromInt(20),
		LeashRadius: fm.FromInt(15),
		LootTable:   "tyrant_drops",
		Level:       1,
		XPValue:     2200,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(50)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(45)},
		},
	}

	// The Tyrant's disposable dead: fast, frail, and endless. XP pays a
	// little so clearing them isn't pure chore.
	thrall := &core.ActorDef{
		ID:     "risen_thrall",
		Name:   "Risen Thrall",
		Team:   core.TeamMonsters,
		Radius: fm.FromMilli(420),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(30),
			stats.MoveSpeed:  fm.FromMilli(5400),
			stats.Accuracy:   fm.FromInt(80),
			stats.CritChance: fm.FromMilli(50),
		}),
		Skills:      []string{"ghoul_claws"},
		AI:          "minion_melee",
		AggroRadius: fm.FromInt(12),
		Level:       1,
		XPValue:     8,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(4)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(45)},
		},
	}

	// The marksman: the durable ranged minion. Shoots bone arrows from the
	// back line, kites like the monster archer, heels like the warrior.
	marksman := &core.ActorDef{
		ID:     "skeleton_marksman",
		Name:   "Skeleton Marksman",
		Team:   core.TeamPlayers,
		Radius: fm.FromMilli(450),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(30),
			stats.MoveSpeed:  fm.FromMilli(5200),
			stats.Accuracy:   fm.FromInt(95),
			stats.CritChance: fm.FromMilli(60),
		}),
		Skills:         []string{"bone_arrow"},
		AI:             "minion_ranged",
		AggroRadius:    fm.FromInt(11),
		PreferredRange: fm.FromInt(9), // inside the leash so it never strands
		Level:          1,
		XPValue:        0, // killing someone's minion pays nothing
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(5)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(50)},
		},
	}

	// The raging spirit: a flaming skull with an 8s fuse (the summon's TTL
	// stamps it). Fast, fragile, all teeth.
	spirit := &core.ActorDef{
		ID:     "raging_spirit",
		Name:   "Raging Spirit",
		Team:   core.TeamPlayers,
		Radius: fm.FromMilli(350),
		BaseStats: baseStats(map[stats.StatID]fm.Fixed{
			stats.Life:       fm.FromInt(18),
			stats.MoveSpeed:  fm.FromMilli(6500),
			stats.Accuracy:   fm.FromInt(85),
			stats.CritChance: fm.FromMilli(80),
		}),
		Skills:      []string{"spirit_bite"},
		AI:          "minion_melee",
		AggroRadius: fm.FromInt(10),
		Level:       1,
		XPValue:     0,
		PerLevel: []core.BuffMod{
			{Stat: stats.Life, Layer: stats.LayerFlat, Value: fm.FromInt(3)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(55)},
		},
	}

	return []*core.ActorDef{player, zombie, archer, dummy, ghoul, mage, colossus, barrowKing, husk, skeleton, tyrant, thrall, marksman, spirit}
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
			Min: fm.FromInt(2), Max: fm.FromInt(5), Step: fm.One, Weight: 100,
			Families: flatDmg,
		},
		{
			ID: "flat_cold_damage", Group: "added_cold", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagCold),
			Min: fm.FromInt(2), Max: fm.FromInt(5), Step: fm.One, Weight: 100,
			Families: flatDmg,
		},
		{
			ID: "flat_lightning_damage", Group: "added_lightning", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagLightning),
			Min: fm.FromInt(1), Max: fm.FromInt(7), Step: fm.One, Weight: 100,
			Families: flatDmg,
		},
		{
			ID: "flat_phys_damage", Group: "added_phys", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagPhysical),
			Min: fm.FromInt(2), Max: fm.FromInt(4), Step: fm.One, Weight: 90,
			Families: flatDmg,
		},
		{
			ID: "increased_spell_damage", Group: "spell_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagSpell),
			Min: fm.FromMilli(80), Max: fm.FromMilli(150), Step: fm.FromMilli(10), Weight: 70, // 8–15%
			Families: caster,
		},
		{
			ID: "increased_fire_damage", Group: "fire_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagFire),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Step: fm.FromMilli(10), Weight: 60, // 10–20%
			Families: incEle,
		},
		{
			ID: "increased_cold_damage", Group: "cold_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagCold),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Step: fm.FromMilli(10), Weight: 60,
			Families: incEle,
		},
		{
			ID: "increased_lightning_damage", Group: "lightning_damage", Kind: core.Prefix,
			Stat: stats.Damage, Layer: stats.LayerIncreased, Tags: stats.T(stats.TagLightning),
			Min: fm.FromMilli(100), Max: fm.FromMilli(200), Step: fm.FromMilli(10), Weight: 60,
			Families: incEle,
		},
		// --- prefixes: defences and pools
		{
			ID: "flat_life", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(8), Max: fm.FromInt(20), Step: fm.One, Weight: 100,
			Families: lifes,
		},
		{
			ID: "flat_life_greater", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(21), Max: fm.FromInt(35), Step: fm.One, Weight: 35, ILvl: 5,
			Families: lifes,
		},
		{
			ID: "flat_life_grand", Group: "life", Kind: core.Prefix,
			Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(36), Max: fm.FromInt(55), Step: fm.One, Weight: 18, ILvl: 12,
			Families: lifes,
		},
		{
			ID: "flat_mana", Group: "mana", Kind: core.Prefix,
			Stat: stats.Mana, Layer: stats.LayerFlat,
			Min: fm.FromInt(8), Max: fm.FromInt(18), Step: fm.One, Weight: 90,
			Families: manas,
		},
		{
			ID: "flat_armour", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(30), Step: fm.One, Weight: 80,
			Families: armours,
		},
		{
			ID: "flat_armour_greater", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(31), Max: fm.FromInt(55), Step: fm.One, Weight: 25, ILvl: 5,
			Families: armours,
		},
		{
			ID: "flat_armour_grand", Group: "armour", Kind: core.Prefix,
			Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(56), Max: fm.FromInt(85), Step: fm.One, Weight: 12, ILvl: 12,
			Families: armours,
		},
		{
			ID: "flat_evasion", Group: "evasion", Kind: core.Prefix,
			Stat: stats.Evasion, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(30), Step: fm.One, Weight: 80,
			Families: evasions,
		},
		{
			ID: "flat_energy_shield", Group: "energy_shield", Kind: core.Prefix,
			Stat: stats.EnergyShield, Layer: stats.LayerFlat,
			Min: fm.FromInt(8), Max: fm.FromInt(20), Step: fm.One, Weight: 70,
			Families: esSlots,
		},
		{
			ID: "life_regen", Group: "life_regen", Kind: core.Prefix,
			Stat: stats.LifeRegen, Layer: stats.LayerFlat,
			Min: fm.FromInt(1), Max: fm.FromInt(2), Step: fm.One, Weight: 60,
			Families: regens,
		},
		{
			ID: "mana_regen", Group: "mana_regen", Kind: core.Prefix,
			Stat: stats.ManaRegen, Layer: stats.LayerFlat,
			Min: fm.FromMilli(500), Max: fm.FromMilli(1500), Step: fm.FromMilli(100), Weight: 60,
			Families: regens,
		},
		// --- suffixes: resistances
		{
			ID: "fire_resistance", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(80), Max: fm.FromMilli(150), Step: fm.FromMilli(10), Weight: 100, // 8–15%
			Families: resists,
		},
		{
			ID: "fire_resistance_greater", Group: "fire_res", Kind: core.Suffix,
			Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(160), Max: fm.FromMilli(240), Step: fm.FromMilli(10), Weight: 30, ILvl: 8, // 16–24%
			Families: resists,
		},
		{
			ID: "cold_resistance", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(80), Max: fm.FromMilli(150), Step: fm.FromMilli(10), Weight: 100,
			Families: resists,
		},
		{
			ID: "cold_resistance_greater", Group: "cold_res", Kind: core.Suffix,
			Stat: stats.ColdRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(160), Max: fm.FromMilli(240), Step: fm.FromMilli(10), Weight: 30, ILvl: 8,
			Families: resists,
		},
		{
			ID: "lightning_resistance", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(80), Max: fm.FromMilli(150), Step: fm.FromMilli(10), Weight: 100,
			Families: resists,
		},
		{
			ID: "lightning_resistance_greater", Group: "lightning_res", Kind: core.Suffix,
			Stat: stats.LightningRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(160), Max: fm.FromMilli(240), Step: fm.FromMilli(10), Weight: 30, ILvl: 8,
			Families: resists,
		},
		{
			ID: "chaos_resistance", Group: "chaos_res", Kind: core.Suffix,
			Stat: stats.ChaosRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(40), Max: fm.FromMilli(120), Step: fm.FromMilli(10), Weight: 40, // 4–12%
			Families: resists,
		},
		// --- suffixes: offense and utility
		{
			ID: "crit_chance", Group: "crit", Kind: core.Suffix,
			Stat: stats.CritChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(10), Max: fm.FromMilli(30), Step: fm.FromMilli(10), Weight: 60, // 1–3%
			Families: crits,
		},
		{
			ID: "crit_multi", Group: "crit_multi", Kind: core.Suffix,
			Stat: stats.CritMulti, Layer: stats.LayerFlat,
			Min: fm.FromMilli(100), Max: fm.FromMilli(250), Step: fm.FromMilli(10), Weight: 40, // +10–25%
			Families: crits,
		},
		{
			ID: "increased_cast_speed", Group: "cast_speed", Kind: core.Suffix,
			Stat: stats.CastSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), Weight: 60, // 5–10%
			Families: caster,
		},
		{
			ID: "increased_attack_speed", Group: "attack_speed", Kind: core.Suffix,
			Stat: stats.AttackSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), Weight: 60,
			Families: attacks,
		},
		{
			ID: "increased_move_speed", Group: "move_speed", Kind: core.Suffix,
			Stat: stats.MoveSpeed, Layer: stats.LayerIncreased,
			Min: fm.FromMilli(40), Max: fm.FromMilli(80), Step: fm.FromMilli(10), Weight: 40, // 4–8%
			Families: boots,
		},
		{
			ID: "flat_accuracy", Group: "accuracy", Kind: core.Suffix,
			Stat: stats.Accuracy, Layer: stats.LayerFlat,
			Min: fm.FromInt(20), Max: fm.FromInt(50), Step: fm.One, Weight: 70,
			Families: accs,
		},
		{
			ID: "ignite_chance", Group: "ignite_chance", Kind: core.Suffix,
			Stat: stats.IgniteChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), Weight: 40, // 5–10%
			Families: procs,
		},
		{
			ID: "shock_chance", Group: "shock_chance", Kind: core.Suffix,
			Stat: stats.ShockChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), Weight: 40,
			Families: procs,
		},
		{
			// Life leech: a fraction of hit damage refills you. The sustain
			// stat that makes standing in melee viable — a deeper reward, so
			// it's ILvl-gated.
			ID: "life_leech", Group: "life_leech", Kind: core.Suffix,
			Stat: stats.LifeLeech, Layer: stats.LayerFlat,
			Min: fm.FromMilli(10), Max: fm.FromMilli(30), Step: fm.FromMilli(10), Weight: 30, ILvl: 4, // 1–3%
			Families: []core.SlotFamily{core.FamilyWeapon, core.FamilyRing, core.FamilyAmulet},
		},
		{
			// Extra block, stacking on the shield's implicit — the tank's
			// chase suffix, offhand-only and ILvl-gated.
			ID: "increased_block", Group: "block", Kind: core.Suffix,
			Stat: stats.Block, Layer: stats.LayerFlat,
			Min: fm.FromMilli(40), Max: fm.FromMilli(80), Step: fm.FromMilli(10), Weight: 35, ILvl: 6, // +4–8%
			Families: []core.SlotFamily{core.FamilyOffhand},
		},
		{
			// Light radius: further reach into the fog of war. Pure
			// presentation — the client sums equipped rolls into its lit
			// circle; nothing sim-side reads it. Appended last: the affix
			// table is ordered, reordering is replay-relevant.
			ID: "light_radius", Group: "light_radius", Kind: core.Suffix,
			Stat: stats.LightRadius, Layer: stats.LayerFlat,
			Min: fm.FromMilli(500), Max: fm.FromMilli(1500), Step: fm.FromMilli(500), Weight: 40, // +0.5–1.5u
			Families: []core.SlotFamily{core.FamilyHelmet, core.FamilyAmulet, core.FamilyRing},
		},
		{
			// Chance to bleed: physical hits tear a physical DoT that armour
			// and resists ignore — the melee attacker's proc suffix,
			// alongside ignite/shock chance. Appended last (ordered table).
			ID: "bleed_chance", Group: "bleed_chance", Kind: core.Suffix,
			Stat: stats.BleedChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), Weight: 40, // 5–10%
			Families: procs,
		},
		{
			// Chance to poison: phys/chaos hits stack a chaos DoT — attack
			// speed becomes a DoT multiplier. Appended last (ordered table).
			ID: "poison_chance", Group: "poison_chance", Kind: core.Suffix,
			Stat: stats.PoisonChance, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), Weight: 40, // 5–10%
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
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), // 5–10%
		}},
		{ID: "wooden_shield", Name: "Wooden Shield", Slot: core.FamilyOffhand, Implicit: &core.ImplicitDef{
			// A shield's identity is block — the reason to give up a second
			// weapon or a caster offhand.
			ID: "block", Stat: stats.Block, Layer: stats.LayerFlat,
			Min: fm.FromMilli(120), Max: fm.FromMilli(200), Step: fm.FromMilli(10), // 12–20% block
		}},
		{ID: "leather_cap", Name: "Leather Cap", Slot: core.FamilyHelmet, Implicit: &core.ImplicitDef{
			ID: "evasion", Stat: stats.Evasion, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(20), Step: fm.One,
		}},
		{ID: "leather_vest", Name: "Leather Vest", Slot: core.FamilyBody, Implicit: &core.ImplicitDef{
			ID: "armour", Stat: stats.Armour, Layer: stats.LayerFlat,
			Min: fm.FromInt(15), Max: fm.FromInt(30), Step: fm.One,
		}},
		{ID: "leather_gloves", Name: "Leather Gloves", Slot: core.FamilyGloves, Implicit: &core.ImplicitDef{
			ID: "accuracy", Stat: stats.Accuracy, Layer: stats.LayerFlat,
			Min: fm.FromInt(20), Max: fm.FromInt(40), Step: fm.One,
		}},
		{ID: "leather_boots", Name: "Leather Boots", Slot: core.FamilyBoots, Implicit: &core.ImplicitDef{
			ID: "move_speed", Stat: stats.MoveSpeed, Layer: stats.LayerFlat,
			Min: fm.FromMilli(200), Max: fm.FromMilli(400), Step: fm.FromMilli(100), // +0.2–0.4 u/s
		}},
		{ID: "bone_amulet", Name: "Bone Amulet", Slot: core.FamilyAmulet, Implicit: &core.ImplicitDef{
			ID: "fire_resistance", Stat: stats.FireRes, Layer: stats.LayerFlat,
			Min: fm.FromMilli(50), Max: fm.FromMilli(100), Step: fm.FromMilli(10), // 5–10%
		}},
		{ID: "iron_ring", Name: "Iron Ring", Slot: core.FamilyRing, Implicit: &core.ImplicitDef{
			ID: "mana", Stat: stats.Mana, Layer: stats.LayerFlat,
			Min: fm.FromInt(5), Max: fm.FromInt(10), Step: fm.One,
		}},
		{ID: "leather_belt", Name: "Leather Belt", Slot: core.FamilyBelt, Implicit: &core.ImplicitDef{
			ID: "life", Stat: stats.Life, Layer: stats.LayerFlat,
			Min: fm.FromInt(10), Max: fm.FromInt(20), Step: fm.One,
		}},
	}
}

func lootTableDefs() []*core.LootTableDef {
	return []*core.LootTableDef{
		{
			// Frontline trash: drops a bit under half the time, mostly plain
			// gear with the occasional magic piece.
			ID:                 "zombie_drops",
			DropChance:         fm.FromMilli(450),
			RarityWeights:      [3]uint32{60, 32, 8},
			SkillGemPermille:   27,
			SupportGemPermille: 15,
			UniquePermille:     4,
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "leather_belt",
			},
		},
		{
			// Squishier but better-connected: rarer drops, jewelry-leaning,
			// noticeably better rarity odds.
			ID:                 "archer_drops",
			DropChance:         fm.FromMilli(400),
			RarityWeights:      [3]uint32{45, 40, 15},
			SkillGemPermille:   33,
			SupportGemPermille: 21,
			UniquePermille:     5,
			Bases: []string{
				"rusty_sword", "bone_amulet", "iron_ring", "leather_cap",
				"leather_gloves", "leather_boots",
			},
		},
		{
			// Test/tuning target keeps the old always-drops behavior and the
			// full base list, so loot work stays easy to exercise.
			ID:                 "dummy_drops",
			DropChance:         fm.One,
			RarityWeights:      [3]uint32{50, 35, 15},
			SkillGemPermille:   300, // test target: gem flows stay easy to exercise
			SupportGemPermille: 300,
			UniquePermille:     100,
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
		{
			// Swarm trash: drops rarely — a pack of ghouls shouldn't carpet
			// the floor — but leans quick gear (boots, gloves, blades).
			ID:                 "ghoul_drops",
			DropChance:         fm.FromMilli(250),
			RarityWeights:      [3]uint32{65, 30, 5},
			SkillGemPermille:   18,
			SupportGemPermille: 12,
			UniquePermille:     3,
			Bases: []string{
				"rusty_sword", "leather_boots", "leather_gloves", "leather_belt",
			},
		},
		{
			// The guardian's hoard: always drops, rare-heavy, full base list —
			// and the rare monster hooks add two more attempts on top.
			// Guardians are the gem faucet: spawned rare (×3), so a kill is
			// near-certain to pay an uncut skill gem and likely a support.
			ID:                 "boss_drops",
			DropChance:         fm.One,
			RarityWeights:      [3]uint32{10, 45, 45},
			SkillGemPermille:   450,
			SupportGemPermille: 300,
			UniquePermille:     45,
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
		{
			// The Barrow King's hoard: the run's jackpot. Always drops,
			// rare-dominant, and near-certain gems — a boss kill should
			// change your build's trajectory. Rare-monster hooks add two
			// more attempts on top.
			ID:                 "king_drops",
			DropChance:         fm.One,
			RarityWeights:      [3]uint32{0, 40, 60},
			SkillGemPermille:   700,
			SupportGemPermille: 500,
			UniquePermille:     90,
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
		{
			// The Tyrant's vault: the deepest jackpot in the game — a fight
			// this committed should have real unique odds.
			ID:                 "tyrant_drops",
			DropChance:         fm.One,
			RarityWeights:      [3]uint32{0, 25, 75},
			SkillGemPermille:   850,
			SupportGemPermille: 650,
			UniquePermille:     180,
			Bases: []string{
				"rusty_sword", "wooden_shield", "leather_cap", "leather_vest",
				"leather_gloves", "leather_boots", "bone_amulet", "iron_ring",
				"leather_belt",
			},
		},
		{
			// Caster elite: stingy but jewelry-leaning with real rarity odds —
			// the mage is the pack member worth focusing for loot too.
			ID:                 "mage_drops",
			DropChance:         fm.FromMilli(350),
			RarityWeights:      [3]uint32{35, 45, 20},
			SkillGemPermille:   38,
			SupportGemPermille: 27,
			UniquePermille:     6,
			Bases: []string{
				"bone_amulet", "iron_ring", "leather_cap", "wooden_shield",
			},
		},
	}
}
