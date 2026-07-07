// Identity — named players and their persistent characters. A name claim
// mints a random secret token, handed to the browser as an HttpOnly cookie;
// the token (never the name) is what authenticates a WebSocket back to its
// account, so knowing someone's name steals nothing. Guests skip all of
// this: no cookie, no store entry, a character that dies with the session.
//
// One token is an account holding a roster of characters (ROADMAP v2
// Track 2 item 1): every character has its own globally-unique name, the
// stash is account-wide, and the WS door picks a character by name
// (?char=). One session per account, whichever character it plays.
//
// The store is its own lock domain, touched from HTTP handlers (claim,
// whoami, WS upgrade) and the tick goroutine (character save on leave).
// Never call into it while holding the instance mutex.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JakeMalmrose/draupforge/sim/core"
)

const (
	tokenCookie = "draupforge_token"
	// saveInterval debounces store writes: the tick goroutine calls
	// SaveIfDue every tick, but a dirty store hits disk at most this often
	// (leaves and claims also save immediately — this is the crash net for
	// long-lived connections).
	saveInterval = 30 * time.Second
)

// nameRe: 2–16 chars, letters/digits with single spaces, dashes or
// underscores inside. Anchored on word characters so names can't disguise
// themselves with edge whitespace.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,14}[A-Za-z0-9]$`)

var (
	errNameTaken   = errors.New("name already taken")
	errNameInvalid = errors.New("names are 2-16 letters, digits, spaces, - or _")
	errRosterFull  = errors.New("character roster is full")
	errNoIdentity  = errors.New("unknown identity")
)

// CharSlot is one character on an account's roster. Char is the character
// as of the last save point (disconnect or periodic flush) — nil until the
// first bank, meaning "a fresh level-1 exile". Played orders the roster by
// recency; the most recently played slot is the default at the WS door.
type CharSlot struct {
	Name    string          `json:"name"`
	Char    *core.Character `json:"char,omitempty"`
	Created time.Time       `json:"created"`
	Played  time.Time       `json:"played,omitzero"`
}

// Identity is one account: a roster of named characters plus the shared
// stash — the hideout bank in durable character form, updated synchronously
// by stash verbs and account-wide by design (the alt loop: bank a drop on
// one character, take it out on the next). online/active are runtime-only.
type Identity struct {
	Chars   []*CharSlot     `json:"chars"`
	Stash   []core.CharItem `json:"stash,omitempty"`
	Created time.Time       `json:"created"`

	online bool
	// active is the lowercased name of the slot in play while online.
	// Tracked by name, not index: a concurrent character delete must never
	// re-aim a dying session's bank at a different slot.
	active string
}

// StashCap bounds the per-account stash — triple the bag, open for tuning.
const StashCap = 60

// RosterCap bounds characters per account — generous, but every slot is
// permanent disk, so it can't be unbounded.
const RosterCap = 12

// slot finds a roster entry by name, any case (nil when absent).
func (id *Identity) slot(name string) *CharSlot {
	for _, cs := range id.Chars {
		if strings.EqualFold(cs.Name, name) {
			return cs
		}
	}
	return nil
}

// defaultSlot is the character the WS door picks when the client doesn't:
// the most recently played, falling back to roster order for fresh
// accounts. Nil only for an empty roster (which Delete prevents outliving).
func (id *Identity) defaultSlot() *CharSlot {
	var best *CharSlot
	for _, cs := range id.Chars {
		if best == nil || cs.Played.After(best.Played) {
			best = cs
		}
	}
	return best
}

// IdentityStore maps secret tokens to accounts, with case-insensitive
// name uniqueness across every character of every account. path == ""
// keeps it memory-only (tests, throwaway runs).
type IdentityStore struct {
	mu        sync.Mutex
	byToken   map[string]*Identity
	byName    map[string]string // lowercased char name → token
	path      string
	dirty     bool
	lastFlush time.Time

	// claims throttles character minting per source — every claim is a
	// permanent store entry, so unthrottled it is a disk-filling lever.
	claims *claimLimiter
}

// identityFile is the on-disk shape, versioned like every other save.
// v1 was one character per identity (Name/Char at the top level); loading
// it migrates each entry to a one-slot roster.
type identityFile struct {
	Version    int                  `json:"version"`
	Identities map[string]*Identity `json:"identities"`
}

const identityFileVersion = 2

// identityV1 is the pre-roster on-disk identity, kept for migration.
type identityV1 struct {
	Name    string          `json:"name"`
	Char    *core.Character `json:"char,omitempty"`
	Stash   []core.CharItem `json:"stash,omitempty"`
	Created time.Time       `json:"created"`
}

