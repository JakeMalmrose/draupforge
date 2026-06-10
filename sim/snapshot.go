package sim

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

// BuildSnapshot encodes the world's visible state after a Step.
func (s *Sim) BuildSnapshot() protocol.Snapshot {
	w := s.W
	snap := protocol.Snapshot{Tick: w.Tick}

	for _, a := range w.Actors {
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
			ID:      uint64(a.ID),
			Def:     a.Def.ID,
			Team:    uint8(a.Team),
			Pos:     vec(a.Pos),
			Radius:  a.Def.Radius.Milli(),
			Life:    a.Life.Milli(),
			MaxLife: a.MaxLife().Milli(),
			Mana:    a.Mana.Milli(),
			MaxMana: a.MaxMana().Milli(),
			ES:      a.ES.Milli(),
			Action:  actionString(a.Action),
			Equipment: equipment,
			Inventory: inventory,
		})
	}
	for _, p := range w.Projectiles {
		snap.Projectiles = append(snap.Projectiles, protocol.ProjectileSnap{
			ID: uint64(p.ID), Skill: p.Skill.ID, Pos: vec(p.Pos), Radius: p.Skill.ProjRadius.Milli(),
		})
	}
	for _, d := range w.Drops {
		snap.Drops = append(snap.Drops, protocol.DropSnap{ID: uint64(d.ID), Pos: vec(d.Pos), Item: itemSnap(d.Item)})
	}
	for _, ev := range w.LastEvents {
		snap.Events = append(snap.Events, protocol.EventSnap{
			Kind:   ev.Kind.String(),
			Actor:  uint64(ev.Actor),
			Other:  uint64(ev.Other),
			Amount: ev.Amount.Milli(),
			Note:   ev.Note,
		})
	}
	return snap
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
