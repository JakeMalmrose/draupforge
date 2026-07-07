// Package protocol defines the wire types crossing the sim boundary:
// commands in, snapshots out. It deliberately imports nothing from sim/ so
// future clients depend only on this package. Fixed-point values travel as
// raw milli-units (int64); clients divide by 1000 for display.
package protocol

// Version is the wire protocol version, carried in the welcome frame. Bump
// it on any change a deployed client could misread — renamed/removed JSON
// fields (omitempty makes those fail silently) or any binary frame layout
// change. Clients hard-fail on mismatch instead of limping.
// v18 unifies two parallel branches that both claimed v16 (gems on main,
// identity on the multiplayer branch; parties took v17) — jumping past all
// of them so no deployed client can match a wrong meaning.
const Version = 24 // v24: gem cooldown ticks (v23: aura toggle state on gem snaps)

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
	// Name is the invitee's display name for the host-layer "invite" verb
	// (the social verbs "accept_invite", "decline_invite" and "leave_party"
	// carry nothing).
	Name string `json:"name,omitempty"`
	// Gem-verb addressing ("cut_skill", "level_gem", "cut_support",
	// "add_socket"): Choice indexes the uncut gem's draft, Gem the actor's
	// cut gems, Socket the gem's sockets; Replace arms cut_skill's
	// destroy-to-make-room path.
	Choice  int  `json:"choice,omitempty"`
	Gem     int  `json:"gem,omitempty"`
	Socket  int  `json:"socket,omitempty"`
	Replace bool `json:"replace,omitempty"`
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
	// 8 buffed, 16 bleeding, 32 poisoned, 64 cursed.
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
	// Orbs: crafting-currency counts, OrbKind order (transmutation,
	// alchemy, chaos, jeweller).
	Orbs []int64 `json:"orbs,omitempty"`
	// Gems: the actor's cut skill gems in bar order. Own binary field
	// group — it changes on cut/level/socket verbs.
	Gems []GemSnap `json:"gems,omitempty"`
	// Progression: Level for everyone (nameplates someday), XP/XPNext as
	// progress into the current level (the HUD bar divides them). XPNext 0
	// means no further progression (max level).
	Level     int            `json:"level,omitempty"`
	XP        int64          `json:"xp,omitempty"`
	XPNext    int64          `json:"xp_next,omitempty"`
	Equipment []EquippedSnap `json:"equipment,omitempty"`
	Inventory []ItemSnap     `json:"inventory,omitempty"`
	// Telegraph marks where this actor's pending skill effect will land —
	// the danger zone a client renders on the ground. Present only while a
	// telegraphed effect is winding up.
	Telegraph *TelegraphSnap `json:"telegraph,omitempty"`
}

// TelegraphSnap is one pending skill effect's danger zone: center, radius,
// and the countdown (Left of Total ticks; Total 0 when the wind-up length
// isn't on the wire — clients infer it from the first Left they see).
type TelegraphSnap struct {
	X      int64  `json:"x"`
	Y      int64  `json:"y"`
	Radius int64  `json:"radius"`
	Left   uint32 `json:"left"`
	Total  uint32 `json:"total,omitempty"`
}

// Ailment bits for ActorSnap.Ail.
const (
	AilIgnited uint8 = 1 << iota
	AilChilled
	AilShocked
	AilBuffed   // any content-defined buff is active
	AilBleeding // a physical DoT is ticking
	AilPoisoned // a chaos DoT is ticking
	AilCursed   // a curse (hex BuffDef) is on this actor
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
	Base   string `json:"base,omitempty"` // empty for uncut gems
	Rarity string `json:"rarity"`
	// Implicit is the base type's inherent modifier (rolled per item),
	// rendered above the affix block; nil when the base has none.
	Implicit *AffixSnap  `json:"implicit,omitempty"`
	// ItemLevel is the level the item rolled at (gates its affix tiers);
	// shown on the tooltip. 0 = gems and pre-item-level items.
	ItemLevel int         `json:"ilvl,omitempty"`
	Affixes   []AffixSnap `json:"affixes,omitempty"`
	// Gem marks an uncut gem item: its kind, found-at level, and the
	// pre-rolled draft the cutting dialog offers.
	Gem *GemItemSnap `json:"gem,omitempty"`
	// Unique marks a unique item: display name, flavor line, and the
	// authored mod lines the tooltip shows verbatim (rarity is "unique").
	Unique *UniqueItemSnap `json:"unique,omitempty"`
}

// UniqueItemSnap is the unique part of an ItemSnap.
type UniqueItemSnap struct {
	Name string   `json:"name"`
	Desc string   `json:"desc,omitempty"`
	Mods []string `json:"mods,omitempty"`
}

// GemItemSnap is the gem part of an uncut gem item.
type GemItemSnap struct {
	Support bool     `json:"support,omitempty"`
	Level   int      `json:"level,omitempty"`
	Choices []string `json:"choices"`
}

