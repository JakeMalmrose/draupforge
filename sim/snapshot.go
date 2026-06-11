package sim

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
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
			Equipment: equipment,
			Inventory: inventory,
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

func vec(v space.Vec2) protocol.Vec {
	return protocol.Vec{X: v.X.Milli(), Y: v.Y.Milli()}
}

func itemSnap(item core.Item) protocol.ItemSnap {
	out := protocol.ItemSnap{ID: uint64(item.ID), Base: item.Base.ID, Rarity: item.Rarity.String()}
	for _, af := range item.Affixes {
		out.Affixes = append(out.Affixes, protocol.AffixSnap{ID: af.Def.ID, Value: af.Value.Milli()})
	}
	return out
}

func actionString(a core.Action) string {
	switch a.Kind {
	case core.ActionMove:
		return "move"
	case core.ActionSkill:
		if a.Phase == core.PhaseWindup {
			return "windup:" + a.Skill.ID
		}
		return "recovery:" + a.Skill.ID
	default:
		return "idle"
	}
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
	default:
		return core.Command{}, fmt.Errorf("protocol: unknown command kind %q", c.Kind)
	}
	return out, nil
}
