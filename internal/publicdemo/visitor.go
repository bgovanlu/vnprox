package publicdemo

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// VisitorCookieName is the opaque per-visitor identifier the edge sets.
//
// It is the ONLY credential a public-demo visitor's browser holds. The
// daemon session the edge mints on their behalf never leaves the server
// (see Edge.ServeHTTP), so a stolen or forged value buys an attacker a
// fresh read-only visitor and nothing else.
const VisitorCookieName = "vnprox_demo_visitor"

// visitor is one browser's slice of a public demo: its own daemon session,
// its own request budget, and its own scratch state.
type visitor struct {
	// state is this visitor's scratch store. json.RawMessage rather than a
	// decoded value: the edge is a transport for opaque frontend state and
	// has no business knowing its shape, exactly as internal/api's
	// /layouts routes treat a layout payload.
	state map[string]json.RawMessage
	id    string
	// lastSeen is both the idle-eviction clock and the token bucket's
	// refill mark; there is only one "when did this visitor last do
	// something".
	lastSeen time.Time
	// sessionCookies are the Set-Cookie values the daemon's own login
	// handler produced for this visitor, replayed onto every forwarded
	// request. Empty until the first mint.
	//
	// Field order in this struct is govet's fieldalignment, not taste.
	sessionCookies []*http.Cookie
	tokens         float64
	stateBytes     int
	// mu guards everything above. Per-visitor rather than one registry-wide
	// lock so a slow mint (which round-trips through the login handler)
	// blocks only the visitor waiting on it.
	mu sync.Mutex
}

// allow consumes one request token, refilling first. False means this
// visitor has exceeded its own request cap; no other visitor is affected.
func (v *visitor) allow(now time.Time, caps Caps) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	if elapsed := now.Sub(v.lastSeen); elapsed > 0 {
		v.tokens += elapsed.Seconds() / caps.RequestRefill.Seconds()
		if v.tokens > float64(caps.RequestBurst) {
			v.tokens = float64(caps.RequestBurst)
		}
	}
	v.lastSeen = now
	if v.tokens < 1 {
		return false
	}
	v.tokens--
	return true
}

// readState returns a copy of one scratch key, and whether it was set.
func (v *visitor) readState(name string) (json.RawMessage, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	raw, ok := v.state[name]
	if !ok {
		return nil, false
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out, true
}

// errStateCapExceeded is returned by writeState when a PUT would take this
// visitor past one of its own scratch caps. The write is not partially
// applied: the visitor's existing state is exactly what it was.
var errStateCapExceeded = fmt.Errorf("visitor scratch state cap exceeded")

func (v *visitor) writeState(name string, value json.RawMessage, caps Caps) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	previous, existed := v.state[name]
	if !existed && len(v.state) >= caps.MaxStateEntries {
		return fmt.Errorf("%w: %d keys is the limit", errStateCapExceeded, caps.MaxStateEntries)
	}
	next := v.stateBytes - len(previous) + len(value)
	if next > caps.MaxStateBytes {
		return fmt.Errorf("%w: %d bytes is the limit", errStateCapExceeded, caps.MaxStateBytes)
	}
	stored := make(json.RawMessage, len(value))
	copy(stored, value)
	v.state[name] = stored
	v.stateBytes = next
	return nil
}

// sweepInterval bounds how often the registry walks itself looking for idle
// visitors. Coarse on purpose: eviction is housekeeping, and a walk on every
// request would make each one O(visitors).
const sweepInterval = time.Minute

// registry holds every live visitor. Its own lock covers the map only;
// per-visitor work happens under the visitor's lock.
type registry struct {
	byID      map[string]*visitor
	lastSweep time.Time
	caps      Caps
	mu        sync.Mutex
}

func newRegistry(caps Caps) *registry {
	return &registry{byID: make(map[string]*visitor), caps: caps}
}

// errAtCapacity is returned by lookupOrCreate when the instance is holding
// as many visitors as it may and none is idle enough to evict.
var errAtCapacity = fmt.Errorf("public demo is at visitor capacity")

// lookupOrCreate resolves the visitor for an inbound cookie value, creating
// one if the cookie is absent, unrecognised (an expired visitor, or a
// forged value), or the instance was restarted. The bool reports whether a
// new visitor was created, which is what tells the caller to set a cookie.
func (r *registry) lookupOrCreate(id string, now time.Time) (*visitor, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Swept on a cadence rather than only under pressure, because the idle
	// TTL is doing a second job: the daemon session the edge minted for a
	// visitor has its own idle timeout (auth.DefaultIdleTimeout, 2h), and a
	// visitor kept alive past it would be forwarded with a session the
	// daemon has forgotten — a 401 on every read, with no login screen to
	// recover through. Discarding the visitor first turns that into a fresh
	// visitor with a fresh session, which is the behaviour a returning
	// stranger expects anyway. VisitorIdleTTL must therefore stay well under
	// the session idle timeout; the defaults are 30m against 2h.
	if now.Sub(r.lastSweep) >= sweepInterval {
		r.evictIdleLocked(now)
		r.lastSweep = now
	}

	if id != "" {
		if v, ok := r.byID[id]; ok {
			return v, false, nil
		}
	}

	if len(r.byID) >= r.caps.MaxVisitors {
		r.evictIdleLocked(now)
	}
	if len(r.byID) >= r.caps.MaxVisitors {
		// Deliberately refuses the ARRIVING visitor rather than evicting an
		// established one: a flood of new arrivals must not be able to take
		// the demo away from the people already in it.
		return nil, false, errAtCapacity
	}

	newID, err := newVisitorID()
	if err != nil {
		return nil, false, err
	}
	v := &visitor{
		id:       newID,
		tokens:   float64(r.caps.RequestBurst),
		lastSeen: now,
		state:    make(map[string]json.RawMessage),
	}
	r.byID[newID] = v
	return v, true, nil
}

// evictIdleLocked drops every visitor untouched for longer than the idle
// TTL. Called on the sweep cadence above, and again when the registry is
// full — the second call is not redundant: an arrival can find the registry
// full within the same minute a sweep already ran.
func (r *registry) evictIdleLocked(now time.Time) {
	for id, v := range r.byID {
		v.mu.Lock()
		idle := now.Sub(v.lastSeen)
		v.mu.Unlock()
		if idle >= r.caps.VisitorIdleTTL {
			delete(r.byID, id)
		}
	}
}

// count reports how many visitors are tracked. Test and log support only.
func (r *registry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// newVisitorID mints 128 bits of randomness, hex-encoded. Opaque and
// unguessable: it is the key another visitor's scratch state would be read
// with if it were guessable.
func newVisitorID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating a public-demo visitor id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
