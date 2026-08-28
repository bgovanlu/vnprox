// SPDX-License-Identifier: Apache-2.0

// Package qos implements T-1505's bridge-level per-service traffic shaping:
// a pure renderer from a named Shape (bridge, optional match CIDR/VLAN,
// rate/ceil/priority) to the ordered `tc` command-line invocations that
// realize it as a Linux HTB (Hierarchical Token Bucket) qdisc/class/filter
// on that bridge — mirroring internal/sdn and internal/fw's "service
// package the change engine's executor calls into" shape: this package
// itself never execs anything or touches store state, it only computes what
// the executor's on-node gateway (cmd/vnproxd's hostQosGateway) should run.
//
// Per-guest-NIC rate limiting is deliberately NOT part of this package: it
// already exists as PVE's own `rate` knob, wired through the ordinary
// guest.nic.update op's RateMbps field (docs/data-model.md §3) — this
// package's whole surface is the genuinely new one, bridge-level per-service
// shaping, applied through the ordinary change-engine lifecycle exactly like
// every other mutation (CLAUDE.md: "Never apply network changes outside the
// change engine").
//
// NEEDS HARDWARE VALIDATION: RenderTC's argv lines are exercised only by
// this package's own golden tests (fixed input -> fixed argv), never against
// a real kernel `tc`/HTB stack — see cmd/vnproxd/qos.go's hostQosGateway doc
// comment for the exec path this feeds.
package qos
