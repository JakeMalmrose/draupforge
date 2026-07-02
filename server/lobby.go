// The lobby — many instances, one social layer (multiplayer.md phase 2).
// A party IS an instance: every fresh connection gets its own world and
// run; an accepted invite moves you into the inviter's instance with the
// same extract/inject/re-welcome machinery a floor swap uses; leaving a
// party moves you to a fresh solo world. There is no separate party object
// to keep in sync — membership is just "who is in this instance".
//
// Lock ordering: lobby.mu → instance.mu → (never both with) client.mu, and
// lobby.mu → IdentityStore.mu. Instance tick goroutines call lobby methods
// only while holding no instance mutex; the lobby never touches a world —
// it moves clients between join queues and lets each tick goroutine do the
// world work.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// reapAfter is how long an empty instance survives — the reconnect grace:
// a named player who drops (reload, flaky wifi) and returns inside this
// window lands back in their old world, run intact.
const reapAfter = 60 * time.Second

type Lobby struct {
	db  *core.ContentDB
	cfg Config
	ids *IdentityStore
	ctx context.Context // set by ListenAndServe; parents every instance

	mu        sync.Mutex
	instances map[int]*instanceRef
	nextID    int
	seedN     uint64
	online    map[string]*client   // token → connected client
	homes     map[string]*Instance // token → last instance, for grace reconnects
	invites   map[string]string    // invitee token → inviter token

	listenerAddr chan net.Addr
}

type instanceRef struct {
	in     *Instance
	cancel context.CancelFunc
}

// NewLobby validates cfg for lobby duty. cfg.Load is a single-world idea
// and is refused here — run saves predate parties (STATUS.md shortcut).
func NewLobby(db *core.ContentDB, cfg Config) (*Lobby, error) {
	if cfg.Load != nil {
		return nil, fmt.Errorf("server: -load is not supported in lobby mode yet")
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = time.Second / core.TicksPerSecond
	}
	if cfg.Seed == 0 {
		// Roll here, not per instance: every instance seed derives from
		// this one, so one logged number reproduces the whole session.
		cfg.Seed = randomSeed()
		log.Printf("server: rolled world seed %d (pass -seed %d to reproduce)", cfg.Seed, cfg.Seed)
	}
	ids, err := NewIdentityStore(cfg.IdentityPath)
	if err != nil {
		return nil, err
	}
	return &Lobby{
		db: db, cfg: cfg, ids: ids,
		instances:    map[int]*instanceRef{},
		online:       map[string]*client{},
		homes:        map[string]*Instance{},
		invites:      map[string]string{},
		listenerAddr: make(chan net.Addr, 1),
	}, nil
}

// Addr returns the bound TCP debug address once ListenAndServe is up.
func (lb *Lobby) Addr() net.Addr { return <-lb.listenerAddr }

// ListenAndServe owns every listener (TCP debug, HTTP, admin) plus the
// instance reaper, and blocks until ctx ends. Instances tick on their own
// goroutines, created as players arrive.
func (lb *Lobby) ListenAndServe(ctx context.Context) error {
	lb.ctx = ctx
	ln, err := net.Listen("tcp", lb.cfg.Addr)
	if err != nil {
		close(lb.listenerAddr)
		return err
	}
	lb.listenerAddr <- ln.Addr()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go lb.acceptLoop(ctx, ln)
	if lb.cfg.HTTPAddr != "" {
		srv := &http.Server{Addr: lb.cfg.HTTPAddr, Handler: lb.Handler()}
		go func() { <-ctx.Done(); srv.Close() }()
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("server: http listener: %v", err)
			}
		}()
	}
	if lb.cfg.AdminAddr != "" {
		srv := &http.Server{Addr: lb.cfg.AdminAddr, Handler: lb.adminHandler()}
		go func() { <-ctx.Done(); srv.Close() }()
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("server: admin listener: %v", err)
			}
		}()
	}
	reap := time.NewTicker(5 * time.Second)
	defer reap.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-reap.C:
			lb.reap()
		}
	}
}

// Handler is the lobby's public HTTP surface: WS, identity API, web client.
func (lb *Lobby) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", lb.HandleWS)
	mux.HandleFunc("/api/claim", lb.ids.handleClaim)
	mux.HandleFunc("/api/whoami", lb.ids.handleWhoami)
	if lb.cfg.StaticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(lb.cfg.StaticDir)))
	}
	return mux
}

// newInstance builds and starts one more world. Each instance gets its own
// derived seed so parallel parties don't race identical dungeons; the base
// seed itself is rolled at boot when the config left it 0 (New logs it).
func (lb *Lobby) newInstanceLocked() (*Instance, error) {
	icfg := lb.cfg
	icfg.IdentityPath = "" // the lobby's store is shared, not per-instance
	icfg.Seed = deriveSeed(lb.cfg.Seed, 0x10b_b700+lb.seedN)
	if icfg.Seed == 0 {
		icfg.Seed = 1 // 0 would re-roll randomly in New; keep derivation pure
	}
	lb.seedN++
	in, err := New(lb.db, icfg)
	if err != nil {
		return nil, err
	}
	in.ids = lb.ids
	in.lobby = lb
	in.id = lb.nextID
	lb.nextID++
	in.publishParty() // seed the telemetry before the first tick
	ictx, cancel := context.WithCancel(lb.ctx)
	lb.instances[in.id] = &instanceRef{in: in, cancel: cancel}
	go in.runLoop(ictx)
	return in, nil
}

