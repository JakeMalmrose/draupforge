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
const SaveVersion = 3 // v3: actor level + xp (v2: item implicit values)

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
	Base     string      `json:"base"`
	Rarity   uint8       `json:"rarity"`
	Implicit fm.Fixed    `json:"implicit,omitempty"`
	Affixes  []affixSave `json:"affixes,omitempty"`
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
	Life      fm.Fixed     `json:"life"`
	Mana      fm.Fixed     `json:"mana"`
	ES        fm.Fixed     `json:"es"`
	Level     int          `json:"level"`
	XP        int64        `json:"xp,omitempty"`
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
}

type dropSave struct {
	ID   uint64     `json:"id"`
	Pos  space.Vec2 `json:"pos"`
	Item itemSave   `json:"item"`
}

// Save serializes the world at a tick boundary.
func (w *World) Save() ([]byte, error) {
	// w.events may still hold last tick's drained events (BeginTick recycles
	// the slice) — only unresolved hits or buffs mean we're genuinely mid-tick.
	if len(w.PendingHits) > 0 || len(w.PendingBuffs) > 0 {
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
		sf.Projectiles = append(sf.Projectiles, projectileSave{
			ID: uint64(p.ID), Source: uint64(p.Source), Team: uint8(p.Team),
			Skill: p.Skill.ID, Pos: p.Pos, Vel: p.Vel, TicksLeft: p.TicksLeft,
		})
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
		ID: uint64(a.ID), Def: a.Def.ID, Team: uint8(a.Team), Pos: a.Pos,
		Life: a.Life, Mana: a.Mana, ES: a.ES,
		Level: a.Level, XP: a.XP,
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
		},
		Equipment: make([]*itemSave, EquipSlotCount),
	}
	if a.Action.Skill != nil {
		as.Action.Skill = a.Action.Skill.ID
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
	is := itemSave{ID: uint64(item.ID), Base: item.Base.ID, Rarity: uint8(item.Rarity), Implicit: item.Implicit}
	for _, af := range item.Affixes {
		is.Affixes = append(is.Affixes, affixSave{ID: af.Def.ID, Value: af.Value})
	}
	return is
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
		w.Projectiles = append(w.Projectiles, &Projectile{
			ID: EntityID(ps.ID), Source: EntityID(ps.Source), Team: Team(ps.Team),
			Skill: sk, Pos: ps.Pos, Vel: ps.Vel, TicksLeft: ps.TicksLeft,
		})
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
		ID: EntityID(as.ID), Def: def, Team: Team(as.Team), Pos: as.Pos,
		Sheet: stats.RestoreSheet(base, mods),
		Life:  as.Life, Mana: as.Mana, ES: as.ES,
		Level: level, XP: as.XP,
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
		},
	}
	if as.Action.Skill != "" {
		sk := db.Skills[as.Action.Skill]
		if sk == nil {
			return nil, fmt.Errorf("core: save references unknown skill %q", as.Action.Skill)
		}
		a.Action.Skill = sk
	}
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

func decodeItem(db *ContentDB, affixes map[string]*AffixDef, is itemSave) (Item, error) {
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
	return item, nil
}
