package sim_test

// Flasks: charges-gated regen bursts. Use costs charges, kills feed them
// back, the bank is durable across zone transfers.

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func drink(s *sim.Sim, id core.EntityID, slot uint64) {
	s.Step([]core.Command{{Actor: id, Kind: core.CmdUseFlask, TargetID: core.EntityID(slot)}})
}

func TestFlaskUseCostsAndHeals(t *testing.T) {
	s := sim.New(content.DB(), 61)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)
	if len(a.FlaskCharges) != 2 || a.FlaskCharges[0] != core.FlaskMaxCharges {
		t.Fatalf("player flasks = %v, want two full", a.FlaskCharges)
	}

	a.Life = fm.FromInt(10) // hurt, to watch the sip work
	drink(s, id, 0)
	if a.FlaskCharges[0] != core.FlaskMaxCharges-core.FlaskUseCost {
		t.Fatalf("charges after use = %d", a.FlaskCharges[0])
	}
	buffed := false
	for _, st := range a.Statuses {
		if st.Buff != nil && st.Buff.ID == "life_flask" {
			buffed = true
		}
	}
	if !buffed {
		t.Fatal("no life_flask buff after drinking")
	}
	before := a.Life
	for i := 0; i < 30; i++ { // one second of the burst
		s.Step(nil)
	}
	// ~25/s from the flask (24.99 after per-tick fixed-point truncation).
	if a.Life < before+fm.FromMilli(24900) {
		t.Errorf("life after 1s of flask = %v, want >= %v", a.Life, before+fm.FromMilli(24900))
	}

	drink(s, id, 0) // 30 -> 0
	drink(s, id, 0) // empty: rejected
	if a.FlaskCharges[0] != 0 {
		t.Errorf("charges = %d, want 0 (third sip rejected)", a.FlaskCharges[0])
	}
}

func TestKillsFeedFlasks(t *testing.T) {
	s := sim.New(content.DB(), 62)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)
	a.FlaskCharges[0], a.FlaskCharges[1] = 0, core.FlaskMaxCharges-5

	dummy := mustSpawn(t, s, "training_dummy", 3000, 0)
	d := s.W.ActorByID(dummy)
	d.Life = fm.FromInt(1)
	s.Step([]core.Command{{Actor: id, Kind: core.CmdUseSkill, Skill: "spark", Point: d.Pos}})
	for i := 0; i < 60 && !d.Dead; i++ {
		s.Step(nil)
	}
	if !d.Dead {
		t.Fatal("dummy survived a spark at 1 life")
	}
	if a.FlaskCharges[0] != core.FlaskGainPerKill {
		t.Errorf("flask 0 charges = %d, want %d from the kill", a.FlaskCharges[0], core.FlaskGainPerKill)
	}
	if a.FlaskCharges[1] != core.FlaskMaxCharges {
		t.Errorf("flask 1 charges = %d, want capped at %d", a.FlaskCharges[1], core.FlaskMaxCharges)
	}
}

func TestFlaskChargesTransfer(t *testing.T) {
	s := sim.New(content.DB(), 63)
	id := mustSpawn(t, s, "player", 0, 0)
	a := s.W.ActorByID(id)
	a.FlaskCharges[0] = 45

	ch := core.ExtractCharacter(a)
	s2 := sim.New(content.DB(), 64)
	b, err := core.InjectCharacter(s2.W, ch, space.V(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if b.FlaskCharges[0] != 45 || b.FlaskCharges[1] != core.FlaskMaxCharges {
		t.Fatalf("transferred charges = %v, want [45 %d]", b.FlaskCharges, core.FlaskMaxCharges)
	}
}
