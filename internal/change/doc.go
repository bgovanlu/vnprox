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
// Validate/Diff/Apply/Confirm/Rollback are NOT implemented here — T-202
// and T-205 own that logic. internal/api registers their routes (per
// docs/api.md's changesets section) but returns 501 rather than calling
// into this package for them, per T-201's task card ("reserving the route
// shape so the API surface matches the doc now").
package change
