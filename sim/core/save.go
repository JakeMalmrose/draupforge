// World save/restore — the persistence boundary RISKS.md #1 called for.
// Content is referenced by string ID; entity state is flat data; everything
// derived (the actor index, sheet memos, grid hash words, pathing scratch)
// is rebuilt at load. JSON because a save format this young is worth being
// able to read; struct marshaling (never maps) keeps the bytes deterministic.
//
// Save captures a tick boundary: BeginTick has not run, PendingHits is
// empty, events are drained. That is the only moment the world is whole —
// saving mid-tick is refused rather than half-captured.
package core

import (
	"encoding/json"
	"errors"
	"fmt"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
	"github.com/JakeMalmrose/draupforge/sim/stats"
)

// SaveVersion gates restores: a format change bumps it, and old files fail
// loudly instead of misloading. Saves are durable state — unlike replays,
// they must never depend on re-execution of the code that wrote them.
const SaveVersion = 11 // v11: unique items (v10: staged skill actions)

type saveFile struct {
	Version     int              `json:"version"`
	Tick        uint64           `json:"tick"`
	NextID      uint64           `json:"next_id"`
	RNG         [4][4]uint64     `json:"rng"` // combat, loot, ai, map
	Grid        *space.GridSave  `json:"grid,omitempty"`
	Actors      []actorSave      `json:"actors"`
	Projectiles []projectileSave `json:"projectiles"`
	Drops       []dropSave       `json:"drops"`
}

// modSave flattens stats.Modifier. Tags travel as tag indices — the
// width-independent form — so TagSet widenings never invalidate saves.
type modSave struct {
	Stat   uint8    `json:"stat"`
	Layer  uint8    `json:"layer"`
	Value  fm.Fixed `json:"value"`
	Tags   []uint8  `json:"tags,omitempty"`
	Source uint64   `json:"source"`
}

type affixSave struct {
	ID    string   `json:"id"`
	Value fm.Fixed `json:"value"`
}

type itemSave struct {
	ID       uint64      `json:"id"`
	Base     string      `json:"base,omitempty"` // empty for uncut gems
	Rarity   uint8       `json:"rarity"`
	Implicit fm.Fixed    `json:"implicit,omitempty"`
	Affixes  []affixSave `json:"affixes,omitempty"`
	Gem      *uncutSave  `json:"gem,omitempty"`
	Unique   string      `json:"unique,omitempty"` // UniqueDef ID
}

// uncutSave is an uncut gem item's payload: kind, found-at level, and the
// pre-rolled draft.
type uncutSave struct {
	Support bool     `json:"support,omitempty"`
	Level   int      `json:"level,omitempty"`
	Choices []string `json:"choices"`
}

// gemSave is one cut skill gem; Supports is socket-addressed, "" = empty.
type gemSave struct {
	Skill    string   `json:"skill"`
	Level    int      `json:"level"`
	Sockets  int      `json:"sockets"`
	Supports []string `json:"supports"`
}

type actionSave struct {
	Kind          uint8        `json:"kind"`
	MoveTarget    space.Vec2   `json:"move_target"`
	Path          []space.Vec2 `json:"path,omitempty"`
	PathStep      int          `json:"path_step,omitempty"`
	Skill         string       `json:"skill,omitempty"`
	AimPoint      space.Vec2   `json:"aim_point"`
	TargetID      uint64       `json:"target_id,omitempty"`
	Phase         uint8        `json:"phase"`
	TicksLeft     uint32       `json:"ticks_left"`
	RecoveryTicks uint32       `json:"recovery_ticks"`
	// Staged skill state (empty for legacy windup/recovery actions).
	Stage      int        `json:"stage,omitempty"`
	StageTicks []uint32   `json:"stage_ticks,omitempty"`
	StageAim   space.Vec2 `json:"stage_aim"`
	// The cast's baked gem context (players; zero for monsters).
	GemLevel    int      `json:"gem_level,omitempty"`
	GemSupports []string `json:"gem_supports,omitempty"`
}