// place picks the instance for a connecting client: a named player whose
// old world still stands (grace window) goes home; everyone else gets a
// fresh solo world.
func (lb *Lobby) place(c *client) (*Instance, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if c.token != "" {
		if home := lb.homes[c.token]; home != nil {
			if ref := lb.instances[home.id]; ref != nil && ref.in == home {
				home.emptyAt.Store(0) // claimed; the reaper keeps its hands off
				return home, nil
			}
		}
	}
	in, err := lb.newInstanceLocked()
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		lb.homes[c.token] = in
	}
	return in, nil
}

// HandleWS is the lobby's WebSocket door: identity from the token cookie
// (?guest=1 skips it), then placement and the ordinary read loop.
func (lb *Lobby) HandleWS(w http.ResponseWriter, r *http.Request) {
	ws, m, ok := acceptWS(w, r)
	if !ok {
		return
	}
	c := &client{tr: &wsTransport{conn: ws}, mode: m}
	if tok := cookieToken(r); tok != "" && r.URL.Query().Get("guest") == "" {
		name, char, ok, dup := lb.ids.connectWithGrace(tok)
		switch {
		case dup:
			frame, _ := json.Marshal(protocol.ServerMsg{
				Type: "error", Error: fmt.Sprintf("%s is already connected", name),
			})
			c.send(frame, false)
			c.tr.Close()
			return
		case ok:
			c.name, c.token = name, tok
			if char != nil {
				c.lastChar, c.hasChar = *char, true
			}
		}
	}
	in, err := lb.place(c)
	if err != nil {
		log.Printf("server: place client: %v", err)
		if c.token != "" {
			lb.ids.Disconnect(c.token, nil)
		}
		c.tr.Close()
		return
	}
	c.mu.Lock()
	c.inst = in
	c.mu.Unlock()
	in.mu.Lock()
	in.joins = append(in.joins, c)
	in.mu.Unlock()
	if c.token != "" {
		lb.mu.Lock()
		lb.online[c.token] = c
		lb.broadcastSocialLocked()
		lb.mu.Unlock()
	}
	readLoop(r.Context(), c)
}

// acceptLoop gives each TCP debug connection its own world, like a guest.
func (lb *Lobby) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		lb.mu.Lock()
		in, ierr := lb.newInstanceLocked()
		lb.mu.Unlock()
		if ierr != nil {
			conn.Close()
			continue
		}
		c := &client{tr: newTCPTransport(conn), mode: modeJSONWorld, inst: in}
		in.mu.Lock()
		in.joins = append(in.joins, c)
		in.mu.Unlock()
		go readLoop(ctx, c)
	}
}

// playerLeft is removeClient's lobby hook (tick goroutine, no instance
// mutex held): drop the online entry and tell everyone the list changed.
// The home entry stays — that's the grace-reconnect breadcrumb.
func (lb *Lobby) playerLeft(c *client) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if lb.online[c.token] == c {
		delete(lb.online, c.token)
	}
	delete(lb.invites, c.token)
	lb.broadcastSocialLocked()
}

// partyChanged is tick's membership hook: someone joined, left, or moved.
// Party views are per-client, so just refresh everyone affected — social
// frames are tiny and membership changes are rare.
func (lb *Lobby) partyChanged(in *Instance) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.broadcastSocialLocked()
}

// processSocial handles one tick's worth of a single instance's social
// verbs. Runs on that instance's tick goroutine, no instance mutex held.
func (lb *Lobby) processSocial(in *Instance, wants []socialWant) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	for _, w := range wants {
		c := w.c
		if c.token == "" {
			continue // guests have no social surface; claim a name first
		}
		switch {
		case w.invite != "":
			lb.inviteLocked(c, w.invite)
		case w.accept:
			lb.acceptLocked(in, c)
		case w.decline:
			delete(lb.invites, c.token)
			lb.pushSocialLocked(c)
		case w.leave:
			lb.leavePartyLocked(in, c)
		}
	}
}

// inviteLocked files a party invite by display name. One pending invite
// per invitee; the newest wins.
func (lb *Lobby) inviteLocked(from *client, name string) {
	tok := lb.ids.TokenByName(name)
	if tok == "" || tok == from.token {
		return
	}
	to := lb.online[tok]
	if to == nil {
		lb.pushSocialLocked(from) // they just went offline; refresh the list
		return
	}
	if sameInstance(from, to) {
		return // already partied up
	}
	lb.invites[tok] = from.token
	lb.pushSocialLocked(to)
}

