// SPDX-License-Identifier: Apache-2.0

// Package probe implements T-802's guest-agent live path probe engine:
// running a real, explicit ICMP/TCP probe from a source guest via the QEMU
// guest agent (internal/pve's AgentExec/AgentExecStatus) toward a
// destination, and classifying the observed outcome. It is the "verify
// live" P2 item docs/features/firewall.md §5 names as a documented,
// deliberate follow-up to the (static, configured-state-only) path
// simulator in internal/sim — this package never reasons about firewall
// rules or L2/L3 topology itself, only about one concrete exec attempt's
// result.
//
// Unlike internal/sim, this package is not pure: Run performs real network
// I/O (PVE API calls) via the caller-supplied PVEExecer. It is a diagnostic
// action, never an automatic one and never a network-config mutation — see
// internal/api's POST /simulate/verify handler for the audited entry point.
//
// Honesty contract carried over from internal/sim (docs/features/firewall.md
// §6): a probe that could not run at all (agent unreachable, exec transport
// failure) reports OutcomeError, never a guessed reachable/unreachable — "the
// attempt itself is the answer" when no real attempt could be made.
//
// Needs-hardware-validation flag (see command.go and
// planning/reports/needs-hardware-validation.md): the exact in-guest probe
// command this package sends is unverified against a real QEMU guest agent
// and real guest OS images. It is deliberately narrow (Linux-only ping/nc)
// rather than a guessed "portable" command covering every guest OS/
// toolchain — see command.go's doc comment.
package probe
