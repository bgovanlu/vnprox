// SPDX-License-Identifier: Apache-2.0

// Package validation implements the machinery T-1801 built for Phase 18's
// hardware-validation arc (docs/roadmap-proven.md D5/D7): the evidence-blob
// schema every planning/validation/harness/<section>.sh script emits, and
// the triage logic that compares a returned blob against a section's
// declared expected outcomes (planning/validation/expected/<section>.md).
//
// The harness scripts themselves are POSIX-ish bash (planning/validation/
// harness/), deliberately not Go, so they run unmodified on a stock PVE
// node over SSH with nothing but bash/curl/coreutils. This package is the
// Go-side counterpart: it defines the wire shape those scripts print
// (Blob), validates that shape (Validate), and turns a blob plus an
// expected-outcome table into a triage verdict per item (Triage) — the
// step T-1802/T-1804/T-1808 run by hand against real evidence.
//
// See planning/validation/README.md for the human-facing runbook and
// planning/reports/T-1801.md for the design rationale.
package validation
