// SPDX-License-Identifier: Apache-2.0

package runbook

import (
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// StepKind names the two — and only two — kinds of step a Runbook may
// declare (the task card's "ordered steps: read-check | op-template"). It is
// a closed vocabulary, mirroring internal/change/policy.go's closed
// factFields set: a Step is data a human reads (Runbooks/catalog_test.go
// asserts the shape below every runbook must have), not a program.
type StepKind string

const (
	// StepReadCheck documents one piece of live context Render gathers and
	// re-verifies before proposing anything — e.g. "the referenced zone is
	// still absent" or "this rule has recorded no hits since the finding
	// fired". A read-check that no longer holds makes Render return
	// ErrNothingToDo instead of a stale op (the same absent/divergent/
	// matching discipline internal/blueprint.Instantiate and T-4002's
	// Ansible modules already use).
	StepReadCheck StepKind = "read-check"
	// StepOpTemplate documents the op(s) Render proposes once every
	// preceding read-check holds.
	StepOpTemplate StepKind = "op-template"
)

// Step is one line of a Runbook's documented procedure: what it checks or
// proposes, in order. Purely descriptive — the behavior it describes lives
// in render.go's fixed, per-Template functions — but catalog_test.go
// enforces that every Runbook's Steps actually end in exactly one
// StepOpTemplate preceded by at least one StepReadCheck, so "read-checks
// before proposing" is a checked structural property, not just prose.
type Step struct {
	Kind        StepKind
	Description string
}

// TemplateKind selects which of this package's fixed op-template functions
// (render.go) renders a Runbook's ops. Closed vocabulary: Render's switch on
// this value has no default case that improvises, only one that reports
// ErrUnimplementedTemplate.
type TemplateKind string

const (
	// TemplateDeleteOrphanVnet renders one sdn.vnet.delete op for an SDN
	// VNet whose zone no longer exists (findings.CheckOrphanVnet). See
	// renderDeleteOrphanVnet.
	TemplateDeleteOrphanVnet TemplateKind = "delete-orphan-vnet"
	// TemplateDeleteUnusedFwRule renders one fw.rule.delete op for a
	// guest-scoped firewall rule with zero recorded hits
	// (findings.CheckFwRuleUnused). See renderDeleteUnusedFwRule.
	TemplateDeleteUnusedFwRule TemplateKind = "delete-unused-fw-rule"
	// TemplateTrimUnusedTrunkVids renders one bridge.update op narrowing a
	// VLAN-aware bridge's trunked VID set to only the VIDs a guest NIC
	// currently uses (findings.CheckTrunkUnusedVlans). See
	// renderTrimUnusedTrunkVids.
	TemplateTrimUnusedTrunkVids TemplateKind = "trim-unused-trunk-vids"
)

// Runbook is one declarative remediation recipe attached to exactly one
// findings check name (CheckName — a value from internal/findings' own
// catalog, findings.AllCheckNames()). Name is this runbook's own stable id
// (distinct from CheckName: a check may eventually offer more than one
// runbook — a "delete it" option and a "recreate the missing zone" option
// for orphan_vnet, say — even though only one exists for each check today).
//
//nolint:govet // fieldalignment: declaration-order readability over packing; this is a small, rarely-allocated catalog value, not a hot-path struct.
type Runbook struct {
	Name      string
	CheckName string
	Title     string
	Summary   string
	DocsLink  string
	Template  TemplateKind
	Steps     []Step
}

// ReadContext is the read-only, freshly-gathered data a Template's render
// function may consult when re-verifying its read-checks and computing its
// ops. Service.Prepare builds one right before calling Render, from live
// data — never from anything cached on the Finding itself — so a read-check
// answers "is this still true right now", not "was this true when the
// finding last fired".
type ReadContext struct {
	// Snapshot is the current inventory graph. Every built-in Template
	// needs it.
	Snapshot inventory.Snapshot
	// FwAnalytics is a fresh firewall-log analytics read (the exact same
	// seam findings.CheckFwRuleUnused itself is backed by — fwlog.Service.
	// Analytics), or nil when no FwAnalyticsProvider is configured. Only
	// TemplateDeleteUnusedFwRule needs it; every other template ignores
	// it. A caller (Service.Prepare, or a test) sets this from a fresh
	// call, never from anything cached on the Finding — see this type's
	// own doc comment.
	FwAnalytics *fwlog.Analytics
}

// findingsCheckNames is a tiny seam over findings.AllCheckNames, declared
// here (rather than called directly at each use site) so catalog_test.go's
// "attached to a finding type that no longer exists" test and any future
// caller share exactly one place that asks "which check names are real".
func findingsCheckNames() map[string]bool {
	m := make(map[string]bool)
	for _, c := range findings.AllCheckNames() {
		m[c] = true
	}
	return m
}