type dotSave struct {
	Type      uint8    `json:"type"`
	PerTick   fm.Fixed `json:"per_tick"`
	TicksLeft uint32   `json:"ticks_left"`
	Source    uint64   `json:"source"`
}

type statusSave struct {
	Kind      uint8    `json:"kind"`
	Buff      string   `json:"buff,omitempty"` // BuffDef ID for StatusBuff entries
	Magnitude fm.Fixed `json:"magnitude"`
	TicksLeft uint32   `json:"ticks_left"`
	Source    uint64   `json:"source"`
}

type actorSave struct {
	ID        uint64       `json:"id"`
	Def       string       `json:"def"`
	Team      uint8        `json:"team"`
	Pos       space.Vec2   `json:"pos"`
	Home      space.Vec2   `json:"home"`
	Life      fm.Fixed     `json:"life"`
	Mana      fm.Fixed     `json:"mana"`
	ES        fm.Fixed     `json:"es"`
	Level     int          `json:"level"`
	XP        int64        `json:"xp,omitempty"`
	Rarity    uint8        `json:"rarity,omitempty"`
	MonMods   []string     `json:"mon_mods,omitempty"`  // MonsterModDef IDs
	Passives  []string     `json:"passives,omitempty"` // PassiveDef IDs
	Flasks    []int32      `json:"flasks,omitempty"`   // charges per flask slot
	Orbs      []int32      `json:"orbs,omitempty"`     // wallet, OrbKind order
	Gems      []gemSave    `json:"gems,omitempty"`
	Base      []fm.Fixed   `json:"base"` // sheet base values, StatID order
	Mods      []modSave    `json:"mods,omitempty"`
	Action    actionSave   `json:"action"`
	DoTs      []dotSave    `json:"dots,omitempty"`
	Statuses  []statusSave `json:"statuses,omitempty"`
	Equipment []*itemSave  `json:"equipment"` // EquipSlotCount entries, null = empty
	Inventory []itemSave   `json:"inventory,omitempty"`
}

type projectileSave struct {
	ID        uint64     `json:"id"`
	Source    uint64     `json:"source"`
	Team      uint8      `json:"team"`
	Skill     string     `json:"skill"`
	Pos       space.Vec2 `json:"pos"`
	Vel       space.Vec2 `json:"vel"`
	TicksLeft uint32     `json:"ticks_left"`
	// Baked gem context + chain state (players; zero for monsters).
	GemLevel    int      `json:"gem_level,omitempty"`
	GemSupports []string `json:"gem_supports,omitempty"`
	ChainsLeft  int      `json:"chains_left,omitempty"`
	HitIDs      []uint64 `json:"hit_ids,omitempty"`
}

type dropSave struct {
	ID   uint64     `json:"id"`
	Pos  space.Vec2 `json:"pos"`
	Item itemSave   `json:"item"`
}

// Save serializes the world at a tick boundary.
func (w *World) Save() ([]byte, error) {
	// w.events may still hold last tick's drained events (BeginTick recycles
	// the slice) — only unresolved hits, buffs, or spawns mean we're
	// genuinely mid-tick.
	if len(w.PendingHits) > 0 || len(w.PendingBuffs) > 0 || len(w.PendingSpawns) > 0 {
		return nil, errors.New("core: world can only be saved at a tick boundary")
	}
	sf := saveFile{
		Version: SaveVersion,
		Tick:    w.Tick,
		NextID:  uint64(w.nextID),
		RNG: [4][4]uint64{
			w.RNGCombat.State(), w.RNGLoot.State(), w.RNGAI.State(), w.RNGMap.State(),
		},
	}
	if w.Grid != nil {
		gs := w.Grid.Encode()
		sf.Grid = &gs
	}
	for _, a := range w.Actors {
		if a.Dead {
			continue // tombstones don't survive a boundary; belt and braces
		}
		sf.Actors = append(sf.Actors, encodeActor(a))
	}
	for _, p := range w.Projectiles {
		if p.Dead {
			continue
		}
		ps := projectileSave{
			ID: uint64(p.ID), Source: uint64(p.Source), Team: uint8(p.Team),
			Skill: p.Skill.ID, Pos: p.Pos, Vel: p.Vel, TicksLeft: p.TicksLeft,
			GemLevel: p.Gem.Level, GemSupports: supportIDs(p.Gem.Supports),
			ChainsLeft: p.ChainsLeft,
		}
		for _, id := range p.HitIDs {
			ps.HitIDs = append(ps.HitIDs, uint64(id))
		}
		sf.Projectiles = append(sf.Projectiles, ps)
	}
	for _, d := range w.Drops {
		if d.Taken {
			continue
		}
		sf.Drops = append(sf.Drops, dropSave{
			ID: uint64(d.ID), Pos: d.Pos, Item: encodeItem(d.Item),
		})
	}
	return json.Marshal(sf)
}

