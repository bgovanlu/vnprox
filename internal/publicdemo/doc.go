// Package publicdemo implements T-2802's edge for a hosted, read-only
// vnprox demo: `vnproxd --demo --public-demo`.
//
// # Why an edge and not more middleware
//
// T-2801 already makes a demo daemon harmless to any real cluster: its PVE
// transport has no dialer, its host reader is a fixture, and every mutating
// API answers "would have" instead of doing anything. That is the right
// posture for a demo someone runs on their own laptop.
//
// A demo running on the public internet is a different threat model. The
// card's word is "all writes disabled at the edge, not merely in the UI",
// and the acceptance criterion is blunter still: every mutating route
// returns 403. So this package sits in front of the whole daemon handler —
// in front of routing, in front of authentication, in front of T-2801's own
// demo middleware — and refuses.
//
// # The classification is the method, and nothing else
//
// A request is refused iff its method is not GET, HEAD or OPTIONS. There is
// no allowlist of "POSTs that are really reads".
//
// That has a real cost, and it is worth naming rather than discovering: the
// path simulator (POST /simulate/path), the diagnosis ladder (POST
// /diagnose) and the MCP transport are read surfaces with mutating-looking
// methods, and a public demo refuses all three. T-2801 recorded exactly this
// as T-2801-followup-01 and left it open. This package deliberately does not
// resolve it in the permissive direction, because the only thing standing
// behind a semantic allowlist at a public edge is somebody's continued
// correctness about which of ~215 routes are safe — and one wrong entry is a
// stranger writing to the instance. The tour (web/src/tour) therefore routes
// around the simulator; see docs/features/demo-mode.md.
//
// Not even POST /auth/login is exempt. A public demo has no login screen: a
// visitor arrives, and the edge mints a session for them out of band (see
// Edge.ServeHTTP) against the demo fixture's own built-in superuser. The
// session id never reaches the browser, and an inbound session cookie is
// stripped before the request is forwarded, so a visitor cannot present a
// session this edge did not mint.
//
// # Per-visitor state
//
// Because the daemon API is strictly read-only here, there is nowhere for
// per-visitor UI state — where the tour got to, where a node was dragged on
// the map — to live. The daemon's own /layouts routes are refused like
// everything else.
//
// So the edge serves a visitor-scoped scratch surface of its own, under
// [VisitorPathPrefix]. It is deliberately not part of docs/openapi.json: it
// is not the product's API, it stores nothing in the daemon's store, it is
// held in memory, it is keyed by an opaque visitor cookie, and it is
// discarded when the visitor goes idle. One visitor cannot read, write or
// enumerate another's.
//
// # Caps
//
// Every cap in [Caps] is per-visitor except [Caps.MaxVisitors], and that is
// the design requirement rather than an accident: "exceeding a cap degrades
// that session only". A visitor who floods gets 429s while every other
// visitor is served; a visitor who stuffs the scratch store gets 413s while
// every other visitor's state is untouched. The single global cap is the
// visitor count, and it is enforced by refusing *new* visitors (after
// evicting idle ones) rather than by evicting active ones, so a flood of new
// arrivals cannot take the demo away from the people already using it.
//
// # What this package does not do
//
// It does not host anything. There is no public instance of vnprox, no
// domain, no object storage and no deploy pipeline for this repository, so
// the card's first bullet — "a public instance serving demo mode" — is not
// met, and is recorded as a gap in docs/features/demo-mode.md. Everything
// that instance would need in order to be safe is here and tested; the
// instance is not.
package publicdemo