// GemSnap is one cut skill gem as the client sees it: the skill, its level,
// socket-addressed supports ("" = empty socket), and the effective mana
// cost (milli) so the HUD never re-derives cost math.
type GemSnap struct {
	Skill    string   `json:"skill"`
	Level    int      `json:"level"`
	Sockets  int      `json:"sockets"`
	Supports []string `json:"supports"`
	ManaCost int64    `json:"mana_cost"`
	// On marks a running aura (SkillAura gems only) — the bar renders the
	// toggle state.
	On bool `json:"on,omitempty"`
	// Cd is the remaining cooldown in ticks (0 = ready) — the bar renders
	// the lockout without re-deriving cooldown math.
	Cd uint32 `json:"cd,omitempty"`
}

// SupportSnap is one support gem's static content: what the cutting/socket
// UI renders, plus which cuttable skills it legally sockets into
// (precomputed from tag requirements). Rides the welcome once.
type SupportSnap struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Desc     string   `json:"desc"`
	LegalFor []string `json:"legal_for"`
}

// SkillSnap is one cuttable skill's static content for the cutting dialog
// and skill bar. Rides the welcome once.
type SkillSnap struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
	Type      string    `json:"type"` // "welcome" | "snapshot" | "pause" | "run" | "roster" | "error"
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
	// Supports and CutSkills are the gem-content tables (static), welcome
	// only: every support gem, and every skill an uncut gem can offer.
	Supports  []SupportSnap `json:"supports,omitempty"`
	CutSkills []SkillSnap   `json:"cut_skills,omitempty"`
	// Name is the receiving client's own identity name ("" = guest);
	// welcome only. Roster maps live actor IDs to identity names — on every
	// welcome, and re-broadcast as "roster" when membership changes.
	Name   string            `json:"name,omitempty"`
	Roster map[uint64]string `json:"roster,omitempty"`
	// Error rides a terminal "error" frame: the connection is refused (a
	// duplicate session, say) and closes after this message.
	Error string `json:"error,omitempty"`
	// Social rides "social" frames — pushed to named players whenever the
	// online list, their party, or their pending invite changes.
	Social *SocialSnap `json:"social,omitempty"`
	// Stash is the receiving client's hideout bank: on welcomes (named
	// players only) and its own "stash" frames after every stash verb.
	Stash *StashSnap `json:"stash,omitempty"`
	// Sheet is the receiving client's computed character sheet, sent as a
	// "sheet" frame in answer to a "sheet" verb (the C panel). Derived
	// data the client can't compute — the stat engine lives server-side.
	Sheet *SheetSnap `json:"sheet,omitempty"`
}

// SheetSnap is one player's character sheet: evaluated stat lines plus
// per-gem combat numbers. Lines are server-authored (name + value) so new
// stats appear on the panel without a wire change.
type SheetSnap struct {
	Stats []SheetStatSnap `json:"stats"`
	Gems  []SheetGemSnap  `json:"gems,omitempty"`
}

// SheetStatSnap is one evaluated stat line. Val is milli fixed-point; Pct
// marks fraction-flavored stats (0.15 = 15%) for display.
type SheetStatSnap struct {
	Name string `json:"name"`
	Val  int64  `json:"val"`
	Pct  bool   `json:"pct,omitempty"`
}

// SheetGemSnap is one cut gem's computed numbers: nominal average non-crit
// hit (milli), cast time in milliseconds (windup + recovery at the actor's
// current speed), mana cost (milli), and the fan/chain shape after
// supports and gear.
type SheetGemSnap struct {
	Skill    string   `json:"skill"`
	Name     string   `json:"name"`
	Level    int      `json:"level"`
	ManaCost int64    `json:"mana_cost"`
	CastMs   int64    `json:"cast_ms"`
	AvgHit   int64    `json:"avg_hit,omitempty"`
	Fans     int      `json:"fans,omitempty"`
	Chains   int      `json:"chains,omitempty"`
	Supports []string `json:"supports,omitempty"`
}

// StashSnap is one identity's stash as the client sees it. Item IDs are
// stash indices (stash items have no entity); "stash_take" sends one back
// as Choice, "stash_put" targets a bag item's entity ID.
type StashSnap struct {
	Cap   int        `json:"cap"`
	Items []ItemSnap `json:"items"`
}

// SocialSnap is one named player's view of the social layer. Party is the
// named members of their world (guests are invisible here); Online is every
// other named player connected right now — the default-visible friends
// list; Invite names who wants them ("" = nobody).
type SocialSnap struct {
	Party  []string `json:"party"`
	Online []string `json:"online"`
	Invite string   `json:"invite,omitempty"`
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
// order starting at 1, which is how Commands reference them. Gems cuts
// extra level-1 skill gems onto the spawned actor (players start with only
// their def's starting gems; scenarios that exercise more skills say so).
type ScriptSpawn struct {
	Def  string   `json:"def"`
	X    int64    `json:"x"` // milli-units
	Y    int64    `json:"y"`
	Gems []string `json:"gems,omitempty"`
}