func encodeActor(a *Actor) actorSave {
	as := actorSave{
		ID: uint64(a.ID), Def: a.Def.ID, Team: uint8(a.Team), Pos: a.Pos, Home: a.Home,
		Life: a.Life, Mana: a.Mana, ES: a.ES,
		Level: a.Level, XP: a.XP, Rarity: uint8(a.Rarity),
		Base: make([]fm.Fixed, stats.StatCount),
		Action: actionSave{
			Kind:       uint8(a.Action.Kind),
			MoveTarget: a.Action.MoveTarget,
			Path:       a.Action.Path,
			PathStep:   a.Action.PathStep,
			AimPoint:   a.Action.AimPoint,
			TargetID:   uint64(a.Action.TargetID),
			Phase:      uint8(a.Action.Phase),
			TicksLeft:  a.Action.TicksLeft,

			RecoveryTicks: a.Action.RecoveryTicks,
			Stage:         a.Action.Stage,
			StageTicks:    a.Action.StageTicks,
			StageAim:      a.Action.StageAim,
		},
		Equipment: make([]*itemSave, EquipSlotCount),
	}
	if a.Action.Skill != nil {
		as.Action.Skill = a.Action.Skill.ID
	}
	as.Action.GemLevel = a.Action.Gem.Level
	as.Action.GemSupports = supportIDs(a.Action.Gem.Supports)
	for i := range a.Gems {
		g := &a.Gems[i]
		gs := gemSave{Skill: g.Skill.ID, Level: g.Level, Sockets: g.Sockets}
		for _, sup := range g.Supports {
			if sup == nil {
				gs.Supports = append(gs.Supports, "")
			} else {
				gs.Supports = append(gs.Supports, sup.ID)
			}
		}
		as.Gems = append(as.Gems, gs)
	}
	for _, md := range a.Mods {
		as.MonMods = append(as.MonMods, md.ID)
	}
	for _, ps := range a.Passives {
		as.Passives = append(as.Passives, ps.ID)
	}
	as.Flasks = a.FlaskCharges
	for _, n := range a.Orbs {
		if n != 0 {
			as.Orbs = a.Orbs[:]
			break
		}
	}
	for st := stats.StatID(0); st < stats.StatCount; st++ {
		as.Base[st] = a.Sheet.Base(st)
	}
	for _, m := range a.Sheet.Mods() {
		ms := modSave{
			Stat: uint8(m.Stat), Layer: uint8(m.Layer),
			Value: m.Value, Source: m.Source,
		}
		for _, t := range m.Tags.Tags() {
			ms.Tags = append(ms.Tags, uint8(t))
		}
		as.Mods = append(as.Mods, ms)
	}
	for _, d := range a.DoTs {
		as.DoTs = append(as.DoTs, dotSave{
			Type: uint8(d.Type), PerTick: d.PerTick, TicksLeft: d.TicksLeft, Source: uint64(d.Source),
		})
	}
	for _, s := range a.Statuses {
		ss := statusSave{
			Kind: uint8(s.Kind), Magnitude: s.Magnitude, TicksLeft: s.TicksLeft, Source: uint64(s.Source),
		}
		if s.Buff != nil {
			ss.Buff = s.Buff.ID
		}
		as.Statuses = append(as.Statuses, ss)
	}
	for slot, item := range a.Equipment {
		if item != nil {
			is := encodeItem(*item)
			as.Equipment[slot] = &is
		}
	}
	for _, item := range a.Inventory {
		as.Inventory = append(as.Inventory, encodeItem(item))
	}
	return as
}

