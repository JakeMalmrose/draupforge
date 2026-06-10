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
		snap.Actors = append(snap.Actors, protocol.ActorSnap{
			ID:      uint64(a.ID),
			Def:     a.Def.ID,
			Team:    uint8(a.Team),
			Pos:     vec(a.Pos),
			Life:    a.Life.Milli(),
			MaxLife: a.MaxLife().Milli(),
			Mana:    a.Mana.Milli(),
			MaxMana: a.MaxMana().Milli(),
			ES:      a.ES.Milli(),
			Action:  actionString(a.Action),
		})
	}
	for _, p := range w.Projectiles {
		snap.Projectiles = append(snap.Projectiles, protocol.ProjectileSnap{
			ID: uint64(p.ID), Skill: p.Skill.ID, Pos: vec(p.Pos),
		})
	}
	for _, d := range w.Drops {
		item := protocol.ItemSnap{Base: d.Item.Base.ID, Rarity: d.Item.Rarity.String()}
		for _, af := range d.Item.Affixes {
			item.Affixes = append(item.Affixes, protocol.AffixSnap{ID: af.Def.ID, Value: af.Value.Milli()})
		}
		snap.Drops = append(snap.Drops, protocol.DropSnap{ID: uint64(d.ID), Pos: vec(d.Pos), Item: item})
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
	default:
		return core.Command{}, fmt.Errorf("protocol: unknown command kind %q", c.Kind)
	}
	return out, nil
}
