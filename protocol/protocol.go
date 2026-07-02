// Package protocol defines the wire types crossing the sim boundary:
// commands in, snapshots out. It deliberately imports nothing from sim/ so
// future clients depend only on this package. Fixed-point values travel as
// raw milli-units (int64); clients divide by 1000 for display.
package protocol

// Version is the wire protocol version, carried in the welcome frame. Bump
// it on any change a deployed client could misread — renamed/removed JSON
// fields (omitempty makes those fail silently) or any binary frame layout
// change. Clients hard-fail on mismatch instead of limping.
const Version = 15 // v15: currency — apply_orb, per-actor orb wallet group (v14: flasks)

// Command is the wire form of player intent. Kind is one of "move",
// "use_skill", "stop", the item verbs "pickup", "equip", "unequip",
// "drop_item" (Target = the drop/item entity ID), or a server-level verb
// the sim never sees: "ack" (Tick = the latest view tick received, consumed
// by the delta encoder), "descend" (use the floor's stairs), "plant_portal"
// (move the run's portal to where you stand).
type Command struct {
	Tick   uint64 `json:"tick,omitempty"` // used by script files; live play is "now"
	Actor  uint64 `json:"actor"`
	Kind   string `json:"kind"`
	X      int64  `json:"x,omitempty"` // milli-units
	Y      int64  `json:"y,omitempty"`
	Skill  string `json:"skill,omitempty"`
	Target uint64 `json:"target,omitempty"`
	// Slot names the concrete equipment slot for "equip" ("weapon",
	// "ring2", ...); empty lets the sim pick by family preference.
	Slot string `json:"slot,omitempty"`
	// Gen tags an ack with the welcome generation it belongs to. A floor
	// swap re-welcomes the client and bumps the generation; acks from the
	// old world are dropped instead of poisoning the new delta encoder.
	Gen int `json:"gen,omitempty"`
	// Passive names the PassiveDef for "choose_passive".
	Passive string `json:"passive,omitempty"`
	// Orb names the currency kind for "apply_orb".
	Orb string `json:"orb,omitempty"`
}

type Vec struct {
	X int64 `json:"x"`
	Y int64 `json:"y"`
}

type ActorSnap struct {
	ID      uint64 `json:"id"`
	Def     string `json:"def"`
	Team    uint8  `json:"team"`
	Pos     Vec    `json:"pos"`
	Radius  int64  `json:"radius"`
	Life    int64  `json:"life"`
	MaxLife int64  `json:"max_life"`
	Mana    int64  `json:"mana"`
	MaxMana int64  `json:"max_mana"`
	ES      int64  `json:"es,omitempty"`
	Action  string `json:"action"`
	// Ail is the active-status bitmask: 1 ignited, 2 chilled, 4 shocked,
	// 8 buffed.
	Ail uint8 `json:"ail,omitempty"`
	// InvSize is the bag capacity (0 = carries nothing). Static per def;
	// travels in the identity field group on the binary wire.
	InvSize int `json:"inv_size,omitempty"`
	// Rarity ("" = normal) and Mods (modifier display names) mark magic
	// and rare monsters. Static per actor life; identity field group.
	Rarity string   `json:"rarity,omitempty"`
	Mods   []string `json:"mods,omitempty"`
	// Passives: taken milestone-passive IDs, in pick order. Own binary
	// field group — it changes on a pick, unlike the identity fields.
	Passives []string `json:"passives,omitempty"`
	// Flasks: charges per flask slot (order = the def's flask order).
	Flasks []int64 `json:"flasks,omitempty"`
	// Orbs: crafting-currency counts, OrbKind order (transmute/alch/chaos).
	Orbs []int64 `json:"orbs,omitempty"`
	// Progression: Level for everyone (nameplates someday), XP/XPNext as
	// progress into the current level (the HUD bar divides them). XPNext 0
	// means no further progression (max level).
	Level     int            `json:"level,omitempty"`
	XP        int64          `json:"xp,omitempty"`
	XPNext    int64          `json:"xp_next,omitempty"`
	Equipment []EquippedSnap `json:"equipment,omitempty"`
	Inventory []ItemSnap     `json:"inventory,omitempty"`
}

// Ailment bits for ActorSnap.Ail.
const (
	AilIgnited uint8 = 1 << iota
	AilChilled
	AilShocked
	AilBuffed // any content-defined buff is active
)

type EquippedSnap struct {
	Slot string   `json:"slot"`
	Item ItemSnap `json:"item"`
}

type ProjectileSnap struct {
	ID     uint64 `json:"id"`
	Skill  string `json:"skill"`
	Pos    Vec    `json:"pos"`
	Radius int64  `json:"radius"`
}

type AffixSnap struct {
	ID    string `json:"id"`
	Value int64  `json:"value"`
}

type ItemSnap struct {
	ID     uint64 `json:"id"` // stable item identity; the target for equip/unequip/drop_item
	Base   string `json:"base"`
	Rarity string `json:"rarity"`
	// Implicit is the base type's inherent modifier (rolled per item),
	// rendered above the affix block; nil when the base has none.
	Implicit *AffixSnap  `json:"implicit,omitempty"`
	Affixes  []AffixSnap `json:"affixes,omitempty"`
}