func encodeItem(item Item) itemSave {
	if item.Gem != nil {
		return itemSave{ID: uint64(item.ID), Gem: &uncutSave{
			Support: item.Gem.Support, Level: item.Gem.Level, Choices: item.Gem.Choices,
		}}
	}
	is := itemSave{ID: uint64(item.ID), Base: item.Base.ID, Rarity: uint8(item.Rarity), Implicit: item.Implicit}
	for _, af := range item.Affixes {
		is.Affixes = append(is.Affixes, affixSave{ID: af.Def.ID, Value: af.Value})
	}
	if item.Unique != nil {
		is.Unique = item.Unique.ID
	}
	return is
}

// supportIDs flattens a compacted support list (a GemCtx's) to IDs.
func supportIDs(supports []*SupportDef) []string {
	var out []string
	for _, s := range supports {
		out = append(out, s.ID)
	}
	return out
}

// LoadWorld rebuilds a world from Save's output against a content registry.
// Content references resolve by string ID; an ID the registry no longer
// knows fails the whole load — a half-restored world is worse than none.
func LoadWorld(db *ContentDB, data []byte) (*World, error) {
	var sf saveFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("core: parsing save: %w", err)
	}
	if sf.Version != SaveVersion {
		return nil, fmt.Errorf("core: save version %d, this build reads %d", sf.Version, SaveVersion)
	}
	w := &World{
		Tick:      sf.Tick,
		Content:   db,
		RNGCombat: RestoreRNG(sf.RNG[0]),
		RNGLoot:   RestoreRNG(sf.RNG[1]),
		RNGAI:     RestoreRNG(sf.RNG[2]),
		RNGMap:    RestoreRNG(sf.RNG[3]),
		nextID:    EntityID(sf.NextID),
		idx:       make(map[EntityID]*Actor),
	}
	if sf.Grid != nil {
		g, err := space.DecodeGrid(*sf.Grid)
		if err != nil {
			return nil, err
		}
		w.Grid = g
	}
	affixes := make(map[string]*AffixDef, len(db.Affixes))
	for _, af := range db.Affixes {
		affixes[af.ID] = af
	}
	for _, as := range sf.Actors {
		a, err := decodeActor(db, affixes, as)
		if err != nil {
			return nil, err
		}
		w.Actors = append(w.Actors, a)
		w.idx[a.ID] = a
	}
	for _, ps := range sf.Projectiles {
		sk := db.Skills[ps.Skill]
		if sk == nil {
			return nil, fmt.Errorf("core: save references unknown skill %q", ps.Skill)
		}
		ctx, err := decodeGemCtx(db, ps.GemLevel, ps.GemSupports)
		if err != nil {
			return nil, err
		}
		p := &Projectile{
			ID: EntityID(ps.ID), Source: EntityID(ps.Source), Team: Team(ps.Team),
			Skill: sk, Pos: ps.Pos, Vel: ps.Vel, TicksLeft: ps.TicksLeft,
			Gem: ctx, ChainsLeft: ps.ChainsLeft,
		}
		for _, id := range ps.HitIDs {
			p.HitIDs = append(p.HitIDs, EntityID(id))
		}
		w.Projectiles = append(w.Projectiles, p)
	}
	for _, ds := range sf.Drops {
		item, err := decodeItem(db, affixes, ds.Item)
		if err != nil {
			return nil, err
		}
		w.Drops = append(w.Drops, &Drop{ID: EntityID(ds.ID), Pos: ds.Pos, Item: item})
	}
	return w, nil
}

