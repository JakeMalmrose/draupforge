package sim

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// BuildSnapshot encodes the full world plus this tick's events — the
// omniscient debug view (headless runner, TCP/nc wire).
func (s *Sim) BuildSnapshot() protocol.Snapshot {
	return s.BuildSnapshotFor(0, 0, s.EncodeEvents())
}

// EncodeEvents returns this tick's events in wire form. The server collects
// these every tick so that views sent at a lower rate still carry every
// event from the ticks in between.
func (s *Sim) EncodeEvents() []protocol.EventSnap {
	var out []protocol.EventSnap
	for _, ev := range s.W.LastEvents {
		out = append(out, protocol.EventSnap{
			Kind:   ev.Kind.String(),
			Actor:  uint64(ev.Actor),
			Other:  uint64(ev.Other),
			Amount: ev.Amount.Milli(),
			Note:   ev.Note,
			Crit:   ev.Crit,
		})
	}
	return out
}

// EncodeMap returns the wire form of the world's terrain, or nil for the
// open plane. Terrain is immutable, so hosts send this once (welcome frame).
func (s *Sim) EncodeMap() *protocol.MapSnap {
	g := s.W.Grid
	if g == nil {
		return nil
	}
	rows := make([]string, g.Height)
	buf := make([]byte, g.Width)
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			if g.Solid(x, y) {
				buf[x] = '#'
			} else {
				buf[x] = '.'
			}
		}
		rows[y] = string(buf)
	}
	return &protocol.MapSnap{Width: g.Width, Height: g.Height, Tile: g.Tile.Milli(), Rows: rows}
}

// BuildSnapshotFor encodes one viewer's view of the world: entities within
// radius of the viewer (the viewer always included), plus the given events
// filtered to entities in the view. A zero viewer, a non-positive radius, or
// a viewer no longer in the world (death screens still want to see the
// fight) all mean no filtering.
func (s *Sim) BuildSnapshotFor(viewer core.EntityID, radius fm.Fixed, events []protocol.EventSnap) protocol.Snapshot {
	w := s.W
	snap := protocol.Snapshot{Tick: w.Tick}

	filtering := false
	var center space.Vec2
	if viewer != 0 && radius > 0 {
		if va := w.ActorByID(viewer); va != nil {
			center, filtering = va.Pos, true
		}
	}
	include := func(pos space.Vec2) bool {
		return !filtering || space.Dist(pos, center) <= radius
	}
	// seen is lookup-only (events filter below) — never iterated.
	seen := make(map[uint64]bool)

	for _, a := range w.Actors {
		if !include(a.Pos) && a.ID != viewer {
			continue
		}
		seen[uint64(a.ID)] = true
		var rarity string
		var modNames []string
		if a.Rarity != core.RarityNormal {
			rarity = a.Rarity.String()
			for _, md := range a.Mods {
				modNames = append(modNames, md.Name)
			}
		}
		var passives []string
		for _, ps := range a.Passives {
			passives = append(passives, ps.ID)
		}
		var flasks []int64
		for _, ch := range a.FlaskCharges {
			flasks = append(flasks, int64(ch))
		}
		var orbs []int64
		for _, n := range a.Orbs {
			orbs = append(orbs, int64(n))
		}
		var gems []protocol.GemSnap
		for i := range a.Gems {
			g := &a.Gems[i]
			gs := protocol.GemSnap{
				Skill: g.Skill.ID, Level: g.Level, Sockets: g.Sockets,
				ManaCost: g.ManaCost().Milli(),
			}
			for _, sup := range g.Supports {
				if sup == nil {
					gs.Supports = append(gs.Supports, "")
				} else {
					gs.Supports = append(gs.Supports, sup.ID)
				}
			}
			gems = append(gems, gs)
		}
		var equipment []protocol.EquippedSnap
		for slot := core.EquipSlot(0); slot < core.EquipSlotCount; slot++ {
			if item := a.Equipment[slot]; item != nil {
				equipment = append(equipment, protocol.EquippedSnap{
					Slot: slot.String(),
					Item: itemSnap(*item),
				})
			}
		}
		var inventory []protocol.ItemSnap
		for _, item := range a.Inventory {
			inventory = append(inventory, itemSnap(item))
		}
		snap.Actors = append(snap.Actors, protocol.ActorSnap{
			ID:        uint64(a.ID),
			Def:       a.Def.ID,
			Team:      uint8(a.Team),
			Pos:       vec(a.Pos),
			Radius:    a.Def.Radius.Milli(),
			Life:      a.Life.Milli(),
			MaxLife:   a.MaxLife().Milli(),
			Mana:      a.Mana.Milli(),
			MaxMana:   a.MaxMana().Milli(),
			ES:        a.ES.Milli(),
			Action:    actionString(a.Action),
			Ail:       ailmentBits(a),
			InvSize:   a.Def.InventorySize,
			Rarity:    rarity,
			Mods:      modNames,
			Passives:  passives,
			Flasks:    flasks,
			Orbs:      orbs,
			Gems:      gems,
			Level:     a.Level,
			XP:        a.XP,
			XPNext:    xpNext(a.Level),
			Equipment: equipment,
			Inventory: inventory,
			Telegraph: telegraphFor(a),
		})
	}
	for _, p := range w.Projectiles {
		if !include(p.Pos) {
			continue
		}
		seen[uint64(p.ID)] = true
		snap.Projectiles = append(snap.Projectiles, protocol.ProjectileSnap{
			ID: uint64(p.ID), Skill: p.Skill.ID, Pos: vec(p.Pos), Radius: p.Skill.ProjRadius.Milli(),
		})
	}
	for _, d := range w.Drops {
		if !include(d.Pos) {
			continue
		}
		seen[uint64(d.ID)] = true
		snap.Drops = append(snap.Drops, protocol.DropSnap{ID: uint64(d.ID), Pos: vec(d.Pos), Item: itemSnap(d.Item)})
	}
	for _, ev := range events {
		// An event is relevant if either participant is in view or is the
		// viewer itself (your own death outlives your actor); participant-less
		// events are global.
		relevant := !filtering ||
			(ev.Actor == 0 && ev.Other == 0) ||
			seen[ev.Actor] || seen[ev.Other] ||
			ev.Actor == uint64(viewer) || ev.Other == uint64(viewer)
		if relevant {
			snap.Events = append(snap.Events, ev)
		}
	}
	return snap
}

