// SPDX-License-Identifier: Apache-2.0

// Package ifaces turns changeset ops that affect a node's
// /etc/network/interfaces(5) file (the "iface", "bond", "bridge", and
// "vlan" op groups in docs/data-model.md §3) into minimal AST edits on top
// of internal/host's lossless interfaces(5) parser, and renders the
// resulting change as unified diffs and structured op summaries matching
// the docs/api.md `GET /changesets/{id}/diff` shape.
//
// This package defines its own Op tagged union (op.go) rather than
// depending on internal/change's Op type, which is being designed and
// implemented concurrently (T-201) in a separate worktree against the same
// frozen docs/data-model.md §3 vocabulary. See op.go's doc comment and the
// T-204 task report (planning/reports/T-204.md) for the exact type/field
// names an integrator needs to reconcile the two against.
//
// Mutation contract (docs/data-model.md §3, docs/features/change-management.md
// §3, task card T-204):
//
//   - Untouched stanzas are byte-identical: Mutate only ever appends new
//     host.Entry values or edits BodyItem/Entry values belonging to the
//     stanza(s) the op actually targets; every other Entry in the File
//     keeps its original Raw bytes.
//   - Comments are preserved: editing an existing stanza only touches the
//     specific option lines the op changes; other BodyItems (including
//     comments and blank lines) are left exactly as parsed.
//   - Stanzas newly created by a Create op carry a
//     "# managed by vnprox (changeset <id>)" comment as the first line of
//     their body, and are preceded by an "auto <name>" line when the op's
//     Autostart is true.
//   - New stanzas are appended at the end of the file, in the order their
//     Create ops appear in the caller's op slice — this is the
//     "deterministic stanza ordering" the task card asks for: it depends
//     only on op order, never on map iteration or entity name sorting.
package ifaces
