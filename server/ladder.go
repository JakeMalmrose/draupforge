// The ladder + the death recap (ROADMAP v2 Track 2 item 5). Best-floor is
// recorded per CHARACTER on the account store, and the entry carries the
// build that reached it — skills, supports, uniques off the banked
// character — because "floor 52 on a poison dagger" is the best
// build-experimentation ad the game can run. The recap turns a death into
// a build lesson: what hit you, for how much, on which floor, under which
// mods.
package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/sim/core"
)

// BuildSnap is a ladder entry's build: what the character was playing when
// it set its best floor. Display names, resolved at record time — the
// ladder reads without a content db.
type BuildSnap struct {
	Level   int        `json:"level"`
	Gems    []BuildGem `json:"gems,omitempty"`
	Uniques []string   `json:"uniques,omitempty"`
}

// BuildGem is one cut gem with its socketed supports, by display name.
type BuildGem struct {
	Skill    string   `json:"skill"`
	Level    int      `json:"level"`
	Supports []string `json:"supports,omitempty"`
}

// buildSnapOf renders a banked character's build with display names.
func buildSnapOf(db *core.ContentDB, ch *core.Character) *BuildSnap {
	b := &BuildSnap{Level: ch.Level}
	for _, g := range ch.Gems {
		bg := BuildGem{Skill: g.Skill, Level: g.Level}
		if sk := db.Skills[g.Skill]; sk != nil {
			bg.Skill = sk.Name
		}
		for _, sid := range g.Supports {
			if sid == "" {
				continue
			}
			name := sid
			if sup := db.Support(sid); sup != nil {
				name = sup.Name
			}
			bg.Supports = append(bg.Supports, name)
		}
		b.Gems = append(b.Gems, bg)
	}
	seen := map[string]bool{}
	for _, it := range ch.Equipment {
		if it == nil || it.Unique == "" || seen[it.Unique] {
			continue
		}
		seen[it.Unique] = true
		name := it.Unique
		if u := db.Unique(it.Unique); u != nil {
			name = u.Name
		}
		b.Uniques = append(b.Uniques, name)
	}
	return b
}

// LadderEntry is one row of /api/ladder.
type LadderEntry struct {
	Name  string     `json:"name"`
	Level int        `json:"level"`
	Best  int        `json:"best"`
	Build *BuildSnap `json:"build,omitempty"`
}

const ladderCap = 100

// recapHitCap bounds each client's recent-hit ring — the recap's memory.
const recapHitCap = 8

// RecordBest banks a new best floor (and the build that reached it) on the
// account's in-play character. Floors at or below the recorded best no-op;
// bests only climb — additive, like all account progression.
func (st *IdentityStore) RecordBest(token string, floor int, build *BuildSnap) {
	if floor <= 0 {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	id := st.byToken[token]
	if id == nil || !id.online {
		return
	}
	cs := id.slot(id.active)
	if cs == nil || floor <= cs.Best {
		return
	}
	cs.Best = floor
	cs.BestBuild = build
	st.dirty = true
}

// Ladder lists every character with a recorded best, deepest first (level,
// then name break ties), capped at ladderCap rows.
func (st *IdentityStore) Ladder() []LadderEntry {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []LadderEntry
	for _, id := range st.byToken {
		for _, cs := range id.Chars {
			if cs.Best <= 0 {
				continue
			}
			lvl := 1
			if cs.Char != nil {
				lvl = cs.Char.Level
			}
			out = append(out, LadderEntry{Name: cs.Name, Level: lvl, Best: cs.Best, Build: cs.BestBuild})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Best != out[j].Best {
			return out[i].Best > out[j].Best
		}
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > ladderCap {
		out = out[:ladderCap]
	}
	return out
}

// handleLadder is GET /api/ladder: the board, deepest first.
func (st *IdentityStore) handleLadder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ladder": st.Ladder()})
}

// recapFor builds one dying client's death recap: the floor, its mods,
// and the recent hits the tick goroutine recorded against them.
func (in *Instance) recapFor(c *client) *protocol.RecapSnap {
	rec := &protocol.RecapSnap{Floor: in.floor, Hits: c.recentHits}
	if in.floor > 0 {
		for _, m := range rollFloorMods(in.runSeed, in.floor, in.route, in.chamber,
			modCountAt(in.floor, in.route, in.chamber)) {
			rec.Mods = append(rec.Mods, m.Name)
		}
	}
	return rec
}
