package publicdemo

import "time"

// Caps are the resource limits a public demo enforces (T-2802 AC4).
//
// Every field but MaxVisitors is per-visitor. Exceeding one of those
// degrades the visitor who exceeded it and nobody else — which is the whole
// assertion the criterion is making, and the reason there is no global
// request budget here. A global budget would let one hostile visitor spend
// the instance's whole allowance and take the demo down for everyone, which
// is precisely the failure the caps exist to prevent.
type Caps struct {
	// RequestRefill is how often one request token is added back to a
	// visitor's bucket, so steady-state throughput is 1/RequestRefill
	// requests per second.
	RequestRefill time.Duration
	// VisitorIdleTTL is how long a visitor may go untouched before the
	// registry may evict it to make room for a new arrival. An evicted
	// visitor loses its scratch state and gets a fresh session on its next
	// request; nothing else about the instance changes.
	VisitorIdleTTL time.Duration
	// MaxVisitors is the only global cap: how many visitors may be tracked
	// at once. See the package doc for why exceeding it refuses new
	// arrivals rather than evicting established ones.
	MaxVisitors int
	// RequestBurst is one visitor's token-bucket capacity: how many
	// requests they may make back to back before RequestRefill governs.
	RequestBurst int
	// MaxStateBytes is the total size of one visitor's scratch state,
	// summed over every key. A PUT that would exceed it is refused with
	// 413 and changes nothing.
	MaxStateBytes int
	// MaxStateEntries is how many distinct scratch keys one visitor may
	// hold. Overwriting an existing key never counts against it.
	MaxStateEntries int
}

// DefaultCaps are the shipped limits.
//
// Sized for a browser doing a guided tour, not for an API client: the SPA's
// initial load is on the order of 30 requests, and the tour adds a handful
// per step, so a 120-request burst refilling twice a second leaves an
// ordinary visitor far from the ceiling while a scripted flood hits it in
// under a second. The state caps are sized for what the tour and the
// topology layout actually store (a few KB each), with an order of
// magnitude of headroom.
func DefaultCaps() Caps {
	return Caps{
		MaxVisitors:     200,
		RequestBurst:    120,
		RequestRefill:   500 * time.Millisecond,
		MaxStateBytes:   256 * 1024,
		MaxStateEntries: 32,
		VisitorIdleTTL:  30 * time.Minute,
	}
}

// withDefaults fills zero fields from DefaultCaps, so a caller may set one
// limit without restating the rest.
func (c Caps) withDefaults() Caps {
	d := DefaultCaps()
	if c.MaxVisitors <= 0 {
		c.MaxVisitors = d.MaxVisitors
	}
	if c.RequestBurst <= 0 {
		c.RequestBurst = d.RequestBurst
	}
	if c.RequestRefill <= 0 {
		c.RequestRefill = d.RequestRefill
	}
	if c.MaxStateBytes <= 0 {
		c.MaxStateBytes = d.MaxStateBytes
	}
	if c.MaxStateEntries <= 0 {
		c.MaxStateEntries = d.MaxStateEntries
	}
	if c.VisitorIdleTTL <= 0 {
		c.VisitorIdleTTL = d.VisitorIdleTTL
	}
	return c
}
