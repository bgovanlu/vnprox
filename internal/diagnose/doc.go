// Package diagnose implements T-1307's guided diagnosis ladder: a generic,
// dependency-light orchestrator that runs a registered sequence of steps
// against one target and folds their outcomes into one stable,
// machine-consumable Result.
//
// # Composes, does not reimplement
//
// This package owns none of the actual diagnostic logic — no simulation,
// no probing, no guest-agent reads, no conntrack parsing, no packet
// capture. Every one of T-1307's five canonical steps (config check, live
// probe, guest interior, conntrack, capture) is a thin StepFunc closure
// built in internal/api/diagnose.go, composing this phase's already-shipped
// surfaces directly: internal/sim.Simulate (T-503), internal/probe.Run
// (T-802), internal/guestinterior (T-1304), internal/host.Reader.Conntrack
// (T-1305), and internal/capture.Coordinator (T-1301). Those step
// closures live in internal/api specifically so they can reuse that
// package's own private target-resolution helpers (resolveQemuGuestNicOwner,
// fetchQEMUInterior/fetchLXCInterior, fetchClusterConntrack, ...) rather
// than duplicating them here — this package stays a pure sequencer with no
// knowledge of inventory Refs, PVE clients, or HTTP.
//
// # Advisory only
//
// Every Ladder run produces a Verdict, never an action: SuggestedFixRef
// (when set) always names an EXISTING fixable finding's own
// POST /findings/{id}/fix link (docs/api.md's Findings section) — this
// package never computes a fix itself and never applies one. A capture
// escalation is the one step capable of a real side effect (starting a
// capture session), and it only runs when the caller explicitly opts in
// (Request.EscalateToCapture) — see internal/api/diagnose.go's capture
// step for the capability check that gates it further.
//
// # Registration table, not a hardcoded sequence
//
// Ladder is built from a []Step slice, run in the given order. A future
// card (e.g. Phase 14's WireGuard/edge diagnostics) extends the ladder by
// appending a Step at its own construction call site — this package's own
// code never needs to change for that, per T-1307's card.
//
// # Stable contract
//
// Result/StepResult/Verdict's JSON shape (docs/api.md's Diagnosis section)
// is the scaffolding T-1701's MCP AI operator drives next arc — field
// names and the StepStatus vocabulary are a contract, not an internal
// detail; see ladder_test.go's schema/golden test.
package diagnose
