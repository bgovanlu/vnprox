// SPDX-License-Identifier: Apache-2.0

// Package microseg is T-1602's microsegmentation planner core: from observed
// flows it computes the minimal firewall policy that preserves observed-good
// traffic ("these N rules cover 30 days of traffic; everything else was
// noise"), and it dry-runs that policy against a flow corpus to report which
// observed-good flows a would-be enforcement would have blocked.
//
// # Safety contract (this package's whole reason to exist)
//
// A microsegmentation policy narrows what a guest may talk to. A false "safe
// to block" verdict is an outage. Three invariants make the planner honest;
// each is provable by a named test, never merely asserted here:
//
//   - Observed-good excludes anomalies. A flow is treated as legitimate only if
//     it appears in the learning window AND was NOT flagged by T-1601's own
//     anomaly detector over that window (Propose runs baseline.Detect against
//     the supplied baseline profile and removes every flagged flow before
//     synthesis). A single transient compromise or misconfiguration inside the
//     window can therefore never legitimize itself into a proposed "allow".
//
//   - Coverage is stated, never rounded up. The minimal-covering-set collapses
//     observed-good flows by (direction, proto, port, peer-subnet) up to a
//     configurable byte-coverage threshold (default 99.5%); the long tail is
//     left uncovered on purpose, and the Proposal reports the exact coverage
//     percentage and uncovered-flow count rather than silently claiming to
//     cover everything.
//
//   - The dry-run cannot silently approximate. DryRun replays each flow against
//     the proposed ruleset using internal/sim's own firewall evaluator
//     (sim.EvaluateFirewall — the SAME rule-walk path simulation uses, never a
//     second, divergent evaluator). A flow the evaluator cannot decide is
//     reported in CannotDetermine ("loud cannot-determine"), never folded into
//     WouldAllow — upholding docs/features/firewall.md §5/§6's "no silent
//     approximation" honesty contract absolutely.
//
// # Never a second mutation path
//
// The planner PROPOSES; a human reviews and applies (T-1603 builds that UX).
// Stage emits ordinary fw.rule.create changeset ops (docs/data-model.md §3's
// existing op vocabulary — no new op type); it never calls change.Service.Apply
// or Confirm. This package imports internal/change only for op-construction
// types, a boundary a regression test enforces (importboundary_test.go).
package microseg
