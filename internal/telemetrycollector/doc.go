// Package telemetrycollector implements the receiving half of T-2503's
// opt-in compatibility report (T-3710).
//
// internal/telemetry is the shipped, tested client: it builds a Payload,
// guards it, and — only when an operator has opted in — POSTs the exact
// bytes it just showed that operator via `telemetry preview`. This package
// is what receives those bytes, and its whole job is to keep every promise
// that client already makes rather than to make new ones:
//
//   - **It re-guards.** The client's Guard already ran before the request
//     left the operator's machine, but this package does not trust that —
//     a collector that only checks the client's homework is one bad client
//     release away from silently accepting anything. Every submission is
//     re-validated with the *same* internal/telemetry.Guard the client
//     uses (the closed field allowlist, the shape rules, the explicit
//     payloadVersion check) before a single byte reaches the store.
//   - **It stores only what the payload carries, plus one field the
//     payload deliberately omits: a receipt timestamp.** payload.go's
//     package doc says why the client sends none of its own ("a local
//     clock is a fingerprint") and that "the collector's receipt time is
//     enough" — this package is that collector, and ReceivedAt is that
//     time.
//   - **It never reads the request's source IP.** See doc/security.md's
//     collector section for the reasoning: the payload structurally omits
//     every identifier, and per-submitter rate limiting is keyed on
//     InstallID — the one correlator the payload already carries — rather
//     than on an identifier this package would otherwise be introducing
//     for the first time. A separate, IP-free global rate limit bounds
//     total throughput as defense in depth against a flood of distinct
//     (but validly-shaped) install ids.
//   - **Retention actually deletes**, on a configurable window, and
//     **revocation actually deletes**, keyed on the one correlator the
//     operator controls (InstallID, resettable client-side any time with
//     `vnproxctl telemetry reset-id`). Neither is a documented intention;
//     both are exercised by this package's tests and by
//     planning/reports/evidence/t-3710-collector-e2e.txt.
//
// This package has no knowledge of vnproxd, internal/store, or any other
// node-local state — it is the one genuinely dynamic piece of
// infrastructure T-3707's hosting decision requires (everything else is a
// static, GitHub-served index), and it is deliberately small and
// self-contained: one table, one binary (cmd/vnproxtelemetryd), no shared
// database with anything else vnprox runs.
package telemetrycollector
