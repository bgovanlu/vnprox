# T-3714 · Tenant mutation routes were reported unscoped — re-derived, and the gap was already closed

**Found by:** `planning/tasks/debt-sweep-2026-08-19.md`'s "STILL OPEN, and arguably worse than the
leak that was fixed" section, and a comment in `tenant_test.go`. Both were correct **when written**.
· **size:** S (verification + retroactive card; no code change) · **depends:** T-3002-followup-01
(read scoping) · **affects:** `internal/api/tenant.go`, `internal/tenant`, `docs/api.md`,
`docs/security.md`

## What was observed (the reported finding)

The debt sweep, run 2026-08-19 at HEAD `d8d2879b`, recorded: `POST`/`DELETE /tenants` and the
`PUT`/`DELETE` routes on `.../scopes` and `.../members` gated only on the `netWrite` capability,
with no check that the caller belonged to the tenant named in the URL. A caller holding `netWrite`
plus membership in **one** tenant could mutate **any** other tenant's scopes and members — added
themselves as an approver of a tenant they don't belong to, deleted a rival tenant outright, or
widened a foreign tenant's visible-resource scope. The sweep explicitly called this **worse** than
the read leak fixed earlier the same day (T-3002-followup-01), because it rewrites a boundary
rather than merely disclosing one, and it recorded the finding as having **no card**.

## Re-derivation (this task, 2026-08-27)

Before changing anything, this task re-checked the claim against the code as it stands today, per
`CLAUDE.md`'s "the recurring failure mode is a claim nobody re-checked."

**The gap does not exist in the code today.** `git log` shows a commit landed the same day as the
debt sweep, 35 minutes after the sweep document was committed:

```
6c66ee60  14:21:23  planning: close the debt sweep — six fixed, one disproven, three newly open
4713d72c  14:56:52  api: scope tenant mutations too, and lock the scope boundary to fleet admins
                     (T-3002-followup-02)
```

`4713d72c` is an ancestor of current `HEAD`. It is the fix this card would otherwise have specced:
`internal/api/tenant.go`'s mutation route group now runs `tenantScopeMiddleware` (the same
middleware the read routes use, `git commit 4713d72c` reusing the T-3002-followup-01 seam) and
every one of the five mutation handlers calls `tenantMutationScope(ctx, id)` before touching the
store:

- `handleDeleteTenant`, `handlePutMember`, `handleDeleteMember`: membership-scoped — a caller who
  is a member of some *other* tenant than the URL's `{id}` gets `404` (never `403`, so the
  response never confirms a foreign tenant exists — the same stance the T-3002-followup-01 read
  fix already took); a member of their own tenant, or an unscoped (non-tenant, i.e. fleet-admin)
  `netWrite` holder, succeeds unchanged.
- `handleCreateTenant`: refused (`403`) for **any** tenant-scoped caller, stricter than
  membership — there is no existing tenant for membership to gate against, and
  `handleCreateTenant` does not add the creator as a member of the tenant it creates, so "create a
  tenant, then reach it as a member" is closed categorically rather than by rule.
- `handlePutScope` / `handleDeleteScope`: also refused (`403`) for any tenant-scoped caller,
  *including a member acting on their own tenant* — `AddScope` stores whatever `scopeRef` it is
  handed with no validation against anything, so a member permitted to widen their own tenant's
  scope could hand that tenant visibility into any resource on the cluster, including another
  tenant's exclusive scope. This is deliberately stricter than the membership-scoping pattern used
  for delete/members, and is documented as such in the handler comments.

`docs/api.md` ("Tenants & self-service", T-3002-followup-02 paragraph) and `docs/security.md`
("No cross-tenant *mutation* either") already carry this as shipped behavior, and
`docs/security.md`'s residual-risk table already lists "Cross-tenant admin *mutation*
(T-3002-followup-02)" as closed. None of that documentation needed a change for this task.

**Test evidence (already in the tree, re-run to confirm, not rewritten):**