type DropSnap struct {
	ID   uint64   `json:"id"`
	Pos  Vec      `json:"pos"`
	Item ItemSnap `json:"item"`
}

type EventSnap struct {
	Kind   string `json:"kind"`
	Actor  uint64 `json:"actor,omitempty"`
	Other  uint64 `json:"other,omitempty"`
	Amount int64  `json:"amount,omitempty"`
	Note   string `json:"note,omitempty"`
	Crit   bool   `json:"crit,omitempty"`
}

// Snapshot is one client's view of one tick — with interest management,
// different clients legitimately see different state, and the omniscient
// debug wires are just the radius-unlimited case. Events may span several
// ticks when the send rate is below the tick rate.
type Snapshot struct {
	Tick        uint64           `json:"tick"`
	Actors      []ActorSnap      `json:"actors"`
	Projectiles []ProjectileSnap `json:"projectiles,omitempty"`
	Drops       []DropSnap       `json:"drops,omitempty"`
	Events      []EventSnap      `json:"events,omitempty"`
}

// MapSnap is the terrain a client renders: a tile grid as one string row
// per tile row, '#' solid / '.' floor. Terrain is immutable per instance,
// so it rides the welcome frame once instead of every view.
type MapSnap struct {
	Width  int      `json:"w"`
	Height int      `json:"h"`
	Tile   int64    `json:"tile"` // milli-units per tile edge
	Rows   []string `json:"rows"`
}

// RunSnap is the descent-run state a client shows: current floor, portal
// uses left, run number, best floor reached this process, and the portal's
// position when it stands on the current floor. It rides every welcome and
// its own "run" frames when run state changes without a floor swap.
type RunSnap struct {
	Floor   int  `json:"floor"`
	Portals int  `json:"portals"`
	Run     int  `json:"run"`
	Best    int  `json:"best"`
	Portal  *Vec `json:"portal,omitempty"`
}

// PassiveSnap is one milestone-passive choice as the client sees it: the
// static content the chooser UI renders. Rides the welcome once.
type PassiveSnap struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Milestone int    `json:"milestone"`
}

// ServerMsg is one server→client JSON frame. "welcome" carries the protocol
// version, the welcome generation (bumped by every re-welcome; acks echo
// it), the client's assigned actor ID, the tick/send cadence (so clients
// can size interpolation buffers), the terrain map when the instance has
// one, and — on descent worlds — the stairs position and run state. Any
// welcome fully resets the client: a new one means a new world (floor swap)
// on the same socket. "snapshot" carries one view in
// full JSON (the TCP/nc wire and the ?format=json debug mode — the binary
// WS wire sends view frames instead, see binary.go); "pause" announces the
// instance freezing or resuming (sent on transitions, and once to clients
// that join while paused); "run" announces run-state changes that don't
// come with a new world (portal planted).
type ServerMsg struct {
	Type      string    `json:"type"` // "welcome" | "snapshot" | "pause" | "run"
	V         int       `json:"v,omitempty"`
	Gen       int       `json:"gen,omitempty"`
	Actor     uint64    `json:"actor,omitempty"`
	TickHz    int       `json:"tick_hz,omitempty"`
	SendEvery int       `json:"send_every,omitempty"`
	Map       *MapSnap  `json:"map,omitempty"`
	Stairs    *Vec      `json:"stairs,omitempty"`
	Run       *RunSnap  `json:"run,omitempty"`
	Snapshot  *Snapshot `json:"snapshot,omitempty"`
	Paused    *bool     `json:"paused,omitempty"`
	// Passives is the milestone-choice table (static content), welcome only.
	Passives []PassiveSnap `json:"passives,omitempty"`
}

// Script is the headless runner's input: a scenario plus scheduled commands.
// A non-nil Map generates terrain (consuming the world's map RNG stream)
// before any spawns; Scatter then places monsters on random walkable tiles.
type Script struct {
	Map      *MapSpec      `json:"map,omitempty"`
	Spawns   []ScriptSpawn `json:"spawns"`
	Scatter  []Scatter     `json:"scatter,omitempty"`
	Commands []Command     `json:"commands"`
}

// MapSpec asks the sim to generate a rooms-and-corridors map. Tile size and
// clearance are engine defaults; scenarios only choose the footprint.
type MapSpec struct {
	Width  int `json:"width"`  // tiles
	Height int `json:"height"` // tiles
	Rooms  int `json:"rooms"`
}

// Scatter places Count actors of Def on random walkable tiles, away from
// the player spawn. Requires a generated map.
type Scatter struct {
	Def   string `json:"def"`
	Count int    `json:"count"`
}

// ScriptSpawn places an actor at startup. Entity IDs are assigned in spawn
// order starting at 1, which is how Commands reference them.
type ScriptSpawn struct {
	Def string `json:"def"`
	X   int64  `json:"x"` // milli-units
	Y   int64  `json:"y"`
}