func decodeActor(db *ContentDB, affixes map[string]*AffixDef, as actorSave) (*Actor, error) {
	def := db.Actors[as.Def]
	if def == nil {
		return nil, fmt.Errorf("core: save references unknown actor def %q", as.Def)
	}
	if len(as.Base) > int(stats.StatCount) {
		return nil, fmt.Errorf("core: actor %d has %d base stats, this build knows %d", as.ID, len(as.Base), stats.StatCount)
	}
	var base [stats.StatCount]fm.Fixed
	copy(base[:], as.Base)
	mods := make([]stats.Modifier, 0, len(as.Mods))
	for _, ms := range as.Mods {
		var tags stats.TagSet
		for _, t := range ms.Tags {
			if stats.Tag(t) >= stats.TagCount {
				return nil, fmt.Errorf("core: actor %d modifier has unknown tag %d", as.ID, t)
			}
			tags = tags.With(stats.Tag(t))
		}
		mods = append(mods, stats.Modifier{
			Stat: stats.StatID(ms.Stat), Layer: stats.Layer(ms.Layer),
			Value: ms.Value, Tags: tags, Source: ms.Source,
		})
	}
	// Level mods are already in the saved modifier list — assign the field
	// directly instead of SetLevel, which would rebuild them from the
	// (possibly since-edited) def.
	level := as.Level
	if level < 1 {
		level = 1
	}
	a := &Actor{
		ID: EntityID(as.ID), Def: def, Team: Team(as.Team), Pos: as.Pos, Home: as.Home,
		Sheet: stats.RestoreSheet(base, mods),
		Life:  as.Life, Mana: as.Mana, ES: as.ES,
		Level: level, XP: as.XP,
		Rarity: Rarity(as.Rarity),
		Action: Action{
			Kind:          ActionKind(as.Action.Kind),
			MoveTarget:    as.Action.MoveTarget,
			Path:          as.Action.Path,
			PathStep:      as.Action.PathStep,
			AimPoint:      as.Action.AimPoint,
			TargetID:      EntityID(as.Action.TargetID),
			Phase:         ActionPhase(as.Action.Phase),
			TicksLeft:     as.Action.TicksLeft,
			RecoveryTicks: as.Action.RecoveryTicks,
			Stage:         as.Action.Stage,
			StageTicks:    as.Action.StageTicks,
			StageAim:      as.Action.StageAim,
		},
	}
	if as.Action.Skill != "" {
		sk := db.Skills[as.Action.Skill]
		if sk == nil {
			return nil, fmt.Errorf("core: save references unknown skill %q", as.Action.Skill)
		}
		a.Action.Skill = sk
		// A staged action must still index a stage the (possibly edited)
		// def actually has — a mismatch would panic ticks later.
		if sk.Kind == SkillStaged && a.Action.Kind == ActionSkill &&
			(a.Action.Stage < 0 || a.Action.Stage >= len(sk.Stages) ||
				len(a.Action.StageTicks) != len(sk.Stages)) {
			return nil, fmt.Errorf("core: actor %d staged action doesn't fit skill %q", as.ID, sk.ID)
		}
	}
	ctx, err := decodeGemCtx(db, as.Action.GemLevel, as.Action.GemSupports)
	if err != nil {
		return nil, err
	}
	a.Action.Gem = ctx
	for _, gs := range as.Gems {
		sk := db.Skills[gs.Skill]
		if sk == nil {
			return nil, fmt.Errorf("core: save references unknown skill %q", gs.Skill)
		}
		g := Gem{Skill: sk, Level: gs.Level, Sockets: gs.Sockets}
		for _, id := range gs.Supports {
			if id == "" {
				g.Supports = append(g.Supports, nil)
				continue
			}
			sup := db.Support(id)
			if sup == nil {
				return nil, fmt.Errorf("core: save references unknown support %q", id)
			}
			g.Supports = append(g.Supports, sup)
		}
		a.Gems = append(a.Gems, g)
	}
	// The mods'/passives' stat packages are already in the saved modifier
	// list (under their shared sources); the defs are resolved only so
	// snapshots can name them and milestone checks can see them.
	for _, id := range as.MonMods {
		md := db.MonsterMod(id)
		if md == nil {
			return nil, fmt.Errorf("core: save references unknown monster mod %q", id)
		}
		a.Mods = append(a.Mods, md)
	}
	for _, id := range as.Passives {
		pd := db.Passive(id)
		if pd == nil {
			return nil, fmt.Errorf("core: save references unknown passive %q", id)
		}
		a.Passives = append(a.Passives, pd)
	}
	a.FlaskCharges = as.Flasks
	if len(as.Orbs) > int(OrbCount) {
		return nil, fmt.Errorf("core: actor %d has %d orb kinds, this build knows %d", as.ID, len(as.Orbs), OrbCount)
	}
	copy(a.Orbs[:], as.Orbs)
	for _, ds := range as.DoTs {
		a.DoTs = append(a.DoTs, DoT{
			Type: DamageType(ds.Type), PerTick: ds.PerTick, TicksLeft: ds.TicksLeft, Source: EntityID(ds.Source),
		})
	}
	for _, ss := range as.Statuses {
		st := Status{
			Kind: StatusKind(ss.Kind), Magnitude: ss.Magnitude, TicksLeft: ss.TicksLeft, Source: EntityID(ss.Source),
		}
		if st.Kind == StatusBuff {
			st.Buff = db.Buffs[ss.Buff]
			if st.Buff == nil {
				return nil, fmt.Errorf("core: save references unknown buff %q", ss.Buff)
			}
		}
		a.Statuses = append(a.Statuses, st)
	}
	if len(as.Equipment) > int(EquipSlotCount) {
		return nil, fmt.Errorf("core: actor %d has %d equipment slots, this build knows %d", as.ID, len(as.Equipment), EquipSlotCount)
	}
	for slot, is := range as.Equipment {
		if is == nil {
			continue
		}
		item, err := decodeItem(db, affixes, *is)
		if err != nil {
			return nil, err
		}
		a.Equipment[slot] = &item
	}
	for _, is := range as.Inventory {
		item, err := decodeItem(db, affixes, is)
		if err != nil {
			return nil, err
		}
		a.Inventory = append(a.Inventory, item)
	}
	return a, nil
}