// ailmentBits folds an actor's active ailments into the wire bitmask, so
// clients can paint burning/chilled/shocked without knowing the systems.
func ailmentBits(a *core.Actor) uint8 {
	var b uint8
	for _, d := range a.DoTs {
		if d.Type == core.Fire {
			b |= protocol.AilIgnited
		}
	}
	for _, st := range a.Statuses {
		switch st.Kind {
		case core.StatusChill:
			b |= protocol.AilChilled
		case core.StatusShock:
			b |= protocol.AilShocked
		case core.StatusBuff:
			b |= protocol.AilBuffed
		}
	}
	return b
}

// xpNext is the HUD denominator: XP to finish the current level, 0 at cap.
func xpNext(level int) int64 {
	if level >= progress.MaxLevel {
		return 0
	}
	return progress.XPToNext(level)
}

func vec(v space.Vec2) protocol.Vec {
	return protocol.Vec{X: v.X.Milli(), Y: v.Y.Milli()}
}

func itemSnap(item core.Item) protocol.ItemSnap {
	if item.Gem != nil {
		return protocol.ItemSnap{ID: uint64(item.ID), Rarity: "normal", Gem: &protocol.GemItemSnap{
			Support: item.Gem.Support, Level: item.Gem.Level, Choices: item.Gem.Choices,
		}}
	}
	out := protocol.ItemSnap{ID: uint64(item.ID), Base: item.Base.ID, Rarity: item.Rarity.String(), ItemLevel: item.ItemLevel}
	if imp := item.Base.Implicit; imp != nil {
		out.Implicit = &protocol.AffixSnap{ID: imp.ID, Value: item.Implicit.Milli()}
	}
	for _, af := range item.Affixes {
		out.Affixes = append(out.Affixes, protocol.AffixSnap{ID: af.Def.ID, Value: af.Value.Milli()})
	}
	if u := item.Unique; u != nil {
		out.Unique = &protocol.UniqueItemSnap{Name: u.Name, Desc: u.Desc, Mods: u.ModLines}
	}
	return out
}

