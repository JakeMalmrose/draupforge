package content

// Floor-modifier packages (ROADMAP v2 Track 2 item 3) — sheet-mod bundles
// the descent applies to every monster on a modded floor. Same shape as
// the rarity mods but a separate table: rarity rolls index MonsterMods,
// so growing THIS table never shifts a replay stream. Which floors roll
// which packages is host-layer policy (server/floormod.go); this table
// only defines what the packages do. Append-only, like every content
// table — actor saves reference these by ID.

import (
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

func floorModDefs() []*core.MonsterModDef {
	return []*core.MonsterModDef{
		{
			// "Monsters leech" — the roadmap's named example: every hit
			// taken feeds the pack, so attrition fights turn against you.
			ID: "floor_vampiric", Name: "Vampiric",
			Mods: []core.BuffMod{
				{Stat: stats.LifeLeech, Layer: stats.LayerFlat, Value: fm.FromMilli(60)},
			},
		},
	}
}
