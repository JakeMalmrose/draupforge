// Package protocol defines the wire types crossing the sim boundary:
// commands in, snapshots out. It deliberately imports nothing from sim/ so
// future clients depend only on this package. Fixed-point values travel as
// raw milli-units (int64); clients divide by 1000 for display.
package protocol

// Version is the wire protocol version, carried in the welcome frame. Bump
// it on any change a deployed client could misread — renamed/removed JSON
// fields (omitempty makes those fail silently) or any binary frame layout
// change. Clients hard-fail on mismatch instead of limping.
const Version = 4 // v4: inventory capacity on actor snaps

// Command is the wire form of player intent. Kind is one of "move",
// "use_skill", "stop", the item verbs "pickup", "equip", "unequip",
// "drop_item" (Target = the drop/item entity ID), or "ack" (Tick = the
// latest view tick received; consumed by the server's delta encoder, never
// forwarded to the sim).
type Command struct {
	Tick   uint64 `json:"tick,omitempty"` // used by script files; live play is "now"
	Actor  uint64 `json:"actor"`
	Kind   string `json:"kind"`
	X      int64  `json:"x,omitempty"` // milli-units
	Y      int64  `json:"y,omitempty"`
	Skill  string `json:"skill,omitempty"`
	Target uint64 `json:"target,omitempty"`
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
	// Ail is the active-ailment bitmask: 1 ignited, 2 chilled, 4 shocked.
	Ail uint8 `json:"ail,omitempty"`
	// InvSize is the bag capacity (0 = carries nothing). Static per def;
	// travels in the identity field group on the binary wire.
	InvSize   int            `json:"inv_size,omitempty"`
	Equipment []EquippedSnap `json:"equipment,omitempty"`
	Inventory []ItemSnap     `json:"inventory,omitempty"`
}

// Ailment bits for ActorSnap.Ail.
const (
	AilIgnited uint8 = 1 << iota
	AilChilled
	AilShocked
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
	ID      uint64      `json:"id"` // stable item identity; the target for equip/unequip/drop_item
	Base    string      `json:"base"`
	Rarity  string      `json:"rarity"`
	Affixes []AffixSnap `json:"affixes,omitempty"`
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

// ServerMsg is one server→client JSON frame. "welcome" carries the protocol
// version, the client's assigned actor ID, and the tick/send cadence (so
// clients can size interpolation buffers); "snapshot" carries one view in
// full JSON (the TCP/nc wire and the ?format=json debug mode — the binary
// WS wire sends view frames instead, see binary.go); "pause" announces the
// instance freezing or resuming (sent on transitions, and once to clients
// that join while paused).
type ServerMsg struct {
	Type      string    `json:"type"` // "welcome" | "snapshot" | "pause"
	V         int       `json:"v,omitempty"`
	Actor     uint64    `json:"actor,omitempty"`
	TickHz    int       `json:"tick_hz,omitempty"`
	SendEvery int       `json:"send_every,omitempty"`
	Snapshot  *Snapshot `json:"snapshot,omitempty"`
	Paused    *bool     `json:"paused,omitempty"`
}

// Script is the headless runner's input: a scenario plus scheduled commands.
type Script struct {
	Spawns   []ScriptSpawn `json:"spawns"`
	Commands []Command     `json:"commands"`
}

// ScriptSpawn places an actor at startup. Entity IDs are assigned in spawn
// order starting at 1, which is how Commands reference them.
type ScriptSpawn struct {
	Def string `json:"def"`
	X   int64  `json:"x"` // milli-units
	Y   int64  `json:"y"`
}
