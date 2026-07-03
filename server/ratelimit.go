// Rate limiting — the cheap end of hardening for a public door. Two
// shapes: a token bucket per connection (readLoop drops command floods
// instead of relaying them to the tick), and a per-source window on name
// claims (each claim writes the identity store to disk forever).
package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Command budget per connection: the busiest legitimate client (WASD at
// 10Hz, acks at 10Hz, click spam) stays well under half of this.
const (
	cmdRatePerSec = 60
	cmdBurst      = 120
)

// tokenBucket is a classic leaky budget; not safe for concurrent use (each
// connection's readLoop owns its own).
type tokenBucket struct {
	tokens float64
	max    float64
	rate   float64 // tokens per second
	last   time.Time
}

func newCmdBucket() *tokenBucket {
	return &tokenBucket{tokens: cmdBurst, max: cmdBurst, rate: cmdRatePerSec, last: time.Now()}
}

// allow refills by elapsed time and spends one token; false = over budget.
func (b *tokenBucket) allow(now time.Time) bool {
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	b.last = now
	if b.tokens > b.max {
		b.tokens = b.max
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// claimLimiter caps how often one source can mint identities: claimMax
// claims per claimWindow, keyed by forwarded-for (the funnel sets it) or
// the socket address. Misses answer 429.
const (
	claimMax    = 3
	claimWindow = time.Minute
	// claimKeysMax bounds the tracking map; blowing past it means an
	// address-rotating flood, and forgetting everyone (briefly opening the
	// window) beats growing without bound.
	claimKeysMax = 10000
)

type claimLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newClaimLimiter() *claimLimiter {
	return &claimLimiter{hits: map[string][]time.Time{}}
}

func (cl *claimLimiter) allow(key string, now time.Time) bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if len(cl.hits) > claimKeysMax {
		cl.hits = map[string][]time.Time{}
	}
	fresh := cl.hits[key][:0]
	for _, t := range cl.hits[key] {
		if now.Sub(t) < claimWindow {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= claimMax {
		cl.hits[key] = fresh
		return false
	}
	cl.hits[key] = append(fresh, now)
	return true
}

// sourceKey names a request's origin for rate limiting.
func sourceKey(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
