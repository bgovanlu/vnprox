// chains.go correlates T-1403's edge/NAT port-forward model with this
// package's freshly discovered ProxyState list into T-1406's own exit-demo
// shape: WAN -> port-forward -> proxy guest -> backend guest, drawn as one
// connected chain whenever a port-forward's target IP matches a configured
// ingress_targets row's own address, and that target's discovered backends
// resolve to a known guest. Pure projection, no I/O — mirrors
// internal/edge.ProjectNAT's own "operates only on already-fetched state"
// shape; internal/api/ingress.go is the thin adapter that gathers the
// inputs (edge.ProjectNAT's port-forward list, this package's Discover
// results, an IPAM-based guest lookup) and calls ProjectChains.

package ingress

import "strings"

// GuestLookup resolves a bare IP to a known guest ref and its powered-off
// state. Structurally identical to internal/edge.GuestLookup (same
// signature) but declared as this package's own named type rather than
// importing internal/edge, so this package stays a leaf with no dependency
// on the edge/NAT projection it correlates against — internal/api, which
// already depends on both, does the one-line conversion at the call site.
// A nil GuestLookup disables backend->guest correlation entirely (every
// backend's GuestRef is simply left unresolved), the same optional-
// dependency convention edge.GuestLookup itself documents.
type GuestLookup func(ip string) (ref string, poweredOff bool, ok bool)

// PortForwardRef is the minimal shape ProjectChains needs from one of
// internal/edge's own PortForward rows — decoupled from that package's
// concrete type for the same leaf-package reason GuestLookup is.
type PortForwardRef struct {
	ID             string
	Node           string
	Proto          string
	IntIP          string
	TargetGuestRef string
	ExtPort        int
	IntPort        int
}

// TargetChainInput pairs one ingress_targets row's own configured address
// with its freshly discovered ProxyState (the result of calling Discover
// against it).
type TargetChainInput struct {
	TargetID string
	Address  string
	State    ProxyState
}

// ChainBackend is one backend server in a Chain's own backend list, with
// guest correlation applied.
type ChainBackend struct {
	Address  string `json:"address"`
	GuestRef string `json:"guestRef,omitempty"`
	Healthy  bool   `json:"healthy"`
}

// Chain is one WAN -> port-forward -> proxy guest -> backend guest path —
// GET /ingress/status's own correlated view, and this card's exit-demo
// shape (docs/roadmap-universal.md's phase-14 intro: "the Edge layer
// showing exactly which ports the lab exposes to the internet").
type Chain struct {
	PortForwardID string         `json:"portForwardId"`
	Node          string         `json:"node"`
	Proto         string         `json:"proto"`
	ProxyGuestRef string         `json:"proxyGuestRef,omitempty"`
	TargetID      string         `json:"targetId"`
	TargetKind    Kind           `json:"targetKind"`
	Backends      []ChainBackend `json:"backends"`
	ExtPort       int            `json:"extPort"`
}

// ProjectChains matches every port-forward whose IntIP equals a
// TargetChainInput's own host (its ingress_targets.address, scheme/port
// stripped) into one Chain per match, resolving each matched target's
// discovered backends to a guest ref via lookup (nil-safe: every backend
// then simply reports no GuestRef rather than guessing). A port-forward
// with no matching ingress_targets row produces no Chain — this function
// only ever draws a chain where the operator has both a port-forward *and*
// an ingress_targets entry that line up, never a guessed/inferred one.
// Deterministic ordering (port-forward input order), matching every other
// projection in this codebase.
func ProjectChains(portForwards []PortForwardRef, targets []TargetChainInput, lookup GuestLookup) []Chain {
	byHost := make(map[string][]TargetChainInput, len(targets))
	for _, t := range targets {
		h := HostOnly(t.Address)
		byHost[h] = append(byHost[h], t)
	}

	var out []Chain
	for _, pf := range portForwards {
		matches, ok := byHost[pf.IntIP]
		if !ok {
			continue
		}
		for _, t := range matches {
			c := Chain{
				PortForwardID: pf.ID, Node: pf.Node, Proto: pf.Proto, ExtPort: pf.ExtPort,
				ProxyGuestRef: pf.TargetGuestRef, TargetID: t.TargetID, TargetKind: t.State.Kind,
			}
			for _, b := range t.State.Backends {
				cb := ChainBackend{Address: b.Address, Healthy: b.Healthy}
				if lookup != nil {
					if ref, _, ok := lookup(HostOnly(b.Address)); ok {
						cb.GuestRef = ref
					}
				}
				c.Backends = append(c.Backends, cb)
			}
			out = append(out, c)
		}
	}
	return out
}

// HostOnly strips an optional "scheme://" prefix and any trailing
// ":port"/path from addr, returning just the bare host/IP — used to
// compare a port-forward's IntIP (always a bare IP) against an
// ingress_targets.address (typically a full base URL like
// "http://10.0.0.5:8404") or a discovered backend's own "host:port"
// address uniformly.
func HostOnly(addr string) string {
	s := addr
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		// Guard against stripping a port out of a bare IPv6 literal
		// (contains multiple ':'), which this codebase's target addresses
		// never use (IPv4-only fixtures throughout) — a plain host:port
		// split is correct for every real case this package handles.
		if strings.Count(s, ":") == 1 {
			s = s[:i]
		}
	}
	return s
}
