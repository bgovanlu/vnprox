// SPDX-License-Identifier: Apache-2.0

// Package tcmirror implements T-4014's SPAN/mirror session rendering: a
// pure renderer from a Session (source interface, destination interface,
// optional declared bandwidth) to the ordered `tc` command-line invocations
// that realize it as a Linux clsact qdisc + matchall filter pair with a
// `mirred` "copy" action — mirroring internal/qos's exact "service package
// the change engine's executor calls into" shape (see internal/qos/doc.go):
// this package itself never execs anything or touches store state, it only
// computes what the executor's on-node gateway (cmd/vnproxd's
// hostTcMirrorGateway) should run.
//
// # Divergence from internal/capture, deliberately
//
// A packet CAPTURE session (internal/capture, T-1301) is a direct,
// capability-gated action — there is no changeset op for it, by design
// (auth.CapCapture alone gates it, confirmed absent from
// internal/change/op.go at the time phase-40 was scoped). A mirror SESSION
// is the opposite shape on purpose: it is a standing config change to a
// node's `tc` rules — the equivalent of adding a bridge port or a QoS
// shape, not a bounded, one-shot diagnostic run — so it is an ordinary
// changeset op (tc.mirror.create/update/delete, internal/change/op.go),
// flowing through the full stage->validate->diff->apply->confirm/rollback
// lifecycle like every other mutation (CLAUDE.md: "Never apply network
// changes outside the change engine"). What IS reused from capture,
// verbatim, is the *bounding and audit discipline*: a hard, server-enforced
// maximum duration that the session cannot outlive, and an audit row for
// every start/stop. See internal/change/tcmirror_expiry.go's doc comment
// for exactly how a mirror session's own expiry is enforced without
// inventing a second mutation path.
//
// # Observed on pvecube (PVE 9.2.4, kernel 7.0.14-4-pve, iproute2-6.15.0)
//
// planning/reports/evidence/pve-9.2.4-tc-mirred-2026-08-28.txt is a
// read-only transcript of `tc qdisc show`, `tc filter show`, `tc -j qdisc
// show`, and `modinfo act_mirred`/`cls_matchall`/`sch_ingress` against
// pvecube, taken before any code here was written (CLAUDE.md: "observe
// before you model"). Findings that shaped this package:
//
//   - No clsact/ingress qdisc exists anywhere on pvecube today — every
//     physical NIC carries the distro-default `mq` root with per-queue
//     `fq_codel`, bridges/veths/fwbr/fwpr/fwln pairs carry `noqueue`, and
//     the two guest taps carry `fq_codel`. RenderTC's `tc qdisc add ...
//     clsact` therefore never contends with an existing root qdisc: clsact
//     attaches at the ingress/egress hook, a different attachment point
//     from whatever root qdisc (mq/noqueue/fq_codel) already governs the
//     interface's normal forwarding path — confirmed empirically, not
//     assumed from documentation.
//   - `tc -j qdisc show` produces valid, well-formed JSON on this kernel/
//     iproute2 pairing (`tc -j filter show` likewise, `[]` on an
//     interface with no filters) — a future inspector seam could parse
//     `tc -j` output directly rather than screen-scraping `tc`'s text
//     form, though this package does not need one (see below).
//   - `act_mirred`, `cls_matchall`, and `sch_ingress` (which provides the
//     clsact qdisc kind) are all present as loadable kernel modules
//     (modinfo succeeds) but none are loaded (`lsmod` shows none of them,
//     `tc actions ls action mirred` returns empty) — the kernel is a clean
//     slate for this feature, and the first `tc qdisc add ... clsact` on
//     any given node will load sch_ingress on demand, standard Linux
//     module-autoload behavior.
//
// # What this package deliberately does NOT do
//
// RenderTC renders a mirror in one direction pair only (both the ingress
// and egress hook of the source's own clsact qdisc, so a SPAN of a bridge
// port sees both directions of that port's traffic) copied into the
// destination via `action mirred egress mirror dev <dest>` — the
// conventional recipe for "inject the copy as if it arrived on dest",
// which is what a sniffer listening on dest's RX expects. It does NOT
// render any bandwidth-limiting action on the mirrored copy: MaxMbit
// (internal/change's TcMirrorCreateParams) is a DECLARED, validated ceiling
// (internal/change/validate_safety.go's cap check), not a kernel-enforced
// `police` action — chaining a `police ... drop` action onto the SOURCE's
// clsact filter would drop the ORIGINAL packet too (clsact's ingress/egress
// hooks sit in the real forwarding path, not a monitoring-only tap), which
// would make an over-budget mirror session silently degrade production
// traffic — exactly the kind of danger CLAUDE.md's safety-interlock rule
// exists to prevent. Correctly rate-limiting only the mirrored COPY would
// need a second policer on the destination's own ingress, which needs
// hardware-timed validation of its own; it is left as documented future
// work rather than shipped unverified.
//
// NEEDS HARDWARE VALIDATION (in the "no destructive action, no real NIC,
// no second node" sense — a disposable nested lab per docs/development.md
// suffices, this is not one of CLAUDE.md's three needs-hardware-validation
// buckets): RenderTC's argv lines are exercised only by this package's own
// golden tests (fixed input -> fixed argv) against the evidence above,
// never against a real kernel tc/clsact/mirred stack — the read-only
// constraint on this task card forbids running `tc qdisc add`/`tc filter
// add` against pvecube. See cmd/vnproxd's hostTcMirrorGateway doc comment
// for the exec path this feeds.
package tcmirror