```
$ go test ./internal/tenant/... ./internal/api/... ./internal/auth/... -run Tenant -v -count=1
--- PASS: TestTenantAdminRoutes_ScopedToMembership (0.17s)
--- PASS: TestTenantScoping_NoCrossTenantLeakage (0.28s)      # both directions, 20x, all 5 mutation routes each way
--- PASS: TestTenantAdmin_CRUD (0.08s)
--- PASS: TestTenantCreate_ScopedCallerForbidden (0.11s)
--- PASS: TestTenantScopeMutation_MemberForbiddenEvenForOwnTenant (0.15s)
--- PASS: TestTenantSelfService_MemberCanManageOwnTenant (0.13s)
ok  	github.com/bgovanlu/vnprox/internal/tenant	0.017s
ok  	github.com/bgovanlu/vnprox/internal/api	1.824s
```

`TestTenantScoping_NoCrossTenantLeakage` (`internal/api/tenant_test.go:264`) is the load-bearing
one: it seeds two tenants (t1/alice, t2/bob) and, in a 20-iteration loop, asserts bob's five
mutation attempts against t1 and alice's five against t2 all come back `404` — the "member of A,
refused on B" direction. `TestTenantSelfService_MemberCanManageOwnTenant`
(`internal/api/tenant_test.go:639`) is the other half: alice adds a member to t1, removes a member
from t1, and deletes t1 — all `204` — proving the fix is a scoping restriction, not an accidental
lockout of legitimate in-tenant self-service. `TestTenantScopeMutation_MemberForbiddenEvenForOwnTenant`
pins the stricter own-tenant-scopes rule, with an unscoped admin's equivalent call still
succeeding (`204`) alongside it.

## Deliverables

None — no code change. This card exists to give the finding a task number (closing the "has no
card" gap the debt sweep itself flagged) and to record, precisely, that the fix landed the same
day under `T-3002-followup-02` before this card was ever opened. Recording that gap-closure
explicitly matters here: the debt-sweep document itself still reads "STILL OPEN" for this item and
was never corrected in place, which is exactly the kind of document staleness the same sweep's
"Document staleness found by the same survey" section warns about elsewhere — this card is that
correction for this one item.

## Acceptance criteria

1. ~~Every mutation verifies the caller's membership in the target tenant~~ — already true; see
   `internal/api/tenant.go`'s `tenantMutationScope` and its five call sites.
2. ~~A legitimate admin/superuser path continues to work~~ — already true; the unscoped
   (non-tenant-member) `netWrite` holder is the preserved fleet-admin persona, unaffected by any
   of the five handlers' scoping checks.
3. ~~Table-driven tests proving both directions~~ — already true and already run in this task:
   `TestTenantScoping_NoCrossTenantLeakage` (refusal, both tenants, both directions) and
   `TestTenantSelfService_MemberCanManageOwnTenant` / `TestTenantAdmin_CRUD` (legitimate success).
4. This card exists and is filed under a task number, closing the sweep's own "has no card" note.

## Judgment calls for the owner to review

- **`planning/tasks/debt-sweep-2026-08-19.md` was left unedited.** Per this repo's convention
  (`internal/blueprint/suggest.go`'s stale-comment correction, the debt sweep's own
  self-corrections), a closed finding is normally noted where it was raised. This card does that
  correction *here* instead of editing the sweep document in place, on the theory that the sweep
  document is itself a dated snapshot-of-record (it says "run 2026-08-19, HEAD `d8d2879b`") and
  silently rewriting its verdict after the fact would erase the true history of "sweep flagged it,
  fix landed 35 minutes later, card came three weeks after that." If the owner prefers the sweep
  document edited in place (with this card's finding linked from there instead), that's a one-line
  change to make.
- **No behavior change shipped.** Because the gap was already closed, there was nothing to
  implement conservatively-vs-permissively — the existing fix's choices (403-for-any-tenant-scoped
  on create/scopes vs. 404-for-foreign-tenant on delete/members) were reviewed against this task's
  instructions and match what was asked: membership-scoped, admin path preserved, conservative
  where membership scoping alone would still allow an escalation (the scopes routes). Nothing
  flagged for reversal.
