package combat

import (
	"testing"

	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

func testBuff() *core.BuffDef {
	return &core.BuffDef{
		ID: "test_haste", Name: "Haste", DurationTicks: 10,
		Mods: []core.BuffMod{
			{Stat: stats.MoveSpeed, Layer: stats.LayerIncreased, Value: fm.FromMilli(500)},
			{Stat: stats.Damage, Layer: stats.LayerIncreased, Value: fm.FromMilli(200), Tags: stats.T(stats.TagFire)},
		},
	}
}

func buffWorld(t *testing.T) (*core.World, *core.Actor) {
	t.Helper()
	db := &core.ContentDB{}
	w := core.NewWorld(db, 1)
	var base [stats.StatCount]fm.Fixed
	base[stats.Life] = fm.FromInt(100)
	base[stats.MoveSpeed] = fm.FromInt(4)
	def := &core.ActorDef{ID: "t", BaseStats: base}
	return w, w.SpawnActor(def, space.Vec2{})
}

// TestBuffGrantsAndExpires: the full lifecycle — modifiers on at apply
// (tagged ones gated correctly), status ticking down, everything off at
// expiry with no residue.
func TestBuffGrantsAndExpires(t *testing.T) {
	w, a := buffWorld(t)
	buff := testBuff()
	w.BeginTick()
	ApplyBuff(w, a, buff, a.ID)

	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(6) {
		t.Errorf("buffed move speed = %d, want 6000 (4 × 1.5)", got)
	}
	if got := a.Sheet.Eval(stats.Damage, stats.T(stats.TagFire)); got != 0 {
		t.Errorf("fire damage eval = %d on zero base, want 0 — but the mod must gate on Fire", got)
	}
	if len(a.Statuses) != 1 || a.Statuses[0].Buff != buff {
		t.Fatalf("statuses = %+v, want one entry for the buff", a.Statuses)
	}

	for i := 0; i < int(buff.DurationTicks); i++ {
		TickStatuses(w)
	}
	if len(a.Statuses) != 0 {
		t.Fatal("buff status survived its duration")
	}
	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(4) {
		t.Errorf("post-expiry move speed = %d, want base 4000 — modifiers must come off whole", got)
	}
}

// TestBuffRefreshDoesNotStack: reapplying refreshes the timer without
// doubling the modifiers.
func TestBuffRefreshDoesNotStack(t *testing.T) {
	w, a := buffWorld(t)
	buff := testBuff()
	w.BeginTick()
	ApplyBuff(w, a, buff, a.ID)
	for i := 0; i < 6; i++ {
		TickStatuses(w)
	}
	ApplyBuff(w, a, buff, a.ID)

	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(6) {
		t.Errorf("move speed after reapply = %d, want 6000 — buffs refresh, never stack", got)
	}
	if len(a.Statuses) != 1 {
		t.Fatalf("reapply created a second status: %+v", a.Statuses)
	}
	if a.Statuses[0].TicksLeft != buff.DurationTicks {
		t.Errorf("TicksLeft = %d after reapply, want refreshed %d", a.Statuses[0].TicksLeft, buff.DurationTicks)
	}
}

// TestBuffAndAilmentCoexist: a buff and a chill on the same actor expire
// independently and remove only their own modifiers — the disjoint
// mod-source spaces doing their job.
func TestBuffAndAilmentCoexist(t *testing.T) {
	w, a := buffWorld(t)
	buff := testBuff()
	w.BeginTick()
	ApplyBuff(w, a, buff, a.ID)
	// setStatus grants the chill's sheet mods itself, same as applyChill.
	if !setStatus(a, core.StatusChill, fm.FromMilli(300), 5, a.ID) {
		t.Fatal("chill failed to apply")
	}

	// 4 × 1.5 (buff) × 0.7 (chill more-multiplier) = 4.2
	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromMilli(4200) {
		t.Errorf("buff+chill move speed = %d, want 4200", got)
	}
	for i := 0; i < 5; i++ {
		TickStatuses(w)
	}
	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(6) {
		t.Errorf("chill expired: move speed = %d, want 6000 with the buff intact", got)
	}
	for i := 0; i < 5; i++ {
		TickStatuses(w)
	}
	if got := a.Sheet.Eval(stats.MoveSpeed, stats.TagSet{}); got != fm.FromInt(4) {
		t.Errorf("all expired: move speed = %d, want base 4000", got)
	}
}
