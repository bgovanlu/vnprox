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
// (T-405). On this branch's base, internal/ipam is an empty stub package
// (T-405 depends on T-401 and has not landed) and there is no picker
// component in web/src. Suggest (suggest.go) therefore computes
// next-free-address directly off inventory.Snapshot's already-declared
// addresses rather than delegating to that not-yet-landed subsystem — see
// the T-603 completion report for the full note and what a follow-up
// should replace once T-405 lands.
//
// This package never talks to internal/store or internal/api directly;
// Service (service.go) is the seam those packages depend on.
package blueprint
