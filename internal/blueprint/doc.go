// Package blueprint implements T-603's parameterized topology templates
// (docs/features/blueprints.md §1): a versioned JSON format
// (blueprintVersion: 1) describing entities to create with {{param}}
// placeholders and per-node expansion selectors, an idempotent
// instantiation engine that diffs the expanded, concrete entities against
// the live inventory.Snapshot (matching entities skipped, divergent ones
// produce update ops, absent ones produce create ops — never applying
// anything itself, only ever producing a []change.Op that the normal
// stage->validate->diff->apply->confirm/rollback changeset lifecycle
// consumes), a capture-from-node ("blueprint-ify") path, five bundled
// read-only starters, and a next-free-address suggester used by the param
// form's IPAM-aware address suggestions.
//
// Note on the IPAM dependency: docs/features/blueprints.md and this task's
// card call for "IPAM-aware address suggestions via the IPAM picker"
// (T-405). Suggest (suggest.go) computes next-free-address directly off
// inventory.Snapshot's already-declared addresses rather than delegating
// to internal/ipam's own next-free-address picker.
//
// Corrected 2026-08-19 (debt sweep): this comment used to say internal/ipam
// was an empty stub and no picker component existed in web/src "on this
// branch's base" — both shipped in T-405 (internal/ipam is a real package;
// web/src/ipam/NextFreePicker.tsx is a real component) some time ago, and
// the comment had not been updated since. The integration this note
// originally deferred is therefore still undone, but for a different
// reason than the one written here: T-405's picker exists now and this
// package still doesn't call it — that is real, uncarded follow-up work,
// not a missing dependency. See the T-603 completion report for the
// original note.
//
// This package never talks to internal/store or internal/api directly;
// Service (service.go) is the seam those packages depend on.
package blueprint