func actionString(a core.Action) string {
	switch a.Kind {
	case core.ActionMove:
		return "move"
	case core.ActionSkill:
		if a.Skill.Kind == core.SkillStaged {
			// Effect stages read as windup, trailing pauses as recovery —
			// clients need no staged-specific vocabulary.
			if a.Skill.Stages[a.Stage].Effect != core.StageNone {
				return "windup:" + a.Skill.ID
			}
			return "recovery:" + a.Skill.ID
		}
		if a.Phase == core.PhaseWindup {
			return "windup:" + a.Skill.ID
		}
		return "recovery:" + a.Skill.ID
	default:
		return "idle"
	}
}

// telegraphFor is the wire's danger zone for an actor's pending effect: a
// staged skill's current blast/ring stage (exact lock point and countdown),
// or a legacy self-centered nova wind-up (Total unknown — it was scaled
// away at use time; clients infer it from the first Left they see).
func telegraphFor(a *core.Actor) *protocol.TelegraphSnap {
	if a.Action.Kind != core.ActionSkill {
		return nil
	}
	sk := a.Action.Skill
	if sk.Kind == core.SkillStaged {
		st := sk.Stages[a.Action.Stage]
		if st.Effect == core.StageNone || st.Radius <= 0 {
			return nil
		}
		// A blast lands on its locked aim; a ring's danger emanates from
		// the caster (Radius is authored as the "get clear" zone).
		center := a.Action.StageAim
		if st.Effect == core.StageRing {
			center = a.Pos
		}
		return &protocol.TelegraphSnap{
			X: center.X.Milli(), Y: center.Y.Milli(),
			Radius: st.Radius.Milli(),
			Left:   a.Action.TicksLeft, Total: a.Action.StageTicks[a.Action.Stage],
		}
	}
	if sk.Kind == core.SkillNova && a.Action.Phase == core.PhaseWindup && sk.AoERadius > 0 {
		return &protocol.TelegraphSnap{
			X: a.Pos.X.Milli(), Y: a.Pos.Y.Milli(),
			Radius: sk.AoERadius.Milli(),
			Left:   a.Action.TicksLeft,
		}
	}
	return nil
}

// DecodeCommand converts a wire command to the sim's internal form.
func DecodeCommand(c protocol.Command) (core.Command, error) {
	out := core.Command{
		Actor:    core.EntityID(c.Actor),
		Point:    space.V(fm.FromMilli(c.X), fm.FromMilli(c.Y)),
		Skill:    c.Skill,
		TargetID: core.EntityID(c.Target),
	}
	if c.Slot != "" {
		slot, ok := core.ParseEquipSlot(c.Slot)
		if !ok {
			return core.Command{}, fmt.Errorf("protocol: unknown equip slot %q", c.Slot)
		}
		out.Slot, out.HasSlot = slot, true
	}
	switch c.Kind {
	case "move":
		out.Kind = core.CmdMove
	case "use_skill":
		out.Kind = core.CmdUseSkill
	case "stop":
		out.Kind = core.CmdStop
	case "equip":
		out.Kind = core.CmdEquip
	case "pickup":
		out.Kind = core.CmdPickup
	case "unequip":
		out.Kind = core.CmdUnequip
	case "drop_item":
		out.Kind = core.CmdDropItem
	case "choose_passive":
		out.Kind = core.CmdChoosePassive
		out.Passive = c.Passive
	case "use_flask":
		out.Kind = core.CmdUseFlask
	case "apply_orb":
		orb, ok := core.ParseOrbKind(c.Orb)
		if !ok {
			return core.Command{}, fmt.Errorf("protocol: unknown orb %q", c.Orb)
		}
		out.Kind = core.CmdApplyOrb
		out.Orb = orb
	case "cut_skill":
		out.Kind = core.CmdCutSkill
		out.Choice, out.GemIndex, out.Replace = c.Choice, c.Gem, c.Replace
	case "level_gem":
		out.Kind = core.CmdLevelGem
		out.GemIndex = c.Gem
	case "cut_support":
		out.Kind = core.CmdCutSupport
		out.Choice, out.GemIndex, out.Socket = c.Choice, c.Gem, c.Socket
	case "add_socket":
		out.Kind = core.CmdAddSocket
		out.GemIndex = c.Gem
	default:
		return core.Command{}, fmt.Errorf("protocol: unknown command kind %q", c.Kind)
	}
	return out, nil
}
