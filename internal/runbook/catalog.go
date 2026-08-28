// SPDX-License-Identifier: Apache-2.0

// catalog.go is the built-in runbook library — the data half of this
// package (render.go is the fixed engine that interprets it).
//
// SOURCING, stated per CLAUDE.md's rule against inventing plausible-looking
// behavior: these three are the checks in internal/findings whose own doc
// comments name a concrete, single, structural remediation a human would
// actually take, found by reading every health_*.go file's doc comment
// (2026-08-28):
//
//   - orphan_vnet (health_orphanvnet.go): "the fix is 'create the missing
//     zone' or 'delete the orphaned vnet'" — this runbook takes the second,
//     simpler option; the first is a legitimate alternative this package
//     does not yet offer (see renderDeleteOrphanVnet's doc comment).
//   - fw_rule_unused (health_fwruleunused.go): "deleting a rule is a
//     judgment call, not a computable patch" — exactly the case a runbook
//     is for: propose the judgment call as a draft, stop, let a human
//     decide.
//   - trunk_unused_vlans (health_trunkvlans.go): "you might be able to tidy
//     this up" — narrowing the trunk to VIDs actually in use.
//
// REJECTED, and why (every other check with a health_*.go file, read the
// same pass): bond_slave_down and orphan_vnet's own comment both say
// outright there is no computable fix (a down NIC needs recabling; the
// zone-vs-vnet choice needs a human's intent) — a runbook cannot propose
// "guess correctly". mgmt_single_path needs a physical topology decision
// (which second uplink) this package has no way to discover. cert_* /
// peer_untrusted / peer_unreachable / gitsync_* / rogue_* are all
// explicitly detection-only in their own doc comments (certs: "vnprox does
// not renew or reissue"; peer trust: "nothing here is fixable"; rogue:
// "never a mitigation path" — a security signal, not a remediation
// target). corosync_link_degraded and ha_replication_degraded describe
// cluster infrastructure health with no single well-known changeset fix
// (the remediation is operational: investigate the ring/replication link
// itself). service_down's remedy is already RemedyOperational (restart a
// unit), not a network-config change — a different, already-shipped tier
// (Finding.Remedy), and a second path to the same destination would be
// exactly the kind of redundant mutation surface CLAUDE.md forbids. That
// is three sourced, two more considered-and-explicitly-excluded-in-code,
// and a wider sweep explicitly rejected as out of scope for a first pass.

package runbook

// Built-in runbook names.
const (
	DeleteOrphanVnet    = "delete-orphan-vnet"
	DeleteUnusedFwRule  = "delete-unused-fw-rule"
	TrimUnusedTrunkVids = "trim-unused-trunk-vids"
)

// Runbooks returns the built-in library, freshly built each call (mirroring
// internal/blueprint.Starters' identical convention) so a caller mutating
// one returned value can never corrupt another caller's copy.
func Runbooks() []Runbook {
	return []Runbook{
		deleteOrphanVnetRunbook(),
		deleteUnusedFwRuleRunbook(),
		trimUnusedTrunkVidsRunbook(),
	}
}

// ByName returns one built-in runbook by its own Name, or ok=false.
func ByName(name string) (Runbook, bool) {
	for _, rb := range Runbooks() {
		if rb.Name == name {
			return rb, true
		}
	}
	return Runbook{}, false
}

// ForCheck returns every built-in runbook attached to checkName — "so a
// finding can offer its runbook" (the task card's own phrase). Today every
// check has at most one; the return type is a slice because CheckName-to-
// Runbook is documented as many-to-many-capable (Runbook's own doc comment)
// even though no built-in exercises more than one yet.
func ForCheck(checkName string) []Runbook {
	var out []Runbook
	for _, rb := range Runbooks() {
		if rb.CheckName == checkName {
			out = append(out, rb)
		}
	}
	return out
}

// healthChecksDocsLink is the shared docs anchor every built-in runbook
// links back to — the same health-checks section its attached finding's
// own DocsLink already points at (docs/features/monitoring.md §5), so "why
// did this fire" and "what does preparing remediation do" read from the
// same page.
const healthChecksDocsLink = "docs/features/monitoring.md#5-health-checks"

func deleteOrphanVnetRunbook() Runbook {
	return Runbook{
		Name:      DeleteOrphanVnet,
		CheckName: "orphan_vnet",
		Title:     "Delete orphaned SDN VNet",
		Summary: "The VNet's zone no longer exists, so the VNet cannot realize on any node. " +
			"Re-verifies the zone is still missing, then stages a draft changeset deleting the VNet.",
		DocsLink: healthChecksDocsLink,
		Template: TemplateDeleteOrphanVnet,
		Steps: []Step{
			{Kind: StepReadCheck, Description: "the finding's VNet still exists in the current inventory snapshot"},
			{Kind: StepReadCheck, Description: "the VNet's zone is still absent from the current inventory snapshot"},
			{Kind: StepOpTemplate, Description: "sdn.vnet.delete targeting the orphaned VNet"},
		},
	}
}

func deleteUnusedFwRuleRunbook() Runbook {
	return Runbook{
		Name:      DeleteUnusedFwRule,
		CheckName: "fw_rule_unused",
		Title:     "Delete unused firewall rule",
		Summary: "The rule has recorded zero hits in the finding's own window. Re-verifies it is still " +
			"unused against fresh firewall log analytics, then stages a draft changeset deleting it. " +
			"Guest-scoped rules only today.",
		DocsLink: healthChecksDocsLink,
		Template: TemplateDeleteUnusedFwRule,
		Steps: []Step{
			{Kind: StepReadCheck, Description: "the rule's origin is guest-scoped (cluster/group rules are not supported yet)"},
			{Kind: StepReadCheck, Description: "the exact rule (guest + origin + position + group) still has zero recorded hits against fresh firewall log analytics"},
			{Kind: StepOpTemplate, Description: "fw.rule.delete targeting the still-unused rule's position"},
		},
	}
}

func trimUnusedTrunkVidsRunbook() Runbook {
	return Runbook{
		Name:      TrimUnusedTrunkVids,
		CheckName: "trunk_unused_vlans",
		Title:     "Narrow trunk to VLANs in use",
		Summary: "The bridge trunks VIDs no attached guest NIC currently uses. Recomputes the currently- " +
			"unused set fresh from live inventory, then stages a draft changeset narrowing the bridge's " +
			"trunked VID set to exactly what is in use.",
		DocsLink: healthChecksDocsLink,
		Template: TemplateTrimUnusedTrunkVids,
		Steps: []Step{
			{Kind: StepReadCheck, Description: "recompute, fresh from the current inventory snapshot, which trunked VIDs no guest NIC on this bridge uses"},
			{Kind: StepOpTemplate, Description: "bridge.update narrowing Vids to the VIDs currently in guest use"},
		},
	}
}