// NewIdentityStore loads path if it exists (a missing file is an empty
// store, not an error — first boot). A v1 file loads via migration and
// rewrites as v2 on the next flush.
func NewIdentityStore(path string) (*IdentityStore, error) {
	st := &IdentityStore{
		byToken: map[string]*Identity{},
		byName:  map[string]string{},
		path:    path,
		claims:  newClaimLimiter(),
	}
	if path == "" {
		return st, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("server: identity store: %w", err)
	}
	var probe struct {
		Version    int             `json:"version"`
		Identities json.RawMessage `json:"identities"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("server: identity store %s: %w", path, err)
	}
	switch probe.Version {
	case identityFileVersion:
		if err := json.Unmarshal(probe.Identities, &st.byToken); err != nil {
			return nil, fmt.Errorf("server: identity store %s: %w", path, err)
		}
	case 1:
		var old map[string]*identityV1
		if err := json.Unmarshal(probe.Identities, &old); err != nil {
			return nil, fmt.Errorf("server: identity store %s: %w", path, err)
		}
		for tok, v1 := range old {
			st.byToken[tok] = &Identity{
				Chars:   []*CharSlot{{Name: v1.Name, Char: v1.Char, Created: v1.Created}},
				Stash:   v1.Stash,
				Created: v1.Created,
			}
		}
		st.dirty = true // persist the migration on the first flush
	default:
		return nil, fmt.Errorf("server: identity store version %d, this build reads %d", probe.Version, identityFileVersion)
	}
	if st.byToken == nil {
		st.byToken = map[string]*Identity{}
	}
	for tok, id := range st.byToken {
		for _, cs := range id.Chars {
			st.byName[strings.ToLower(cs.Name)] = tok
		}
	}
	return st, nil
}

// validName is the claim-time gate for character names.
func validName(name string) bool {
	return nameRe.MatchString(name) && !strings.Contains(name, "  ")
}

// Claim mints a fresh account whose first character is name, returning the
// account's new token.
func (st *IdentityStore) Claim(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", errNameInvalid
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, taken := st.byName[strings.ToLower(name)]; taken {
		return "", errNameTaken
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	now := time.Now().UTC()
	st.byToken[tok] = &Identity{
		Chars:   []*CharSlot{{Name: name, Created: now}},
		Created: now,
	}
	st.byName[strings.ToLower(name)] = tok
	st.dirty = true
	st.saveLocked()
	return tok, nil
}

// AddChar appends a fresh character named name to token's roster — the alt
// loop's front door. Name uniqueness is global, same as Claim.
func (st *IdentityStore) AddChar(token, name string) error {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return errNameInvalid
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return errNoIdentity
	}
	if _, taken := st.byName[strings.ToLower(name)]; taken {
		return errNameTaken
	}
	if len(id.Chars) >= RosterCap {
		return errRosterFull
	}
	id.Chars = append(id.Chars, &CharSlot{Name: name, Created: time.Now().UTC()})
	st.byName[strings.ToLower(name)] = token
	st.dirty = true
	st.saveLocked()
	return nil
}

// CharInfo is one roster entry's join-screen summary.
type CharInfo struct {
	Name  string `json:"name"`
	Level int    `json:"level"`
}

// Roster summarizes token's characters in roster order, plus the default
// (most recently played) character's name. Nil roster for unknown tokens.
func (st *IdentityStore) Roster(token string) (chars []CharInfo, last string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return nil, ""
	}
	for _, cs := range id.Chars {
		lvl := 1
		if cs.Char != nil {
			lvl = cs.Char.Level
		}
		chars = append(chars, CharInfo{Name: cs.Name, Level: lvl})
	}
	if def := id.defaultSlot(); def != nil {
		last = def.Name
	}
	return chars, last
}

// Name reports the display name behind a token: the character in play
// while online, otherwise the default character ("" for unknown tokens).
func (st *IdentityStore) Name(token string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return ""
	}
	if id.online {
		if cs := id.slot(id.active); cs != nil {
			return cs.Name
		}
	}
	if def := id.defaultSlot(); def != nil {
		return def.Name
	}
	return ""
}

// HasChar reports whether name is on token's own roster (any case).
func (st *IdentityStore) HasChar(token, name string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	return id != nil && id.slot(strings.TrimSpace(name)) != nil
}

// TokenByName resolves a character's display name (any case) to its
// account token.
func (st *IdentityStore) TokenByName(name string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.byName[strings.ToLower(name)]
}

// OnlineNames lists the characters connected named players are playing,
// sorted — the default-visible "friends list" of multiplayer.md.
func (st *IdentityStore) OnlineNames() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var names []string
	for _, id := range st.byToken {
		if !id.online {
			continue
		}
		if cs := id.slot(id.active); cs != nil {
			names = append(names, cs.Name)
		}
	}
	sort.Strings(names)
	return names
}

// Connect authenticates a token, picks the character named charName ("" or
// a stale name falls back to the most recently played), and marks the
// account online. A token that is unknown or already online doesn't
// connect; the caller treats the former as a guest-with-stale-cookie and
// the latter as a duplicate session.
func (st *IdentityStore) Connect(token, charName string) (name string, char *core.Character, ok bool, dup bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return "", nil, false, false
	}
	if id.online {
		if cs := id.slot(id.active); cs != nil {
			return cs.Name, nil, false, true
		}
		return "", nil, false, true
	}
	cs := id.slot(strings.TrimSpace(charName))
	if cs == nil {
		cs = id.defaultSlot()
	}
	if cs == nil {
		return "", nil, false, false // empty roster; shouldn't outlive Delete
	}
	id.online = true
	id.active = strings.ToLower(cs.Name)
	cs.Played = time.Now().UTC()
	st.dirty = true
	return cs.Name, cs.Char, true, false
}

// connectWithGrace is Connect with patience for the reconnect race: a page
// reload's new socket can beat its old session's leave (which frees the
// online slot a tick later), so a dup here retries briefly before it is
// believed. A genuine second session just gets its refusal ~½s late.
func (st *IdentityStore) connectWithGrace(token, charName string) (name string, char *core.Character, ok, dup bool) {
	for i := 0; ; i++ {
		name, char, ok, dup = st.Connect(token, charName)
		if !dup || i >= 20 {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// Disconnect banks the freshest character into the slot in play and frees
// the online flag. A nil char keeps whatever was banked before (a session
// that never managed to extract shouldn't wipe the previous save). A slot
// deleted mid-session banks nowhere — no resurrection.
func (st *IdentityStore) Disconnect(token string, char *core.Character) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return
	}
	if char != nil {
		if cs := id.slot(id.active); cs != nil {
			cs.Char = char
		}
	}
	id.online = false
	id.active = ""
	st.dirty = true
	st.saveLocked()
}

// DeleteChar removes the character named name from token's roster —
// character and name reservation, persisted immediately. Deleting the last
// character deletes the account outright, shared stash included (gone
// reports it, so the handler can expire the cookie). wasActive reports
// that a live session was playing the deleted character — the caller
// kicks it; the leave that follows banks nothing because the slot is gone.
func (st *IdentityStore) DeleteChar(token, name string) (deleted string, wasActive, gone bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return "", false, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if def := id.defaultSlot(); def != nil {
			name = def.Name // legacy no-body forget: the default character
		}
	}
	idx := -1
	for i, cs := range id.Chars {
		if strings.EqualFold(cs.Name, name) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false, false
	}
	deleted = id.Chars[idx].Name
	wasActive = id.online && strings.EqualFold(id.active, deleted)
	id.Chars = append(id.Chars[:idx], id.Chars[idx+1:]...)
	delete(st.byName, strings.ToLower(deleted))
	if wasActive {
		id.active = ""
	}
	if len(id.Chars) == 0 {
		delete(st.byToken, token)
		gone = true
	}
	st.dirty = true
	st.saveLocked()
	return deleted, wasActive, gone
}

// Bank updates the connected account's in-play character without touching
// the online flag — the periodic crash net for long-lived sessions.
func (st *IdentityStore) Bank(token string, char *core.Character) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil || !id.online || char == nil {
		return
	}
	if cs := id.slot(id.active); cs != nil {
		cs.Char = char
		st.dirty = true
	}
}

// StashAdd banks one item into a token's account stash; false when the
// token is unknown or the stash is full. Persists on the usual flush
// schedule.
func (st *IdentityStore) StashAdd(token string, item core.CharItem) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil || len(id.Stash) >= StashCap {
		return false
	}
	id.Stash = append(id.Stash, item)
	st.dirty = true
	return true
}

// StashTake removes and returns the stash item at idx; false when the
// token is unknown or idx is out of range.
func (st *IdentityStore) StashTake(token string, idx int) (core.CharItem, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil || idx < 0 || idx >= len(id.Stash) {
		return core.CharItem{}, false
	}
	item := id.Stash[idx]
	id.Stash = append(id.Stash[:idx], id.Stash[idx+1:]...)
	st.dirty = true
	return item, true
}

// StashList copies a token's stash for snapshot building (nil for guests
// and unknown tokens).
func (st *IdentityStore) StashList(token string) []core.CharItem {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil || len(id.Stash) == 0 {
		return nil
	}
	out := make([]core.CharItem, len(id.Stash))
	copy(out, id.Stash)
	return out
}

// SaveIfDue flushes a dirty store at most every saveInterval. Called every
// tick; the usual case is a cheap mutex bounce.
func (st *IdentityStore) SaveIfDue() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.dirty || time.Since(st.lastFlush) < saveInterval {
		return
	}
	st.saveLocked()
}

// saveLocked writes the store atomically (tmp + rename). Callers hold mu.
// Failures log and stay dirty — the next flush retries.
func (st *IdentityStore) saveLocked() {
	if st.path == "" {
		st.dirty = false
		return
	}
	f := identityFile{Version: identityFileVersion, Identities: st.byToken}
	raw, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		log.Printf("server: identity save: %v", err)
		return
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("server: identity save: %v", err)
		return
	}
	if err := os.Rename(tmp, st.path); err != nil {
		log.Printf("server: identity save: %v", err)
		return
	}
	st.dirty = false
	st.lastFlush = time.Now()
}

// ---------------------------------------------------------------- HTTP

// handleClaim is POST /api/claim {"name": "..."}: mint a character and set
// the account's token cookie. A request whose cookie already owns the name
// gets it back untouched — reloading the join page never forks anything.
// A valid cookie with a new name grows the roster (the alt loop) instead
// of orphaning the old identity; no cookie mints a fresh account.
func (st *IdentityStore) handleClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
		return
	}
	name := strings.TrimSpace(body.Name)
	// Re-claiming a character you already own is a no-op login (a reload
	// mid-claim, say). Throttled after this path — reloads don't burn
	// budget — but before the store grows a permanent entry.
	if tok := cookieToken(r); tok != "" && st.Name(tok) != "" {
		if st.HasChar(tok, name) {
			json.NewEncoder(w).Encode(map[string]string{"name": name})
			return
		}
		if !st.claims.allow(sourceKey(r), time.Now()) {
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "too many name claims; wait a minute"})
			return
		}
		if err := st.AddChar(tok, name); err != nil {
			code := http.StatusBadRequest
			if err == errNameTaken {
				code = http.StatusConflict
			}
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"name": name})
		return
	}
	if !st.claims.allow(sourceKey(r), time.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "too many name claims; wait a minute"})
		return
	}
	tok, err := st.Claim(name)
	if err != nil {
		code := http.StatusBadRequest
		if err == errNameTaken {
			code = http.StatusConflict
		}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     tokenCookie,
		Value:    tok,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Behind the funnel/any TLS proxy the Go server sees plain HTTP;
		// the forwarded proto is what the browser actually speaks.
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	json.NewEncoder(w).Encode(map[string]string{"name": name})
}

// handleForget builds the POST /api/forget handler: permanently delete one
// character — {"name": "..."} picks it; an empty body keeps the legacy
// meaning (the default character). The roster survives unless this was its
// last entry, in which case the whole account goes — shared stash, name
// reservations, cookie. The store entry goes first, THEN kick severs the
// live session if it was playing the deleted character, so the session's
// leave path banks nothing into a gone slot.
func (st *IdentityStore) handleForget(kick func(token string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Name string `json:"name"`
		}
		// A bare POST (legacy button) carries no body; ignore decode errors.
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body)
		tok := cookieToken(r)
		deleted, wasActive, gone := st.DeleteChar(tok, body.Name)
		if wasActive && kick != nil {
			kick(tok)
		}
		if gone {
			http.SetCookie(w, &http.Cookie{
				Name:     tokenCookie,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"deleted": deleted, "gone": gone})
	}
}

// handleWhoami is GET /api/whoami: the account's roster —
// {"name":"<default>","chars":[{"name","level"},...]} for a valid token,
// {} otherwise (including after a server-side wipe — the client then shows
// the join screen and the stale cookie gets replaced by the next claim).
func (st *IdentityStore) handleWhoami(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if tok := cookieToken(r); tok != "" {
		if chars, last := st.Roster(tok); len(chars) > 0 {
			json.NewEncoder(w).Encode(map[string]any{"name": last, "chars": chars})
			return
		}
	}
	w.Write([]byte("{}\n"))
}

func cookieToken(r *http.Request) string {
	c, err := r.Cookie(tokenCookie)
	if err != nil {
		return ""
	}
	return c.Value
}
