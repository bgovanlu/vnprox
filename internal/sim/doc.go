// Package sim implements the path simulator (T-503): a pure, static
// reachability + firewall engine answering docs/features/firewall.md §5's
// question — "Can A reach B on proto/port X, and if not, what stops it?" —
// over a configured-state inventory snapshot.
//
// Honesty contract (docs/features/firewall.md §6, binding): every PVE
// firewall / SDN feature the engine touches is either evaluated correctly
// or reported as a "not evaluated: <feature>" caveat. The engine never
// silently approximates. When it cannot fully evaluate a path — an unknown
// entity kind, an unresolvable firewall reference, or a firewall decision
// that would depend on an IP the snapshot does not carry — it returns
// VerdictIndeterminate with a blocker-severity caveat rather than a
// confident allow/deny/unreachable (AC5: "never a confident verdict").
//
// Purity: this package imports no I/O (no net/http, os, io, database/sql,
// nor any internal package that performs I/O). It takes an inventory
// Snapshot (and an optional plain guest-IP side-table) as arguments. The
// .golangci.yml `sim-purity` depguard rule enforces this, mirroring
// internal/fw's `fw-purity` rule.
//
// The engine uses internal/fw.Resolve as its firewall-evaluation substrate
// (not a parallel reimplementation) so its firewall verdicts are, by
// construction, consistent with the T-501 resolved view the rest of the
// product renders (AC2's differential property).
package sim