// decodeGemCtx resolves a saved cast context's support IDs.
func decodeGemCtx(db *ContentDB, level int, supports []string) (GemCtx, error) {
	ctx := GemCtx{Level: level}
	for _, id := range supports {
		sup := db.Support(id)
		if sup == nil {
			return GemCtx{}, fmt.Errorf("core: save references unknown support %q", id)
		}
		ctx.Supports = append(ctx.Supports, sup)
	}
	return ctx, nil
}

func decodeItem(db *ContentDB, affixes map[string]*AffixDef, is itemSave) (Item, error) {
	if is.Gem != nil {
		item := Item{ID: EntityID(is.ID), Gem: &UncutGem{
			Support: is.Gem.Support, Level: is.Gem.Level, Choices: is.Gem.Choices,
		}}
		for _, c := range is.Gem.Choices {
			if is.Gem.Support {
				if db.Support(c) == nil {
					return Item{}, fmt.Errorf("core: save references unknown support %q", c)
				}
			} else if db.Skills[c] == nil {
				return Item{}, fmt.Errorf("core: save references unknown skill %q", c)
			}
		}
		return item, nil
	}
	base := db.BaseItems[is.Base]
	if base == nil {
		return Item{}, fmt.Errorf("core: save references unknown base item %q", is.Base)
	}
	item := Item{ID: EntityID(is.ID), Base: base, Rarity: Rarity(is.Rarity), Implicit: is.Implicit}
	for _, af := range is.Affixes {
		def := affixes[af.ID]
		if def == nil {
			return Item{}, fmt.Errorf("core: save references unknown affix %q", af.ID)
		}
		item.Affixes = append(item.Affixes, RolledAffix{Def: def, Value: af.Value})
	}
	if is.Unique != "" {
		item.Unique = db.Unique(is.Unique)
		if item.Unique == nil {
			return Item{}, fmt.Errorf("core: save references unknown unique %q", is.Unique)
		}
	}
	return item, nil
}
