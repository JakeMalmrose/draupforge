// Feats + hideout trophies (ROADMAP v2 Track 2 item 7). Account-wide
// achievements on deterministic triggers the host layer already sees —
// floors reached, set-pieces felled, a boss dropped without taking a hit —
// paying out trophy dressing in the hideout and a list the panel shows.
// Some feats are only reachable by builds you haven't played yet; the
// hideout becomes the account's history made visible. Strictly additive,
// never reset — the non-seasonal rule.
package server

import "sort"

// featDef is one achievement. The client mirrors ids to names/desc for
// display (FEAT_META); the wire carries ids only.
type featDef struct {
	ID   string
	Name string
}

// featTable is ordered for display; append-only like every table.
var featTable = []featDef{
	{ID: "depth_10", Name: "Ten Floors Under"},
	{ID: "depth_20", Name: "The Cold Below"},
	{ID: "depth_30", Name: "Where Light Forgets"},
	{ID: "hc_10", Name: "No Second Chances"},
	{ID: "guardian", Name: "Wallbreaker"},
	{ID: "king", Name: "Kingslayer"},
	{ID: "king_untouched", Name: "Untouchable"},
	{ID: "apex", Name: "Tyrant's End"},
}

// AddFeat records an achievement on the account; true when it's new.
func (st *IdentityStore) AddFeat(token, id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	acct := st.byToken[token]
	if acct == nil {
		return false
	}
	i := sort.SearchStrings(acct.Feats, id)
	if i < len(acct.Feats) && acct.Feats[i] == id {
		return false
	}
	acct.Feats = append(acct.Feats, "")
	copy(acct.Feats[i+1:], acct.Feats[i:])
	acct.Feats[i] = id
	st.dirty = true
	return true
}

// Feats copies a token's achievements (nil for guests/unknown).
func (st *IdentityStore) Feats(token string) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	acct := st.byToken[token]
	if acct == nil || len(acct.Feats) == 0 {
		return nil
	}
	out := make([]string, len(acct.Feats))
	copy(out, acct.Feats)
	return out
}

// awardFeat gives one client's account an achievement, announcing only
// first earns — re-earning is silent, the history already holds it.
func (in *Instance) awardFeat(c *client, id string) {
	if c.token == "" {
		return
	}
	if in.ids.AddFeat(c.token, id) {
		in.syntheticEvent("feat", 0, id)
	}
}

// depthFeats awards what reaching a floor proves, per client.
func (in *Instance) depthFeats(floor int) {
	for _, c := range in.clients {
		if c.token == "" {
			continue
		}
		if floor >= 10 {
			in.awardFeat(c, "depth_10")
			if c.hardcore {
				in.awardFeat(c, "hc_10")
			}
		}
		if floor >= 20 {
			in.awardFeat(c, "depth_20")
		}
		if floor >= 30 {
			in.awardFeat(c, "depth_30")
		}
	}
}

// killFeats awards what a fallen set-piece proves. The untouched check
// leans on the recap ring: cleared at floor entry, so an empty ring at
// the kill means no hit landed on that client the whole floor.
func (in *Instance) killFeats(def string) {
	for _, c := range in.clients {
		if c.token == "" {
			continue
		}
		switch def {
		case guardianDef:
			in.awardFeat(c, "guardian")
		case bossDef:
			in.awardFeat(c, "king")
			if len(c.recentHits) == 0 {
				in.awardFeat(c, "king_untouched")
			}
		case apexDef:
			in.awardFeat(c, "apex")
		}
	}
}
