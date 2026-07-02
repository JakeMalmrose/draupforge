// Identity — named players and their persistent characters. A name claim
// mints a random secret token, handed to the browser as an HttpOnly cookie;
// the token (never the name) is what authenticates a WebSocket back to its
// character, so knowing someone's name steals nothing. Guests skip all of
// this: no cookie, no store entry, a character that dies with the session.
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
)

// Identity is one named player. Char is the character as of the last save
// point (disconnect or periodic flush); online is runtime-only.
type Identity struct {
	Name    string          `json:"name"`
	Char    *core.Character `json:"char,omitempty"`
	Created time.Time       `json:"created"`

	online bool
}

// IdentityStore maps secret tokens to identities, with case-insensitive
// name uniqueness. path == "" keeps it memory-only (tests, throwaway runs).
type IdentityStore struct {
	mu        sync.Mutex
	byToken   map[string]*Identity
	byName    map[string]string // lowercased name → token
	path      string
	dirty     bool
	lastFlush time.Time
}

// identityFile is the on-disk shape, versioned like every other save.
type identityFile struct {
	Version    int                  `json:"version"`
	Identities map[string]*Identity `json:"identities"`
}

const identityFileVersion = 1

// NewIdentityStore loads path if it exists (a missing file is an empty
// store, not an error — first boot).
func NewIdentityStore(path string) (*IdentityStore, error) {
	st := &IdentityStore{
		byToken: map[string]*Identity{},
		byName:  map[string]string{},
		path:    path,
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
	var f identityFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("server: identity store %s: %w", path, err)
	}
	if f.Version != identityFileVersion {
		return nil, fmt.Errorf("server: identity store version %d, this build reads %d", f.Version, identityFileVersion)
	}
	st.byToken = f.Identities
	if st.byToken == nil {
		st.byToken = map[string]*Identity{}
	}
	for tok, id := range st.byToken {
		st.byName[strings.ToLower(id.Name)] = tok
	}
	return st, nil
}

// Claim registers name and returns its fresh token.
func (st *IdentityStore) Claim(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !nameRe.MatchString(name) || strings.Contains(name, "  ") {
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
	st.byToken[tok] = &Identity{Name: name, Created: time.Now().UTC()}
	st.byName[strings.ToLower(name)] = tok
	st.dirty = true
	st.saveLocked()
	return tok, nil
}

// Name reports the identity name behind a token ("" for unknown).
func (st *IdentityStore) Name(token string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if id := st.byToken[token]; id != nil {
		return id.Name
	}
	return ""
}

// TokenByName resolves a display name (any case) to its token.
func (st *IdentityStore) TokenByName(name string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.byName[strings.ToLower(name)]
}

// OnlineNames lists connected named players, sorted — the default-visible
// "friends list" of multiplayer.md.
func (st *IdentityStore) OnlineNames() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var names []string
	for _, id := range st.byToken {
		if id.online {
			names = append(names, id.Name)
		}
	}
	sort.Strings(names)
	return names
}

// Connect authenticates a token and marks it online. A token that is
// unknown or already online doesn't connect; the caller treats the former
// as a guest-with-stale-cookie and the latter as a duplicate session.
func (st *IdentityStore) Connect(token string) (name string, char *core.Character, ok bool, dup bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return "", nil, false, false
	}
	if id.online {
		return id.Name, nil, false, true
	}
	id.online = true
	return id.Name, id.Char, true, false
}

// connectWithGrace is Connect with patience for the reconnect race: a page
// reload's new socket can beat its old session's leave (which frees the
// online slot a tick later), so a dup here retries briefly before it is
// believed. A genuine second session just gets its refusal ~½s late.
func (st *IdentityStore) connectWithGrace(token string) (name string, char *core.Character, ok, dup bool) {
	for i := 0; ; i++ {
		name, char, ok, dup = st.Connect(token)
		if !dup || i >= 20 {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// Disconnect banks the freshest character and frees the online slot.
// A nil char keeps whatever was banked before (a session that never
// managed to extract shouldn't wipe the previous save).
func (st *IdentityStore) Disconnect(token string, char *core.Character) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil {
		return
	}
	id.online = false
	if char != nil {
		id.Char = char
	}
	st.dirty = true
	st.saveLocked()
}

// Bank updates a connected identity's character without touching the
// online flag — the periodic crash net for long-lived sessions.
func (st *IdentityStore) Bank(token string, char *core.Character) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if id := st.byToken[token]; id != nil && char != nil {
		id.Char = char
		st.dirty = true
	}
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

// handleClaim is POST /api/claim {"name": "..."}: mint an identity and set
// its token cookie. A request that already carries a valid token gets its
// existing name back — reloading the join page never forks an identity.
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
	// Re-claiming your own name is a no-op login (a reload mid-claim, say);
	// a different name deliberately starts a fresh identity — the new
	// cookie replaces the old one, whose character stays banked under it.
	if tok := cookieToken(r); tok != "" {
		if name := st.Name(tok); name != "" &&
			strings.EqualFold(name, strings.TrimSpace(body.Name)) {
			json.NewEncoder(w).Encode(map[string]string{"name": name})
			return
		}
	}
	tok, err := st.Claim(body.Name)
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
	json.NewEncoder(w).Encode(map[string]string{"name": strings.TrimSpace(body.Name)})
}

// handleWhoami is GET /api/whoami: {"name":"..."} for a valid token, {}
// otherwise (including after a server-side wipe — the client then shows
// the join screen and the stale cookie gets replaced by the next claim).
func (st *IdentityStore) handleWhoami(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if tok := cookieToken(r); tok != "" {
		if name := st.Name(tok); name != "" {
			json.NewEncoder(w).Encode(map[string]string{"name": name})
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
