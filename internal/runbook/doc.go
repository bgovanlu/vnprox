// SPDX-License-Identifier: Apache-2.0

// Package runbook implements T-4003: parameterized sequences of read-checks
// and changeset-op templates, attached to a findings check name, so an
// operator looking at a known finding gets "prepare remediation" instead of
// a blank changeset editor.
//
// This is the third caller of the stage-only contract T-4001 (the Terraform
// provider, contrib/terraform-provider-vnprox/) established and T-4002 (the
// Ansible collection, contrib/ansible-collection-vnprox/) reused: Prepare
// stages a draft changeset from a runbook's op template and runs it through
// the ordinary validator — exactly Create then Validate — and stops there.
// It never applies. Applying stays a human review action (roadmap decision
// D4, docs/adr/0004-change-engine-is-the-sole-mutation-path.md); the narrow
// changeCreator interface in service.go enforces this at compile time, the
// same way internal/plugin/stager.go and internal/mcp/stageonly.go already
// do for the plugin and MCP seams (stageonly.go in this package is that
// same assertion, a third time).
//
// # Why declarative, no interpreter
//
// A Runbook (catalog.go) is a plain Go value: a check-name attachment, an
// ordered list of Step descriptions (read-check | op-template — the shape
// the task card asks for), and a Template naming which of this package's
// fixed, closed set of op-template functions (render.go) renders its ops.
// There is no embedded code, no string-evaluated expression language, and
// no per-runbook Go closure — the same discipline internal/change/policy.go
// holds a PolicyRule to (data evaluated by a fixed engine, never a scripting
// language) and internal/blueprint holds a Blueprint's EntityTemplate.Kind
// to (a closed set of kinds, each realized by a fixed diffEntity case).
// Authoring a new runbook today still means adding a Go function to
// render.go's switch — this package does not yet let an operator define a
// wholly new template without a code change — but the *runbook itself* (its
// attachment, its steps, its documentation) is data, inspectable and
// testable without running it, exactly like a PolicyRule or a Blueprint is.
//
// # Convergence semantics (T-4016)
//
// A prepared runbook is the third face of the same open question T-4001's
// Terraform work first raised and named T-4016 over: a stage-only
// integration's "success" can look like "done" when nothing is live yet.
// This package follows T-4016's documented interim answer (option 3,
// "accept and document loudly", pending an ADR) exactly as
// planning/tasks/T-4016-stage-only-convergence-semantics.md instructs T-4003
// to: Service.Prepare's returned Changeset always carries its real Status
// (draft or validated — never applied, structurally), so a caller has the
// same status data option 2 would gate on (`status == "applied"`) if T-4016
// settles there. See service.go's Prepare doc comment for where this is
// surfaced.
package runbook
