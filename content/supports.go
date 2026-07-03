package content

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// supportDefs is the support-gem pool: what an uncut support gem's draft
// offers. Slice order feeds draft rolls — reordering is a replay-relevant
// change, same rule as the affix table; append at the end. The mix is
// deliberate: raw power that costs mana, powerful upside/powerful downside
// pairs, and situationally-good utility.
func supportDefs() []*core.SupportDef {
	return []*core.SupportDef{
		{
			ID: "brute_force", Name: "Brute Force",
			Desc:     "25% more damage",
			ManaMult: fm.FromMilli(1300),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(250)},
			},
		},
		{
			ID: "lesser_projectiles", Name: "Lesser Multiple Projectiles",
			Desc:     "+2 projectiles, 20% less damage",
			Requires: stats.T(stats.TagProjectile),
			ManaMult: fm.FromMilli(1200),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(-200)},
			},
			ExtraProjectiles: 2,
		},
		{
			ID: "greater_projectiles", Name: "Greater Multiple Projectiles",
			Desc:     "+4 projectiles, 35% less damage",
			Requires: stats.T(stats.TagProjectile),
			ManaMult: fm.FromMilli(1350),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(-350)},
			},
			ExtraProjectiles: 4,
		},
		{
			ID: "chain", Name: "Chain",
			Desc:     "projectiles chain twice, 30% less damage",
			Requires: stats.T(stats.TagProjectile),
			ManaMult: fm.FromMilli(1300),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(-300)},
			},
			Chains: 2,
		},
		{
			ID: "faster_casting", Name: "Faster Casting",
			Desc:     "30% increased cast speed",
			Requires: stats.T(stats.TagSpell),
			ManaMult: fm.FromMilli(1150),
			Mods: []core.BuffMod{
				{Stat: stats.CastSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(300)},
			},
		},
		{
			ID: "faster_attacks", Name: "Faster Attacks",
			Desc:     "25% increased attack speed",
			Requires: stats.T(stats.TagAttack),
			ManaMult: fm.FromMilli(1100),
			Mods: []core.BuffMod{
				{Stat: stats.AttackSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(250)},
			},
		},
		{
			ID: "added_fire", Name: "Added Fire Damage",
			Desc:     "adds 6 fire damage",
			ManaMult: fm.FromMilli(1200),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagFire), Value: fm.FromInt(6)},
			},
		},
		{
			ID: "added_lightning", Name: "Added Lightning Damage",
			Desc:     "adds 4 lightning damage",
			ManaMult: fm.FromMilli(1200),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagLightning), Value: fm.FromInt(4)},
			},
		},
		{
			// The conversion support — chills off anything. Converted damage
			// is scaled by mods of both its source and destination types.
			ID: "glaciate", Name: "Glaciate",
			Desc:     "half of fire and lightning damage converted to cold",
			ManaMult: fm.FromMilli(1100),
			Conversions: []core.ConversionDef{
				{From: core.Fire, To: core.Cold, Fraction: fm.Half},
				{From: core.Lightning, To: core.Cold, Fraction: fm.Half},
			},
		},
		{
			ID: "inspiration", Name: "Inspiration",
			Desc:     "30% reduced mana cost",
			ManaMult: fm.FromMilli(700),
		},
		{
			// The melee payoff: a big flat more-multiplier that only a melee
			// skill can socket. Pairs with Sweep and the melee attacks.
			ID: "ruthless", Name: "Ruthless",
			Desc:     "40% more melee damage",
			Requires: stats.T(stats.TagMelee),
			ManaMult: fm.FromMilli(1400),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(400)},
			},
		},
		{
			// Fire specialist: adds flat fire and amps the fire portion, so a
			// fireball or an added-fire build leans harder into ignite.
			ID: "immolate", Name: "Immolate",
			Desc:     "adds 8 fire damage, 25% more fire damage",
			ManaMult: fm.FromMilli(1300),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerFlat, Tags: stats.T(stats.TagFire), Value: fm.FromInt(8)},
				{Stat: stats.Damage, Layer: stats.LayerMore, Tags: stats.T(stats.TagFire), Value: fm.FromMilli(250)},
			},
		},
		{
			// The fan that doesn't cost you damage — one extra projectile and
			// a small more-multiplier, at a steep mana price. The aggressive
			// alternative to LMP/GMP's damage penalty.
			ID: "cannonade", Name: "Cannonade",
			Desc:     "+1 projectile, 15% more damage",
			Requires: stats.T(stats.TagProjectile),
			ManaMult: fm.FromMilli(1600),
			Mods: []core.BuffMod{
				{Stat: stats.Damage, Layer: stats.LayerMore, Value: fm.FromMilli(150)},
			},
			ExtraProjectiles: 1,
		},
	}
}
