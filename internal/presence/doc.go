// Package presence implements T-2805: advisory entity locks on staged
// drafts, and per-changeset/per-entity presence over the existing /api/ws
// event stream.
//
// # The one invariant
//
// A lock here is ADVISORY. It warns a second operator that someone else has
// a draft open against the same entity, names who, and lets them proceed
// anyway with the override recorded. It never refuses anything. In T-2805's
// own words: "a lock never prevents an emergency change; it prevents an
// accidental one."
//
// That is enforced structurally rather than by convention. This package is
// consumed only by internal/api's staging handlers and its two read routes;
// internal/change — the change engine that owns stage → validate → diff →
// apply → confirm/rollback — has no reference to it at all, so no code path
// that applies a changeset can consult a lock even by accident.
// TestChangeEngineDoesNotImportPresence (deps_test.go) asserts exactly that
// over the real package source, so the day someone wires a lock check into
// the apply path, the build says so.
//
// # Locks
//
// A lock is one row of `entity_locks` (internal/store, migration 0044),
// keyed by the entity Ref — one holder per entity, as a PRIMARY KEY rather
// than as application logic. It carries the holder's username, the session
// that took it, the draft it was taken for, and an expiry.
//
// Three things free a lock, and only one of them is a request the operator
// made:
//
//   - Expiry. Enforced at READ time against this service's injected clock, so
//     a stopped daemon cannot leave a lock standing (the sweep that deletes
//     expired rows keeps the table bounded, it is never the correctness
//     argument).
//   - Session end. A dropped WebSocket connection releases every lock its
//     session holds once that session has no live connections left — the
//     closed laptop, which is the failure mode a release endpoint would
//     never cover.
//   - Discarding the draft the lock was taken for.
//
// # Presence
//
// Presence is who is currently LOOKING at a changeset or an entity. It is
// derived entirely from live WebSocket connections and is deliberately never
// persisted: a presence record that outlives its connection is a lie, and a
// daemon restart must not resurrect one.
//
// It rides the existing event stream rather than adding a second push
// channel. A client subscribes to the topic `presence:<scope>` — the same
// parameterised-topic shape `metrics:<ref>` already uses — where scope is
// `changeset:<id>` or `entity:<ref>`; the hub reports subscription changes
// here, and this service broadcasts `presence.changed` on that topic.
//
// The event carries a COUNT and no identities, exactly like the
// `drift.changed`/`findings.changed` events beside it. That is what makes
// T-2805 AC5 ("presence does not leak identities to a caller lacking the
// capability to see them") a structural property of the WS surface rather
// than a per-subscriber filter the hub has no way to apply: the names live
// only in GET /presence, which is capability-gated.
package presence