// acceptLocked moves the accepting client into the inviter's instance.
// Runs on the acceptor's tick goroutine — the release is world work and
// must happen there; the adoption is just a queue append.
func (lb *Lobby) acceptLocked(in *Instance, c *client) {
	fromTok, pending := lb.invites[c.token]
	delete(lb.invites, c.token)
	if !pending {
		return
	}
	inviter := lb.online[fromTok]
	if inviter == nil {
		lb.pushSocialLocked(c) // inviter left; the invite just evaporates
		return
	}
	inviter.mu.Lock()
	to := inviter.inst
	inviter.mu.Unlock()
	if to == in {
		lb.pushSocialLocked(c)
		return
	}
	lb.transferLocked(in, to, c)
}

// leavePartyLocked moves the client to a fresh solo world — unless they
// are already alone, in which case there is nothing to leave.
func (lb *Lobby) leavePartyLocked(in *Instance, c *client) {
	if in.clientN.Load() <= 1 {
		return
	}
	to, err := lb.newInstanceLocked()
	if err != nil {
		log.Printf("server: leave party: %v", err)
		return
	}
	lb.transferLocked(in, to, c)
}

// transferLocked is the move itself: out of the old world on this tick
// goroutine, into the new instance's join queue for its next tick. The
// same socket carries a fresh welcome (bumped generation) — to the client
// it looks exactly like a floor swap.
func (lb *Lobby) transferLocked(from, to *Instance, c *client) {
	if !from.releaseClient(c) {
		return // raced a disconnect; the leave path owns them now
	}
	c.mu.Lock()
	c.inst = to
	c.mu.Unlock()
	lb.homes[c.token] = to
	to.emptyAt.Store(0)
	to.mu.Lock()
	to.joins = append(to.joins, c)
	to.mu.Unlock()
	// The source instance's membership changed outside its join/leave
	// queues, so its tick won't announce this — do it here.
	lb.broadcastSocialLocked()
}

// SocialSnap for one client: their party, everyone online, their invite.
func (lb *Lobby) socialSnapLocked(c *client) *protocol.SocialSnap {
	snap := &protocol.SocialSnap{Party: []string{}, Online: []string{}}
	c.mu.Lock()
	in := c.inst
	c.mu.Unlock()
	if in != nil {
		if names, ok := in.partyNames.Load().([]string); ok {
			snap.Party = names
		}
	}
	for _, name := range lb.ids.OnlineNames() {
		if name != c.name {
			snap.Online = append(snap.Online, name)
		}
	}
	if fromTok, ok := lb.invites[c.token]; ok {
		snap.Invite = lb.ids.Name(fromTok)
	}
	return snap
}

func (lb *Lobby) pushSocialLocked(c *client) {
	frame, _ := json.Marshal(protocol.ServerMsg{Type: "social", Social: lb.socialSnapLocked(c)})
	if !c.send(frame, false) {
		c.tr.Close()
	}
}

func (lb *Lobby) broadcastSocialLocked() {
	for _, c := range lb.online {
		lb.pushSocialLocked(c)
	}
}

// reap retires instances that have stood empty past the grace window,
// freeing their tick goroutines and forgetting stale homes.
func (lb *Lobby) reap() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	now := time.Now().UnixNano()
	for id, ref := range lb.instances {
		if ref.in.clientN.Load() != 0 {
			continue
		}
		emptyAt := ref.in.emptyAt.Load()
		if emptyAt == 0 || now-emptyAt < int64(reapAfter) {
			continue
		}
		ref.in.mu.Lock()
		joining := len(ref.in.joins)
		ref.in.mu.Unlock()
		if joining > 0 {
			continue // someone is mid-door; let their tick land
		}
		ref.cancel()
		delete(lb.instances, id)
		for tok, home := range lb.homes {
			if home == ref.in {
				delete(lb.homes, tok)
			}
		}
	}
}

func sameInstance(a, b *client) bool {
	a.mu.Lock()
	ia := a.inst
	a.mu.Unlock()
	b.mu.Lock()
	ib := b.inst
	b.mu.Unlock()
	return ia == ib
}

// adminHandler lists instances and mounts each one's admin dashboard under
// /i/{id}/. Same rule as ever: no auth, never expose it publicly.
func (lb *Lobby) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		lb.mu.Lock()
		defer lb.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>draupforge lobby</title><h1>instances</h1><ul>")
		for id, ref := range lb.instances {
			party := ""
			if names, ok := ref.in.partyNames.Load().([]string); ok && len(names) > 0 {
				party = " — " + fmt.Sprint(names)
			}
			fmt.Fprintf(w, `<li><a href="/i/%d/">instance %d</a>: %d client(s)%s</li>`,
				id, id, ref.in.clientN.Load(), party)
		}
		fmt.Fprint(w, "</ul>")
	})
	mux.HandleFunc("/i/{id}/", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		lb.mu.Lock()
		ref := lb.instances[id]
		lb.mu.Unlock()
		if ref == nil {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix(fmt.Sprintf("/i/%d", id), ref.in.adminMux()).ServeHTTP(w, r)
	})
	return mux
}
