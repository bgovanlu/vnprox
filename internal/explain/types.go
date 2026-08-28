// SPDX-License-Identifier: Apache-2.0

package explain

// findingTemplate is one check's fixed explanation text — data, not a
// closure, mirroring internal/runbook.Runbook's own "declarative, no
// interpreter" discipline (internal/runbook/doc.go). What and Why are
// always rendered. Remedy is used only when the check has no built-in
// runbook (registry.go's runbookFor); a check that DOES have a runbook
// leaves Remedy empty and Explain points at the runbook instead, per the
// task card's "point at it rather than restating the remediation in prose".
type findingTemplate struct {
	// What is one or two sentences: the condition that made this check
	// fire, in plain language.
	What string
	// Why is one or two sentences: why an operator should care.
	Why string
	// Remedy is what an operator would concretely do, for a check with no
	// runbook (checkHasRunbook is false). Left empty when a runbook exists
	// — Explain fills WhatToDo from the runbook instead, so a check's
	// remediation prose is never written twice.
	Remedy string
}

// Explanation is one finding's rendered, plain-language explanation:
// What/Why come from the check's findingTemplate; Where and severity
// framing are generic, computed fresh from the finding's own typed fields
// (Nodes/Refs/Severity) rather than templated per check, since every check
// shares the same shape for "which nodes/entities this concerns" and "how
// urgent this severity is".
//
//nolint:govet // fieldalignment: declaration-order readability for a small, rarely-allocated value, not a hot-path/wire struct.
type Explanation struct {
	// Check is the finding's check name (Finding.Check), carried through so
	// a caller rendering a list of explanations doesn't have to zip it back
	// against the findings it came from.
	Check string
	// Severity is the finding's own severity string, verbatim.
	Severity string
	// What is this check's condition, in plain language.
	What string
	// WhyItMatters is why an operator should care.
	WhyItMatters string
	// WhatToDo is the remediation sentence: either the check's own Remedy
	// text (no runbook exists) or a sentence pointing at the runbook by
	// name and title (one exists). Never both, and never the runbook's own
	// Summary restated — see RunbookName's doc comment.
	WhatToDo string
	// RunbookName is the runbook's own stable Name (internal/runbook.
	// Runbook.Name) when this check has one, or "" when it does not. A
	// caller that wants to offer "prepare this runbook" as a button reads
	// this rather than parsing WhatToDo's prose.
	RunbookName string
	// Where names which nodes and/or entities this finding concerns,
	// generically rendered from Finding.Nodes/Finding.Refs — never templated
	// per check, since every check's finding carries these the same way.
	Where string
}
