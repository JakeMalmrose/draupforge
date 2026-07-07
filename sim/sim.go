// Package sim wires the deterministic tick: it owns the phase order and the
// command-validation gate. Data types live in sim/core; system logic lives in
// the leaf packages; nothing below this package knows the phase order.
package sim

import (
	"fmt"

	"github.com/JakeMalmrose/draupforge/sim/ai"
	"github.com/JakeMalmrose/draupforge/sim/combat"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/items"
	"github.com/JakeMalmrose/draupforge/sim/progress"
	"github.com/JakeMalmrose/draupforge/sim/skills"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

type Sim struct {
	W *core.World
}

func New(db *core.ContentDB, seed uint64) *Sim {
	return &Sim{W: core.NewWorld(db, seed)}
}

// Load resumes a sim from a World.Save file. The restored world continues
// bit-exactly: same hashes, same RNG streams, same in-flight actions.
func Load(db *core.ContentDB, data []byte) (*Sim, error) {
	w, err := core.LoadWorld(db, data)
	if err != nil {
		return nil, err
	}
	return &Sim{W: w}, nil
}

// Spawn places an actor by content ID and returns its entity ID. On grid
// worlds the position is clamped to the nearest walkable spot, so scenario
// coordinates authored against a different layout still land somewhere legal.
func (s *Sim) Spawn(defID string, pos space.Vec2) (core.EntityID, error) {
	def := s.W.Content.Actors[defID]
	if def == nil {
		return 0, fmt.Errorf("sim: unknown actor def %q", defID)
	}
	if s.W.Grid != nil {
		p, ok := s.W.Grid.NearestWalkable(pos)
		if !ok {
			return 0, fmt.Errorf("sim: no walkable tile to spawn %q", defID)
		}
		pos = p
	}
	a := s.W.SpawnActor(def, pos)
	// A fresh character picks its first skill: StartingUncut rolls uncut
	// gems (three loot-stream draws each) straight into the bag. Lives
	// here rather than in SpawnActor — injection must never re-grant (the
	// bag transfers), and only root sim reaches into items.
	for i := 0; i < def.StartingUncut; i++ {
		a.Inventory = append(a.Inventory, items.RollUncutGem(s.W, false, 1))
	}
	return a.ID, nil
}

// GrantGem cuts a skill gem directly onto an actor (level clamped to
// [1, MaxGemLevel]) — the scenario/test path around the drop-and-cut loop.
// Rejected for unknown skills or skills the actor already has cut.
func (s *Sim) GrantGem(actor core.EntityID, skillID string, level int) error {
	a := s.W.ActorByID(actor)
	if a == nil {
		return fmt.Errorf("sim: unknown actor %d", actor)
	}
	sk := s.W.Content.Skills[skillID]
	if sk == nil {
		return fmt.Errorf("sim: unknown skill %q", skillID)
	}
	if a.GemForSkill(skillID) != nil {
		return fmt.Errorf("sim: actor already has %q cut", skillID)
	}
	a.GrantGem(sk, level)
	return nil
}

// GenerateMap rolls terrain from the world's map RNG stream and installs
// it. Call before any spawns; terrain is immutable afterwards.
func (s *Sim) GenerateMap(spec space.MapSpec) {
	s.W.Grid = space.GenerateRooms(spec, s.W.RNGMap)
}

// SpawnLeveled is Spawn with a level override (0 keeps the def's level):
// growth mods applied, pools filled at the new maxima. Floor-scaling
// spawners use this to hand out level-N packs.
func (s *Sim) SpawnLeveled(defID string, pos space.Vec2, level int) (core.EntityID, error) {
	id, err := s.Spawn(defID, pos)
	if err != nil || level <= 0 {
		return id, err
	}
	a := s.W.ActorByID(id)
	a.SetLevel(level)
	a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
	return id, nil
}

// scatterMinSpawnDist keeps scattered monsters out of the players' entry
// room — far enough to not be hit the instant you load in.
var scatterMinSpawnDist = fm.FromInt(10)

// ScatterSpawn places count actors on random walkable tiles (map RNG
// stream), preferring spots away from the player spawn point.
func (s *Sim) ScatterSpawn(defID string, count int) error {
	return s.ScatterSpawnLeveled(defID, count, 0)
}

// ScatterSpawnLeveled is ScatterSpawn with a level override (0 keeps the
// def's level).
func (s *Sim) ScatterSpawnLeveled(defID string, count, level int) error {
	return s.ScatterSpawnPack(defID, count, level, 0, 0)
}

// ScatterSpawnPack is ScatterSpawnLeveled with rarity pressure: each
// monster independently rolls rare (rarePermille‰) then magic
// (magicPermille‰); magic monsters take one modifier from
// Content.MonsterMods, rares two distinct ones (uniform picks). All draws
// come from RNGMap, interleaved per monster: position (with retries), then
// one rarity draw iff magicPermille+rarePermille > 0, then 1–2 mod draws
// by outcome. Zero chances keep the stream identical to
// ScatterSpawnLeveled, which existing goldens depend on.
func (s *Sim) ScatterSpawnPack(defID string, count, level int, magicPermille, rarePermille uint64) error {
	g := s.W.Grid
	if g == nil {
		return fmt.Errorf("sim: scatter spawn needs a generated map")
	}
	tiles := g.WalkableCenters()
	if len(tiles) == 0 {
		return fmt.Errorf("sim: map has no walkable tiles")
	}
	for i := 0; i < count; i++ {
		pos := tiles[s.W.RNGMap.Uint64n(uint64(len(tiles)))]
		for try := 0; try < 20 && space.Dist(pos, g.Spawn) < scatterMinSpawnDist; try++ {
			pos = tiles[s.W.RNGMap.Uint64n(uint64(len(tiles)))]
		}
		id, err := s.SpawnLeveled(defID, pos, level)
		if err != nil {
			return err
		}
		if magicPermille+rarePermille > 0 {
			s.rollMonsterRarity(s.W.ActorByID(id), magicPermille, rarePermille)
		}
	}
	return nil
}

// SpawnRareLeveled spawns a monster already rolled rare with two distinct
// mods (RNGMap picks) — the guaranteed-elite path for floor guardians.
// Rarity's usual hooks (XP x6, 3 drop attempts, wire tags) come along.
func (s *Sim) SpawnRareLeveled(defID string, pos space.Vec2, level int) (core.EntityID, error) {
	id, err := s.SpawnLeveled(defID, pos, level)
	if err != nil {
		return 0, err
	}
	a := s.W.ActorByID(id)
	pool := s.W.Content.MonsterMods
	if len(pool) == 0 {
		return id, nil
	}
	i := s.W.RNGMap.Uint64n(uint64(len(pool)))
	mods := []*core.MonsterModDef{pool[i]}
	if len(pool) > 1 {
		j := s.W.RNGMap.Uint64n(uint64(len(pool) - 1))
		if j >= i {
			j++
		}
		mods = append(mods, pool[j])
	}
	a.ApplyMonsterMods(core.RarityRare, mods)
	a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
	return id, nil
}

// rollMonsterRarity rolls one monster's rarity and modifiers off RNGMap
// and refills its pools at the new maxima.
func (s *Sim) rollMonsterRarity(a *core.Actor, magicPermille, rarePermille uint64) {
	pool := s.W.Content.MonsterMods
	if len(pool) == 0 {
		return
	}
	roll := s.W.RNGMap.Uint64n(1000)
	var rarity core.Rarity
	switch {
	case roll < rarePermille:
		rarity = core.RarityRare
	case roll < rarePermille+magicPermille:
		rarity = core.RarityMagic
	default:
		return
	}
	i := s.W.RNGMap.Uint64n(uint64(len(pool)))
	mods := []*core.MonsterModDef{pool[i]}
	if rarity == core.RarityRare && len(pool) > 1 {
		// Distinct second pick: roll over the remaining n-1 slots.
		j := s.W.RNGMap.Uint64n(uint64(len(pool) - 1))
		if j >= i {
			j++
		}
		mods = append(mods, pool[j])
	}
	a.ApplyMonsterMods(rarity, mods)
	a.Life, a.Mana, a.ES = a.MaxLife(), a.MaxMana(), a.MaxES()
}

// Step advances exactly one tick. The phase order below is the determinism
// contract — fixed, every tick, no exceptions. Callers must pass commands in
// a deterministic order (the server's job; tests and scripts are ordered by
// construction).
func (s *Sim) Step(cmds []core.Command) {
	w := s.W
	w.BeginTick()

	combat.Upkeep(w)               // regen, so this tick's casts see fresh mana
	applyCommands(w, cmds)         // player/network intent
	applyCommands(w, ai.Decide(w)) // monster intent, same validation gate
	skills.AdvanceActions(w)       // movement + windup/recovery; effects queue hits/buffs
	skills.Separate(w)             // soft monster de-overlap; no RNG, pure positions
	skills.UpdateProjectiles(w)    // flight + collision; impacts queue hits
	combat.ResolveBuffs(w)         // buff applications land before hits resolve
	combat.ResolveHits(w)          // the damage pipeline, in queue order
	combat.TickDoTs(w)             // ignites and friends
	combat.TickStatuses(w)         // chill/shock timers; modifiers off at expiry
	items.RollLoot(w)              // reacts to this tick's death events
	progress.AwardXP(w)            // ditto: XP and level-ups off the same deaths
	queueDeathSpawns(w)            // splitters queue their adds off the same deaths
	w.DrainSpawns()                // queued spawns materialize — the LAST actor phase:
	//                                newcomers take no action and eat no hit today

	w.EndTick()
}

// deathSpawnOffsets fans on-death adds around the corpse — fixed pattern,
// no RNG, clamped to walkable at drain. Cycles through for large counts.
var deathSpawnOffsets = []space.Vec2{
	space.V(fm.FromMilli(800), 0),
	space.V(fm.FromMilli(-800), 0),
	space.V(0, fm.FromMilli(800)),
	space.V(0, fm.FromMilli(-800)),
	space.V(fm.FromMilli(600), fm.FromMilli(600)),
	space.V(fm.FromMilli(-600), fm.FromMilli(-600)),
}

// queueDeathSpawns scans this tick's deaths for splitter defs and queues
// their adds at the corpse, at the dier's level. Runs after XP/loot so the
// kill pays out before the room refills.
func queueDeathSpawns(w *core.World) {
	for _, ev := range w.Events() {
		if ev.Kind != core.EvDeath {
			continue
		}
		a := w.ActorByID(ev.Actor)
		if a == nil || a.Def.DeathSpawnCount <= 0 {
			continue
		}
		def := w.Content.Actors[a.Def.DeathSpawnDef]
		if def == nil {
			continue // content.DB() validates; a foreign DB just no-ops
		}
		for i := 0; i < a.Def.DeathSpawnCount; i++ {
			off := deathSpawnOffsets[i%len(deathSpawnOffsets)]
			w.QueueSpawn(core.PendingSpawn{
				Def: def, Pos: a.Pos.Add(off), Level: a.Level, Source: a.ID,
			})
		}
	}
}

// applyCommands is the validation gate: every command is checked against the
// actor's actual state. Invalid commands are dropped silently — the sim never
// trusts the sender.
func applyCommands(w *core.World, cmds []core.Command) {
	for _, c := range cmds {
		a := w.ActorByID(c.Actor)
		if a == nil || a.Dead {
			continue
		}
		// A stunned actor can't act — every command is dropped for the
		// lockout window (Upkeep already decremented StunTicks this tick).
		if a.Stunned() {
			continue
		}
		switch c.Kind {
		case core.CmdMove:
			if a.Action.Kind == core.ActionSkill {
				if a.Action.Phase != core.PhaseChannel {
					continue // committed: no canceling windup/recovery in v1
				}
				a.Action = core.Action{} // a channel breaks for its owner's next order
			}
			if w.Grid == nil {
				a.Action = core.Action{Kind: core.ActionMove, MoveTarget: c.Point}
				continue
			}
			// Repath throttle: AI re-issues its chase target every tick; as
			// long as the request stays within half a tile of the current
			// goal, the existing path is still the answer.
			if a.Action.Kind == core.ActionMove && len(a.Action.Path) > 0 &&
				space.Dist(a.Action.MoveTarget, c.Point) <= w.Grid.Tile/2 {
				continue
			}
			path := w.Grid.FindPath(a.Pos, c.Point)
			if len(path) == 0 {
				continue // nowhere legal to go
			}
			a.Action = core.Action{Kind: core.ActionMove, MoveTarget: c.Point, Path: path}

		case core.CmdStop:
			if a.Action.Kind == core.ActionMove ||
				(a.Action.Kind == core.ActionSkill && a.Action.Phase == core.PhaseChannel) {
				a.Action = core.Action{}
			}

		case core.CmdEquip, core.CmdPickup, core.CmdUnequip, core.CmdDropItem:
			if a.Action.Kind == core.ActionSkill {
				continue // no rummaging through the bag mid-swing
			}
			switch c.Kind {
			case core.CmdEquip:
				slot := core.EquipAuto
				if c.HasSlot {
					slot = c.Slot
				}
				items.Equip(w, a, c.TargetID, slot)
			case core.CmdPickup:
				items.Pickup(w, a, c.TargetID)
			case core.CmdUnequip:
				items.Unequip(w, a, c.TargetID)
			case core.CmdDropItem:
				items.DropItem(w, a, c.TargetID)
			}

		case core.CmdUseFlask:
			// Not an action — a sip mid-swing is fine (PoE1 muscle memory).
			i := int(c.TargetID)
			if i < 0 || i >= len(a.FlaskCharges) || a.FlaskCharges[i] < core.FlaskUseCost {
				continue
			}
			buff := w.Content.Buffs[a.Def.Flasks[i]]
			if buff == nil {
				continue
			}
			a.FlaskCharges[i] -= core.FlaskUseCost
			combat.ApplyBuff(w, a, buff, a.ID)

		case core.CmdApplyOrb:
			if a.Action.Kind == core.ActionSkill {
				continue // no crafting mid-swing, same as the other item verbs
			}
			items.ApplyOrb(w, a, c.Orb, c.TargetID)

		case core.CmdCutSkill, core.CmdLevelGem, core.CmdCutSupport, core.CmdAddSocket:
			if a.Action.Kind == core.ActionSkill {
				continue // gem work is bench work, not battle work
			}
			switch c.Kind {
			case core.CmdCutSkill:
				items.CutSkill(w, a, c.TargetID, c.Choice, c.Replace, c.GemIndex)
			case core.CmdLevelGem:
				items.LevelGem(w, a, c.TargetID, c.GemIndex)
			case core.CmdCutSupport:
				items.CutSupport(w, a, c.TargetID, c.Choice, c.GemIndex, c.Socket)
			case core.CmdAddSocket:
				items.AddSocket(w, a, c.GemIndex)
			}

		case core.CmdChoosePassive:
			// Not an action — legal even mid-swing. Level-gated, one pick
			// per milestone, permanent.
			def := w.Content.Passive(c.Passive)
			if def == nil || a.Level < def.Milestone || a.HasMilestone(def.Milestone) {
				continue
			}
			a.TakePassive(def)
			w.Emit(core.Event{Kind: core.EvPassive, Actor: a.ID, Note: def.ID})

		case core.CmdUseSkill:
			if a.Action.Kind == core.ActionSkill {
				if a.Action.Phase != core.PhaseChannel {
					continue
				}
				a.Action = core.Action{} // a channel yields to the next cast
			}
			sk := w.Content.Skills[c.Skill]
			if sk == nil {
				continue
			}
			if sk.CooldownTicks > 0 && a.OnCooldown(sk.ID) {
				continue
			}
			// Cut gems first (the player path — gem level and supports come
			// along); Def.Skills is the monster path.
			var ctx core.GemCtx
			cost := sk.ManaCost
			if gem := a.GemForSkill(c.Skill); gem != nil {
				ctx = gem.Ctx()
				cost = gem.ManaCost()
			} else if !actorKnows(a, c.Skill) {
				continue
			}
			if a.Mana < cost {
				continue
			}
			speed := speedWithSupports(a, sk, ctx)
			a.Mana -= cost
			a.StartCooldown(sk.ID, sk.CooldownTicks)
			if sk.Kind == core.SkillStaged {
				skills.BeginStaged(w, a, sk, ctx, c.Point, c.TargetID, speed)
				continue
			}
			// Channelled skills bind their repeat interval where recovery
			// would live — both are use-time speed-bound tails.
			tail := sk.RecoveryTicks
			if sk.ChannelTicks > 0 {
				tail = sk.ChannelTicks
			}
			a.Action = core.Action{
				Kind:          core.ActionSkill,
				Skill:         sk,
				AimPoint:      c.Point,
				TargetID:      c.TargetID,
				Phase:         core.PhaseWindup,
				TicksLeft:     skills.ScaleTicks(sk.WindupTicks, speed),
				RecoveryTicks: skills.ScaleTicks(tail, speed),
				Gem:           ctx,
			}
		}
	}
}

func actorKnows(a *core.Actor, skillID string) bool {
	for _, id := range a.Def.Skills {
		if id == skillID {
			return true
		}
	}
	return false
}

// speedWithSupports evaluates the skill's speed stat with the cast's
// support modifiers folded in — the same algebra as Sheet.Eval, so a
// gem-less cast computes exactly what Eval would.
func speedWithSupports(a *core.Actor, sk *core.SkillDef, ctx core.GemCtx) fm.Fixed {
	p := a.Sheet.Layers(sk.SpeedStat, sk.Tags)
	p = ctx.FoldSupportMods(p, sk.SpeedStat, sk.Tags)
	if p.HasOverride {
		return p.Override
	}
	return fm.Mul(a.Sheet.Base(sk.SpeedStat)+p.Flat, p.Multiplier())
}

