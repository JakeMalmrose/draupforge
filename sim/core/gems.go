// Gems — the skill/support gem system. Player skills exist only as cut
// skill gems: each has a level (scaling the skill's base damage roll and
// mana cost) and support sockets. Support gems modify the socketed skill
// only — their modifiers fold into that skill's stat queries at use time,
// never onto the actor's sheet. Uncut gems drop as items carrying a
// pre-rolled draft of three choices; cutting is deterministic, the drop
// was the RNG.
package core

import (
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

const (
	MaxGemLevel = 20
	// MaxGemSockets caps what jeweller orbs can grow a gem to; every cut
	// gem starts with GemStartSockets.
	MaxGemSockets   = 4
	GemStartSockets = 1
	// MaxSkillGems is the cut-skill cap per actor — the skill bar's width.
	// Cutting a fifth skill must name a gem to replace.
	MaxSkillGems = 4
	// GemDraftSize is how many choices an uncut gem carries.
	GemDraftSize = 3
)

// GemDamageScale is the gem-level multiplier on the skill's own base damage
// roll: +10% of base per level past the first, linear (level 20 = ×2.9).
// Level 0 means "no gem" (monsters) and scales by One exactly.
func GemDamageScale(level int) fm.Fixed {
	if level <= 1 {
		return fm.One
	}
	return fm.One + fm.FromMilli(int64(100*(level-1)))
}

// GemManaScale is the gem-level multiplier on the skill's mana cost:
// +5% per level past the first — power outruns cost, but not for free.
func GemManaScale(level int) fm.Fixed {
	if level <= 1 {
		return fm.One
	}
	return fm.One + fm.FromMilli(int64(50*(level-1)))
}

// GemAuraScale is the gem-level multiplier on an aura's mod values: +5% of
// the authored value per level past the first — auras grow with drops like
// everything else, while their reservation stays flat.
func GemAuraScale(level int) fm.Fixed {
	if level <= 1 {
		return fm.One
	}
	return fm.One + fm.FromMilli(int64(50*(level-1)))
}

// ConversionDef moves a fraction of one damage type's pre-multiplier total
// (base roll + added flat) into another type. Converted portions are scaled
// by modifiers of both the source and destination types (DESIGN.md §3:
// base → added → converted, mods apply post-conversion).
type ConversionDef struct {
	From, To DamageType
	Fraction fm.Fixed // 0.5 = half
}

// SupportDef is a support gem: a named package of per-skill effects. Unlike
// a BuffDef its modifiers never touch the actor's sheet — they fold into
// the supported skill's queries only.
type SupportDef struct {
	ID   string
	Name string
	Desc string // one line for the cutting/socket UI

	// Requires gates legality: the skill's tags must contain all of these
	// (e.g. projectile supports need TagProjectile).
	Requires stats.TagSet

	// ManaMult multiplies the skill's mana cost (One = unchanged).
	ManaMult fm.Fixed

	// Mods fold into the supported skill's stat queries (Damage and the
	// speed stats); tags gate per-type application as on the sheet.
	Mods []BuffMod

	// Behavior levers.
	ExtraProjectiles int
	Chains           int
	Conversions      []ConversionDef
}

// Gem is one cut skill gem on an actor. Supports is socket-addressed:
// len(Supports) == Sockets, nil = empty socket.
type Gem struct {
	Skill    *SkillDef
	Level    int
	Sockets  int
	Supports []*SupportDef
	// AuraOn marks a running aura (SkillAura gems only). Durable like the
	// gem itself: it transfers with the character and re-applies its mods
	// at injection.
	AuraOn bool
}

// ManaCost is the gem's effective cost: base × level scale × each socketed
// support's multiplier, in socket order (fixed-point rounding makes order
// part of the contract).
func (g *Gem) ManaCost() fm.Fixed {
	c := fm.Mul(g.Skill.ManaCost, GemManaScale(g.Level))
	for _, s := range g.Supports {
		if s != nil {
			c = fm.Mul(c, s.ManaMult)
		}
	}
	return c
}

// HasSupport reports whether the gem already sockets the support (no
// duplicates within one gem; across gems is fine).
func (g *Gem) HasSupport(id string) bool {
	for _, s := range g.Supports {
		if s != nil && s.ID == id {
			return true
		}
	}
	return false
}

// GemCtx is the level/support context a skill use carries through its whole
// life: baked at use time (command gate), copied onto projectiles at the
// effect point and onto hits at resolution, so an in-flight projectile
// keeps the stats it was cast with whatever happens to the gem meanwhile.
// The zero value is "no gem" — the monster path.
type GemCtx struct {
	Level    int
	Supports []*SupportDef // compacted: no nil entries
}

// Ctx bakes a gem's context: supports compacted, slice freshly allocated so
// later socket edits can't reach into an in-flight cast.
func (g *Gem) Ctx() GemCtx {
	ctx := GemCtx{Level: g.Level}
	for _, s := range g.Supports {
		if s != nil {
			ctx.Supports = append(ctx.Supports, s)
		}
	}
	return ctx
}

// ExtraProjectiles sums the supports' extra-projectile counts.
func (c GemCtx) ExtraProjectiles() int {
	n := 0
	for _, s := range c.Supports {
		n += s.ExtraProjectiles
	}
	return n
}

// Chains sums the supports' chain counts.
func (c GemCtx) Chains() int {
	n := 0
	for _, s := range c.Supports {
		n += s.Chains
	}
	return n
}

// FoldSupportMods folds the context's support modifiers for one (stat, tags)
// query into sheet-evaluated Parts — same algebra as the evaluator: flat
// adds, increased shares the additive bucket, more compose in list order.
func (c GemCtx) FoldSupportMods(p stats.Parts, stat stats.StatID, tags stats.TagSet) stats.Parts {
	for _, s := range c.Supports {
		for _, m := range s.Mods {
			if m.Stat != stat || !tags.ContainsAll(m.Tags) {
				continue
			}
			switch m.Layer {
			case stats.LayerFlat:
				p.Flat += m.Value
			case stats.LayerIncreased:
				p.Increased += m.Value
			case stats.LayerMore:
				p.More = fm.Mul(p.More, fm.One+m.Value)
			}
		}
	}
	return p
}

// GemForSkill finds the actor's cut gem for a skill ID; nil if uncut.
func (a *Actor) GemForSkill(id string) *Gem {
	for i := range a.Gems {
		if a.Gems[i].Skill.ID == id {
			return &a.Gems[i]
		}
	}
	return nil
}

// GrantGem cuts a skill directly onto the actor (starting gems, script
// spawns, character injection): level clamped to [1, MaxGemLevel], fresh
// sockets. Callers ensure the skill isn't already cut.
func (a *Actor) GrantGem(sk *SkillDef, level int) *Gem {
	if level < 1 {
		level = 1
	}
	if level > MaxGemLevel {
		level = MaxGemLevel
	}
	a.Gems = append(a.Gems, Gem{
		Skill:    sk,
		Level:    level,
		Sockets:  GemStartSockets,
		Supports: make([]*SupportDef, GemStartSockets),
	})
	return &a.Gems[len(a.Gems)-1]
}

// UncutGem is the gem part of a dropped item: which kind, the level it was
// found at (skill gems only), and the pre-rolled draft of choices the
// cutting UI offers. Uncut items have no Base.
type UncutGem struct {
	Support bool
	Level   int
	Choices []string // GemDraftSize distinct skill or support IDs
}
