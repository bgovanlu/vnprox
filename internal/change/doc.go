// Package change implements changesets: the v1 op vocabulary, the
// changeset aggregate and its status state machine, store-backed draft
// CRUD, and (in later tasks, T-202/T-205) validators, the differ, the
// applier, and rollback.
//
// # T-201 scope
//
// This task built:
//
//   - Op (op.go): the tagged union `{"op", "target", "params"}` covering
//     the full v1 vocabulary (docs/data-model.md §3), with typed params
//     per op and strict JSON (de)serialization — unknown op types, missing
//     required targets, and unknown fields anywhere in the envelope or its
//     params all decode to a typed *OpDecodeError carrying the offending
//     JSON path, so the API layer can return docs/api.md's
//     `validation_failed` shape without re-deriving it.
//   - Changeset (changeset.go): the aggregate plus its documented status
//     state machine (draft|validated|applying|awaiting_confirm|committed|
//     rolled_back|failed|discarded) — illegal transitions return a typed
//     *ErrIllegalTransition rather than silently succeeding or panicking.
//   - Service (service.go): draft CRUD (Create/Get/List/UpdateDraft/
//     Discard) on top of T-003's existing *store.ChangesetRepo (that
//     task already created the changesets table and repository per
//     docs/data-model.md §2 — this package does not duplicate either),
//     WS `changeset.status` broadcasts on every status transition (via
//     the Broadcaster seam, satisfied by topology.Service.Broadcast — see
//     that package's hub.go for the small generic-broadcast addition this
//     task made), and audit entries on create/discard (T-003's
//     *store.AuditRepo, same pattern as internal/auth).
//
// Diff/Apply/Confirm/Rollback are NOT implemented here — T-205 owns that
// logic; internal/api's routes for them remain 501 stubs.
//
// # T-202 scope
//
// This task built the layered validator pipeline (validate.go and its
// validate_*.go siblings) and wired it in two places: Service.Validate
// (backing `POST /changesets/{id}/validate`, promoting/demoting the
// draft<->validated status transition) and auto-validation on every draft
// mutation (Service.Create/UpdateDraft now populate Findings immediately,
// though — unlike Validate — those two never change Status themselves).
//
//   - validate.go: Validate(ops, snap) is the pure entry point (no service/
//     store dependency, so it's directly table-testable): runs classes in
//     docs/features/change-management.md §2's documented order — schema,
//     referential, [safety — T-203's insertion point], [cross-node — not
//     assigned], advisory — short-circuiting after any class that produces
//     an error-severity finding.
//   - validate_schema.go: class 1, per-op types/ranges/enums/syntax,
//     independent of the snapshot.
//   - validate_projection.go + validate_referential.go: class 2, evaluated
//     against an inventory.Snapshot *plus* every earlier op in the same
//     changeset (the "projection" folded forward op-by-op) — this is what
//     lets `bond.create bond0` followed by `bridge.port.add vmbr0 bond0` in
//     one changeset validate clean, while the reverse order correctly
//     errors (T-202 acceptance criterion 2).
//   - validate_advisory.go: class 5, style/health warnings.
//   - validate_fix.go: machine-applicable `fix` patches (MTU/VID clamps)
//     attached to the schema findings that have an obvious correction.
//   - validate_codes.go: the stable Finding.Code identifiers the golden
//     test suite and any future frontend/documentation reference.
package change
