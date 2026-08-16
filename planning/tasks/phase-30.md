# Phase 30 — The visible product

**Roadmap:** [`docs/roadmap-earned.md`](../../docs/roadmap-earned.md) ·
**Plan:** [`../implementation-plan-earned.md`](../implementation-plan-earned.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/features.md`, `docs/status-matrix.md` §7.

The organising rule, from the arc roadmap: **a backend feature without a UI is not shipped in
this product.** vnprox's stated identity is "a complete, modern, visual web interface for **all**
Proxmox networking". A capability an operator can only reach with `curl` does not meet that
sentence, however good its tests are.

These are **assembly cards, not design-from-scratch cards**: every route each one needs already
exists, is contract-frozen, and is covered by `internal/apicontract` goldens. The work is
screens, wiring, and help — not new backend surface. A card that finds itself adding routes has
almost certainly misread its scope; say so in the report rather than growing the API.

---

## The evidence these cards are scoped from

The arc roadmap said "~19 backend feature areas have zero frontend client" and named them from an
audit. **That list was re-derived mechanically on 2026-08-16 (T-2906/Phase 30 authoring) rather
than carried forward**, because the same audit also produced a claim about canary apply that
turned out to be false (see `T-3005`). The method, so it can be re-run and disagreed with:

```bash
# every /api/v1 route family the daemon serves
python3 -c "import json;d=json.load(open('docs/openapi.json'));\
print(sorted({p.split('/')[3] for p in d['paths'] if p.startswith('/api/v1/')}))"

# every path any non-test file under web/src passes to apiFetch
grep -rhoE 'apiFetch[^(]*\(\s*[\`\"]/[A-Za-z0-9/{}._$-]+' web/src --include=*.ts --include=*.tsx
```

**Result: 70 `/api/v1` route families; 53 are called from `web/src`; 17 are not.**

| Headless route family | Ops | Card | Note |
|---|---|---|---|
| `gitsync` | 1 | `T-3001` | |
| `spec` | 5 | `T-3001` | `GET /spec`, `POST /spec/import`, `GET`/`POST`/`DELETE /spec/pin` — there is no `plan` or `diff` route; the plan IS `POST /spec/import`'s empty-ops response |
| `policies` | 3 | `T-3002` | `vnproxctl policy` is the only client |
| `compliance` | 2 | `T-3002` | |
| `digest` | 2 | `T-3002` | schedule is API-only; `AlertRules.tsx` covers delivery targets, not the schedule |
| `tenants` | 8 | `T-3002` | largest single headless surface |
| `tokens` | 3 | `T-3003` | minting a bearer token needs `curl` today |
| `webhooks` | 3 | `T-3003` | Phase 29 added `[webhooks]` policy with no UI to see it |
| `plugins` | 4 | `T-3003` | the hub client has a UI; plugin *management* does not |
| `doctor` | 1 | `T-3003` | `--live` |
| `failsim` | 1 | `T-3004` | |
| `wan` | 3 | `T-3004` | |
| `capacity` | 1 | `T-3004` | export only; forecasts already surface as findings — see the card |
| `pbs` | 1 | `T-3004` | |
| `qos` | 1 | `T-3004` | |
| `ipv6/segments` | 1 | `T-3004` | `DualStackWizard.tsx` exists and calls nothing |
| `dashboard` | 1 | — | **False positive, do not build a screen for it.** The dashboard has a UI; `web/src/dashboard/` composes its tiles from other routes and never calls `GET /api/v1/dashboard`. Decide in `T-3003` whether the route or the composition is the redundant one — do not assume the route is |

Two families the roadmap named are **not** in this list and the cards must not chase them:
`ha` and `sriov` have no `/api/v1` route family of their own in `docs/openapi.json` at all.
Establish where their surface actually lives before scoping any work against them.

The generator behind `docs/openapi.json` walks the daemon that `testdata/dev.toml` brings up, so
subsystems that config leaves disabled (the MCP transport, the plugin hub) are outside the count.
Treat 17 as a floor.

---

## T-3001 · Config-as-code cockpit ★

**kind:** implementation · **depends on:** —
**context:** `internal/api/gitsync.go`, `internal/api/spec.go`, `internal/api/drift.go`,
`docs/api.md` §gitsync/§spec, `docs/features/topology.md` §6, `web/src/drift/`

Six routes, one workflow, no screen. `[gitsync]` opens a changeset when the repo and the cluster
disagree (`T-2701`); `POST /spec/import` is the drift-detection primitive the automation contract
specifies for `terraform plan` (`T-2702`/`docs/api.md`); `POST /drift/{id}/restore-intent` and the
adopt-drift path are the two explicit reconciliation actions (`T-2703`). `web/src/drift/` exists
and covers none of it.

- One screen presenting the three-way state — **spec, config, live** — as the product's own
  vocabulary rather than three separate API concepts. The drift page is the natural home.
- The exact route set, verified against `docs/openapi.json` on 2026-08-16 so nobody has to guess:
  `GET /gitsync/status` · `GET /spec` · `POST /spec/import` · `GET`/`POST`/`DELETE /spec/pin` ·
  `GET /drift` · `GET /drift/{id}/adoption` · `POST /drift/{id}/adopt-reality` ·
  `POST /drift/{id}/restore-intent` · `POST /drift/{id}/fix`. Read each one's contract in
  `docs/api.md` before building against it, and **report any mismatch between this card and the
  contract rather than coding around it** — two claims in this phase's roadmap have already
  turned out to be wrong that way.
- Surface `GET /gitsync/status` (**that is the exact path — there is no bare `/gitsync`**): repo, ref, path, last sync, last error. A `[gitsync]`
  configuration that is failing must be visible without reading the journal.
- Both reconciliation directions as explicit, separately-confirmed actions — "restore intent"
  (bring the cluster back to spec) and "adopt reality" (rewrite spec from live). **Never a single
  "reconcile" button**: the two have opposite blast radii and `T-2703` shipped them apart on
  purpose.
- `POST /spec/import` with empty ops is a clean plan; render `ops == 0` and `notInSpec == 0` as
  the two distinct facts they are, per `docs/api.md`'s drift-detection paragraph.
- Every mutation goes through the change engine's normal review flow. This card adds **no** apply
  path of its own.

**Acceptance**

1. With `[gitsync]` unconfigured, the screen says so and offers no controls that would 500 —
   asserted by a test, not by inspection.
2. A spec/config/live disagreement renders all three states and both reconciliation actions;
   each action opens a changeset and lands in the ordinary review screen.
3. "Adopt reality" and "restore intent" are separately confirmed and separately audited; a test
   asserts a single click cannot produce both.
4. A gitsync error state (bad ref, unreachable remote, auth failure) renders the daemon's own
   error message rather than a generic failure.
5. Help topics for the screen and each panel; the `T-3006` panel-aware coverage gate must pass.
6. No new route. `docs/openapi.json` is byte-identical before and after.

---

## T-3002 · Governance surfaces ★

**kind:** implementation · **depends on:** —
**context:** `internal/api/policies.go`, `internal/api/compliance.go`, `internal/api/digest.go`,
`internal/api/tenants.go`, `internal/change/policy/`, `web/src/changesets/ReviewApplyScreen.tsx`,
`docs/features/change-management.md` §2/§4

Fifteen routes across four governance features, none reachable from the UI. Policy-as-code
(`T-2601`) blocks applies with `severity: deny` and an operator cannot see which policies exist.
Multi-tenancy (`T-2605`-era) is eight routes with no screen. Compliance mapping reports
`unmapped` honestly and to nobody.

- **Policies**: list the active set, show each rule's `match`/`assert`/`severity`, and — the part
  that earns the card — surface a `deny` verdict **inside the review screen where it blocks**,
  naming the rule, not as a generic validation error.
- **Two-person rule**: `ReviewApplyScreen.tsx` already disables Apply with an approval count.
  Break-glass is `POST /changesets/{id}/break-glass` with **no caller in `web/src`** — an
  emergency override reachable only by hand-crafting a request is not an emergency override.
  Give it a deliberately high-friction affordance: written reason required, the audit
  consequence (`change.breakglass` plus a 24h-unacknowledgeable finding) stated *before* confirm.
- **Tenants**: the eight routes as an **admin** management screen — list, get, create, delete,
  put/delete scope, put/delete member (`internal/api/tenant.go:268-282`). Out-of-scope objects
  must stay genuinely invisible, so this screen is also the place to prove the "returns not found"
  property in a browser test.

  > **Card corrected 2026-08-16 before dispatch.** An earlier revision said these eight routes
  > include "the request/approve flow". **They do not.** The only approval routes in the API are
  > `POST /changesets/{id}/approve` and `POST /changesets/{id}/review/approve`, which belong to
  > the two-person rule, not to tenancy. There is no tenant self-service request or approval
  > route. If self-service tenancy is wanted it is a backend card, not this one — build the admin
  > surface that exists and say so.
- **Digest schedules**: `GET`/`PUT /digest/schedule` beside the existing alert-rule settings.

**Acceptance**

1. A changeset blocked by a `deny` policy shows the rule id and its `assert` in the review
   screen; the copy does not invent a reason the daemon did not give.
2. Break-glass requires a typed reason, states the audit consequence before confirming, and is
   unreachable in one click from the blocked state. Asserted in a test.
3. A tenant-scoped session cannot see an out-of-scope guest anywhere in the new screens; the
   assertion is on the rendered DOM, not on the API response.
4. Compliance controls with no mapped evidence render as `unmapped` — never as pass, never
   hidden.
5. Digest schedule round-trips through `PUT` and reflects the daemon's stored value on reload.
6. Help topics for every new screen and panel; coverage gate green. No new route.

---

## T-3003 · Platform panel

**kind:** implementation · **depends on:** —
**context:** `internal/api/tokens.go`, `internal/api/webhooks.go`, `internal/api/plugins.go`,
`internal/api/doctor.go`, `web/src/settings/`, `docs/security.md` §Authentication,
`docs/deployment.md` §`[webhooks]`

Eleven routes behind Settings. Two of them are newly urgent because Phase 29 changed their
semantics with no UI to observe the change:

- **Tokens** (`T-2903`): tokens now carry `expiresAt`, defaulting to 90 days, and `read_only`
  deployments force read-only caps regardless of stored scope. Minting a token requires `curl`
  and the CSRF double-submit dance. The list must show scope, expiry, **and whether a
  deployment-level `read_only` is currently narrowing it** — a token whose stored scope and
  effective scope differ is exactly the confusion `T-2903` existed to end.
- **Webhooks** (`T-2905`): destinations are now refused at registration and again at dial time
  when they resolve to private/loopback/link-local addresses, unless `[webhooks]
  allow_private_targets` is set. An operator whose webhook silently stopped working after
  upgrading needs to see *which policy refused it*, not a generic delivery failure.
- **Plugins**: list, enable, disable, remove — `GET /plugins`, `POST /plugins/{id}/enable`,
  `POST /plugins/{id}/disable`, `DELETE /plugins/{id}` — with the declared capability scope shown
  as the ceiling it is.

  > **Card corrected 2026-08-16 before dispatch.** An earlier revision said "install/list/remove".
  > **There is no plugin install route under `/api/v1/plugins`.** Installing goes through
  > `POST /hub/install`, which already has a UI (`web/src/hub/`). Do not build a second install
  > path. What is headless is the *lifecycle* of an already-installed plugin.

  Signature verification stays mandatory; this card adds no trust bypass, and a registry-supplied
  manifest must not be able to widen anything (`T-2904`).
- **`doctor --live`**: render `GET /doctor/live`'s ten checks, including the two that still
  `skip` by design (`T-2406-followup-01`/`-02`). **A skipped check must not render as a passing
  one** — that is the same failure this arc keeps finding, and `vnproxctl verify`'s own exit-code
  rule is the precedent to copy.

Also in scope, as a **decision rather than a screen**: `GET /api/v1/dashboard` has no caller
(see the evidence table above). Determine whether the route or the tile-composition is redundant
and record the answer. Do not build a second dashboard.

**Acceptance**

1. A token minted through the UI carries the default 90-day expiry, and the list distinguishes
   stored scope from effective scope under `read_only`.
2. Registering a webhook pointing at a private address in the default configuration is refused
   with the daemon's own reason naming the policy and the knob that would permit it.
3. Plugin install shows the declared capability scope before confirming; a scope disagreement
   between listing and manifest is refused and surfaced (re-asserting `T-2904`, not re-implementing it).
4. `doctor --live` renders pass/fail/skip as three distinct states; a test asserts skip is not
   styled or counted as pass.
5. The `/api/v1/dashboard` question is answered in the card report with evidence either way.
6. Help topics; coverage gate green. No new route.

---

## T-3004 · Analysis surfaces

**kind:** implementation · **depends on:** —
**context:** `internal/api/failsim.go`, `internal/api/wan.go`, `internal/api/capacity.go`,
`internal/api/pbs.go`, `internal/api/qos.go`, `internal/api/ipv6.go`,
`web/src/ipv6/DualStackWizard.tsx`, `docs/features/monitoring.md`

Eight routes across six analysis features. These are the cards' easiest wins — each is
read-mostly, and several already have a partial component waiting for data.

- **Failure simulation / SPOF**: "what breaks if this NIC/bond/switch dies", drawn on the map
  rather than listed. The existing paint-mode machinery is the right vehicle.
- **WAN health** (`T-2905` added `validWANHost` validation): uplink status, and the validation
  failure surfaced as a named refusal.
- **Capacity**: `GET /capacity/export?ref=&kind=link|ipam_pool[&format=csv|json]` — a per-entity
  history export, bounded server-side to `aggregate_retention_days`. The deliverable is an export
  affordance on the entity that owns the data (link, IPAM pool), stating the retention bound.

  > **Correction to a correction, and this one was the orchestrator's own.** An earlier revision
  > of this bullet told the implementer *"capacity forecasts are not headless — they already reach
  > the operator as `SourceCapacity` findings, which have a UI"*. **That is false**, and the
  > T-3004 agent caught it by checking rather than complying. The findings *stream* has a UI, but
  > it does not model the capacity source: `FindingSource` in `web/src/api/types.ts` lists five of
  > the backend's sixteen `findings.Source` constants, `capacity` is not one of them, and
  > `SOURCE_LABELS[f.source]` therefore evaluates to `undefined` — a capacity finding renders
  > literally as `undefined · capacity_link_forecast` and cannot be filtered for. Filed as
  > `T-3004-followup-01`. The instruction "do not build a second forecast screen" still stands,
  > because the fix belongs in the findings stream, not here.
- **PBS backup-path awareness** and **QoS editing**: QoS is the one write path in this card, so
  it goes through the change engine like everything else.
- **IPv6 segments**: `DualStackWizard.tsx` exists and calls no route. Wire it to
  `GET /ipv6/segments` or delete the component — a wizard that cannot read the plan it is
  supposed to edit is worse than no wizard.

**Acceptance**

1. A failure simulation names the affected entities as map links, and states "indeterminate"
   where it cannot decide — matching the simulator's existing four-verdict honesty.
2. The capacity export is reachable from a link and from an IPAM pool, passes both required
   query parameters, and states the retention bound. No second forecast screen is added — the
   report says explicitly that `SourceCapacity` findings were checked and already have a UI.
3. QoS edits stage a changeset; no direct write path exists (grep-level or type-level assertion).
4. The IPv6 wizard either reads `/ipv6/segments` or is removed; the report says which and why.
5. Every one of the six areas is reachable from the nav or from an entity inspector — a screen
   that exists but cannot be navigated to does not close this card.
6. Help topics; coverage gate green. No new route.

---

## T-3005 · Canary apply: give it a UI ★

**kind:** implementation · **depends on:** —
**context:** `internal/change/apply.go` (`ApplyStaged`, `ContinueStagedApply`,
`StagedApplyState`), `internal/api/changesets.go` (`StagedApplyService`, `withStagedApply`),
`web/src/changesets/ReviewApplyScreen.tsx`, `docs/features/change-management.md` §4

> **Read this before starting.** The arc roadmap originally scoped this card as "implement the
> default path, which returns 501". **That was wrong and was corrected on 2026-08-16 (`T-2906`).**
> The 501 in `internal/api/changesets.go` sits behind `svc.(StagedApplyService)` and fires only
> for a changeset service that does not implement `ApplyStaged` — a test-double escape hatch. The
> production wiring injects `*change.Service`, which implements all three staged-apply methods.
> **The backend works. Do not "fix" it.** If you find yourself changing `internal/change/apply.go`
> to close this card, stop and re-read this paragraph.

Canary apply (`T-2602`) and finding-triggered auto-rollback (`T-2603`) both ship complete,
survive a daemon restart mid-hold, and are reachable only through the API and CLI.
`docs/features.md` lists canary as shipped **P0**. `CHANGELOG.md`'s v3.5.0 entry says plainly
there is no picker in the review screen. This card closes that gap and nothing else.

- A strategy picker in the review screen: `mode: all` (today's default, unchanged) vs
  `mode: canary` with node selection and `gate: manual|auto`.
- A **rollout-state view** for a changeset mid-hold: which nodes are done, which are pending,
  what the gate is waiting for, and the Continue action. This is the load-bearing half — a canary
  that pauses with no way to see that it paused is worse than no canary.
- The `autoRollbackOnError` toggle beside it, off by default, with the confirm-window
  relationship stated (it governs the window, not the fan-out).
- Restart survival is already proven server-side; the UI must re-derive state from
  `StagedApplyState` on load rather than holding it in the client.

**Acceptance**

1. A canary apply staged from the UI produces the same changeset as the equivalent API call —
   asserted by comparing the request body, not by eyeballing the result.
2. Mid-hold, the rollout view renders per-node state and the Continue action; reloading the page
   re-derives that state from the server.
3. `mode: all` remains the default and its request body is unchanged from today's (regression
   assertion — this card must not alter the existing apply path).
4. Auto-rollback toggle round-trips and is off by default.
5. A Playwright spec drives a canary apply against the mock stack through the hold and out the
   other side.
6. Help topics; coverage gate green. **`internal/change/` is not modified** — a diff touching it
   fails this card's review.

---

## T-3006 · Help completion and a panel-aware coverage gate

**kind:** implementation · **depends on:** T-3001…T-3005 (it gates their help)
**context:** `web/src/help/coverage.test.ts`, `web/src/help/registry.ts`,
`web/src/help/content/`, `planning/tasks/phase-22.md` (followups 01 and 02)

The help coverage gate derives its inventory from `App.tsx`/`NavRail.tsx` — **routed screens
only**. It is structurally blind to panels, which is exactly how `T-2005` (the PWA) shipped with
zero help topics and no test noticed. Every card in this phase adds panels.

- Absorb `T-2202-followup-01`: the 20+ help topics that exist but have no `?` anchor at their own
  panel.
- Absorb `T-2202-followup-02`: field-level inline help in the entity editors.
- **Extend the gate's inventory to panels**, so a new panel without a topic fails a test rather
  than an audit. This is the card's real deliverable; the two followups are the backlog it
  happens to clear.

**Acceptance**

1. The gate enumerates panels as well as routes, derived from the source (a `HelpAnchor` census
   or equivalent) and **not** from a hand-maintained list — the same property phase 22's gate
   already holds for routes.
2. Verified by mutation: adding a panel with no topic fails; removing an anchor fails; a typo in
   a `topic` id fails. Each failure names the offender.
3. Every panel added by `T-3001`–`T-3005` has a topic, and the gate proves it.
4. `coverage.test.ts`'s existing assertions are **extended, never weakened** — no suppression,
   no allowlist, no narrowing of the route inventory.
5. Field-level help exists in at least the entity editors named by
   `docs/features/change-management.md` §5.

---

## Phase invariants

- **No new API routes.** Every card asserts `docs/openapi.json` is unchanged. A card that needs a
  route has misread its scope; raise it rather than adding one.
- **No new write paths.** Everything that mutates goes through the change engine, as always.
- **A skipped or unknown state never renders as a healthy one.** This arc has now found the same
  bug three times (the doctor's confident skip, `pwa.servable` skipping on the deployments it was
  written for, and the guest-interior panel's synthetic error). Treat it as a house style, not a
  per-card acceptance criterion.
- **The gate is the orchestrator's to run.** Implementation agents do not run `make check`,
  `make e2e`, or Playwright.
- **One migration number per wave**, if any card needs one at all — none is expected to.

---

## `T-3001-followup-01` · every `terraform plan` leaves a draft changeset behind (open)

**Found:** 2026-08-16, by the T-3001 agent reading the contract instead of the card, and confirmed
by the orchestrator against the code. **Not a UI defect — out of Phase 30's scope, filed rather
than fixed.**

`docs/api.md`'s automation contract specifies **`POST /spec/import`'s empty-ops response** as the
route external tooling uses to answer "would `apply` change anything" — the `terraform plan`
primitive. That paragraph also says, correctly, that it is *"idempotent by construction:
re-importing the same unchanged content repeatedly always yields zero ops"*.

Idempotent in **ops**. Not in **rows**.

`internal/api/spec.go:128` calls `changesets.Create` unconditionally, before it knows whether the
diff is empty, and the route is `netWrite` + CSRF. `docs/api.md:1050` and `:1054` both state this
plainly, so the *documentation* is accurate — what nothing states is the consequence of combining
the two paragraphs:

- Every `terraform plan` creates a draft changeset.
- **Nothing ever prunes drafts.** `internal/store/retention.go` retains *snapshots*
  (`snapshot_keep_days`, committed-changeset snapshots, the 7-day manual-rollback window). There
  is no `DELETE FROM changesets` anywhere in `internal/store` outside the explicit
  `DELETE /changesets/{id}` discard endpoint. Grep confirms it.

So a provider polling `plan` on a schedule — which is the normal way Terraform is operated — grows
the changeset table without bound, and fills the operator's changeset list with drafts nobody
staged deliberately. At a 15-minute poll that is ~35,000 drafts a year, per cluster.

**Do not "fix" this by making the UI hide them.** The T-3001 screen already does the honest thing:
its button is labelled "Plan against live state (stages a draft)" and the result links to the draft
noting it can be discarded. The question this card raises is a product one, and it has at least
three defensible answers, which is exactly why it is filed rather than decided here:

1. A side-effect-free plan mode (`?dryRun=1`) that runs the same `spec.Import` diff and returns the
   ops without calling `Create`. Additive, contract-compatible, and it makes the automation
   contract's own words true. **Most likely right.**
2. Retention for drafts — but a draft an operator staged by hand and left overnight must not
   evaporate, so this needs a provenance distinction (`origin: spec_import`) the store does not
   currently carry.
3. Leave it, and document the accumulation plus the expectation that callers
   `DELETE /changesets/{id}` after reading the plan. Cheapest, and it makes every integrator
   responsible for cleanup they will forget.

**Already checked, so nobody repeats it:** `internal/apicontract`'s `TestSpecImportIdempotency`
(`internal/apicontract/specimport_test.go`) asserts `len(ops) == 0` and `len(notInSpec) == 0` on
**both** imports and asserts a read-only token gets 403. It asserts **nothing about how many
changesets now exist**, and never compares the two responses' changeset ids — which will differ.
Its own comment says the second import yields "the same 'no changes' result, not an accumulating
diff", and that is exactly true and exactly the wrong dimension: the *diff* does not accumulate,
the *rows* do. The test passes identically whether the route creates zero drafts or two.

So the contract's "idempotent by construction" has never been tested in the dimension that
matters, and whichever of the three answers above is chosen, the fix ships with a row-count
assertion in that test — otherwise the same claim stays untested afterwards.

---

## `T-3004-followup-01` · eleven of sixteen finding sources render as `undefined` (open)

**Found:** 2026-08-16 by the T-3004 agent, verifying a claim this card made rather than complying
with it. Confirmed by the orchestrator. **UI-only; in Phase 30's spirit but not in any card's file
set, so it is filed rather than smuggled into one.**

`web/src/api/types.ts:1107` declares:

```ts
export type FindingSource = "drift" | "lldp" | "ipam" | "health" | "probe";
```

`internal/findings` declares **sixteen**: `baseline`, `capacity`, `cert`, `drift`, `federation`,
`flow`, `gitsync`, `health`, `ipam`, `lldp`, `peer`, `probe`, `rogue`, `store`, `wan`, `wireguard`.

Eleven are missing, and the consequence is visible to users rather than merely typed wrong:

- `FindingsStreamPanel.tsx:307` builds each row's category as
  `` `${SOURCE_LABELS[f.source]} · ${f.check}` ``. `SOURCE_LABELS` is a
  `Record<FindingSource, string>` with five keys, so a capacity finding renders literally as
  **`undefined · capacity_link_forecast`**.
- The source filter (`FindingsStreamPanel.tsx:194`) enumerates `Object.keys(SOURCE_LABELS)`, so
  eleven sources cannot be filtered for at all.

This is the same family as everything else this arc keeps finding — an unmodelled state rendering
as a definite-looking one — except here the definite-looking thing is the string `"undefined"`.

**Why it was not fixed in place:** `web/src/findings/**` belonged to no wave-1 card, and widening
the union without also widening `SOURCE_LABELS` breaks the `Record<FindingSource, string>`
exhaustiveness that is the only thing currently keeping the two in step. Both must move together.

**Scope when taken:**

1. Widen `FindingSource` to all sixteen and give every one a label. The `Record<FindingSource,
   string>` type already forces exhaustiveness — keep it, it is what will catch the seventeenth.
2. A test that fails when the two lists disagree. The union is hand-maintained against a Go
   package the frontend cannot import, which is exactly the shape that produced the
   `vnproxctl backup` field-name defect (see `planning/tasks/phase-29.md`'s wave-4 record) — so
   pin it the same way, with a golden the Go side also asserts against rather than a second
   hand-written list.
3. Check the same class elsewhere before closing: any other `Record<SomeUnion, string>` in
   `web/src` whose union is a hand-copied subset of a Go constant set.

**Two smaller findings recorded here rather than lost**, both from the same agent:

- `GET /ipam/subnets/{prefix}/v6-plan` has no frontend caller, and a family-level headless audit
  cannot see it because sibling `ipam` routes are called. The phase preamble's "treat 17 as a
  floor" is right for a second reason: the census is per-family, not per-route.
- Help topic `ipv6-planning` describes a planning grid that does not exist in `web/src`, and
  `topology-paint-modes` describes a backup-path paint mode that does not exist either. The help
  coverage gate proves every screen *has* a topic; nothing proves a topic describes something
  real. That is `T-3006`'s natural extension and should be considered when it is written.
