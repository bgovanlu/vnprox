// SPDX-License-Identifier: Apache-2.0

// Package explain implements T-4104's deterministic, offline explainers —
// no LLM, no network, works unchanged on an air-gapped install. Two
// independent halves, both templates over typed data:
//
//   - findings.go/registry.go/types.go: "what does this finding mean, why
//     does it matter, what would an operator do", for every check name
//     internal/findings.AllCheckNames() can emit — generated from a
//     per-check template registry keyed on findings.Finding.Check, not by
//     reformatting or parsing findings.Finding.Detail.
//   - ops.go: "what does this changeset op do", generated directly from
//     internal/change's own typed Op.Type/Op.Target/Op.Params — change.Op
//     carries no rendered-text field to parse in the first place, so this
//     half has no Detail-shaped trap to avoid; see ops.go's own doc comment.
//
// # Why this is not internal/findings.Explain(check string)
//
// The obvious home for "explain a finding" is internal/findings itself, and
// that is where the task card's own phrasing starts ("likely internal/
// findings gains an Explain(check string) Explainer"). It cannot live
// there: this package points a check's explanation at its runbook where
// one exists (internal/runbook.ForCheck), rather than restating the
// runbook's remediation in prose — and internal/runbook already imports
// internal/findings (runbook/types.go's findingsCheckNames, which validates
// a built-in runbook's CheckName against findings.AllCheckNames()). Putting
// the explainer inside internal/findings and having it call
// runbook.ForCheck would close that import loop the wrong way. A small
// sibling package that imports both is the same shape internal/runbook
// itself already is relative to internal/findings and internal/change (a
// composition, not a modification, of the packages it reads).
//
// # Why Detail is not the input
//
// internal/findings.Finding.Detail is a string a producer renders once
// (internal/findings/types.go's own doc comment on Finding). There is no
// structured value underneath it to recover by parsing — and parsing a
// human-readable string back into structure is exactly the trap the task
// card calls out. Every template here reads Finding.Check (which template
// to use), Finding.Severity, Finding.Nodes, and Finding.Refs (how to
// parameterize it) — fields the producer set as data, not prose it
// rendered.
//
// # Templates, not prose interpolation
//
// A findingTemplate (registry.go) is a fixed pair of static sentences (What
// it means, Why it matters) plus, for a check with no built-in runbook, a
// static remediation sentence. Explain (findings.go) composes those with a
// generic "Where" clause built from Finding.Nodes/Finding.Refs and a
// generic severity framing built from Finding.Severity — the parts that
// are the same shape for every check, so no template needs to restate them.
//
// # No LLM, no network
//
// This package imports internal/findings, internal/runbook, internal/change,
// internal/inventory, and the Go standard library's fmt/sort/strings —
// nothing that can reach a network or a model backend.
// nonetwork_test.go's TestNoNetworkCapableImports asserts that statically
// (parses this package's own non-test source for forbidden imports), so a
// future dependency added here without reading this doc comment first
// fails a test rather than quietly requiring network access on an
// air-gapped install.
package explain
