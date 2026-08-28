// SPDX-License-Identifier: Apache-2.0

// Package fw is the pure, I/O-free firewall resolution engine (T-501):
// given a snapshot of the three pve-firewall scopes (cluster/node/guest)
// plus cluster-scope security groups, it computes
//
//   - the effective, ordered evaluation a guest's traffic is actually
//     subject to (Resolve), with each rule labeled by origin (cluster,
//     security group, guest, or the fallthrough default policy) exactly
//     as docs/features/firewall.md §1 documents it: "cluster rules →
//     security groups → guest rules → default policies";
//   - the enablement banners the UI must show whenever a scope's own
//     firewall toggle — or an ancestor scope's — means none of a
//     ruleset's rules are actually active (ScopeBanners), covering the
//     "Datacenter firewall is OFF" footgun docs/features/firewall.md §2
//     calls out by name;
//   - "referenced by N rules" usage counts for every alias/ipset/security
//     group (UsageCounts); and
//   - expansion previews for pve-firewall's built-in service macros
//     (MacroExpansion).
//
// This package is deliberately pure: it operates only on the Snapshot
// value passed in (assembled by a caller — internal/api — from
// internal/inventory's live graph) and does no I/O of its own. It is
// exactly the substrate T-503's path simulator is specified to reuse
// (planning/tasks/phase-5.md's T-501 card), so its purity is enforced by
// .golangci.yml's depguard rule scoped to this package, not just by
// convention.
package fw
