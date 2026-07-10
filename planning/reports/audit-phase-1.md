# Phase 1 audit — Read-only visibility (T-101…T-107)

**Date:** 2026-07-09 · **HEAD:** a5cab4c · **Auditor:** Claude (6 parallel audit agents, one per task pair)

**Method:** every acceptance criterion verified against actual code and test assertions, with the
tests executed: `go test -race -count=1` on pve/host/inventory/collect/auth/topology/api (all pass),
the full 30s inventory stress soak (`INVENTORY_STRESS_SECONDS=30`, race-clean), a real 60s fuzz run
on the interfaces parser (8.4M execs, clean), the 500-client WS load test (run explicitly, passes),
Snapshot benchmarks at the topology-spec scale target, `npx tsc --noEmit` / `eslint` / `vitest`
(66/66), and `go vet` (clean).

**Verdict: phase 1 passes functionally but has real gaps.** The core engineering is strong —
T-103's merge/delta/race guarantees are genuinely proven, and the auth rate-limiter, renewal, WS
mutation-to-delta, and 500-client tests all do what they claim. However there is **one correctness
bug** (F-01), **two silently missing deliverables** (F-02, F-05), **two unmet CI/verification
obligations** (F-03, F-06/F-07), and a recurring hygiene problem: **code comments that claim task-card
authorization that doesn't exist** (F-04 notes, F-08, and the retirement comment in F-01). Fix
F-01 before phase-2 work builds on collector output.

## Criteria summary

| Task | AC1 | AC2 | AC3 | AC4 | AC5 | AC6 |
|---|---|---|---|---|---|---|
| T-101 PVE client | PARTIAL (token mode, TTL — F-05) | PASS | PASS | PASS | — | — |
| T-102 host readers | PASS (15-file corpus, byte-identical) | PASS | PASS | PARTIAL (fuzz not in CI — F-03) | — | — |
| T-103 inventory graph ★ | PASS (30s soak race-clean) | PASS (exhaustive ownership table test) | PASS | PASS (p99 ≈ 112ns « 5ms at spec scale) | PASS | — |
| T-104 collectors | PASS (caveats F-13) | PASS | PASS | PASS (goleak, correctly used) | — | — |
| T-105 auth | PARTIAL (no TOTP fixture — F-04) | PASS | PARTIAL (missing personas — F-09) | PASS | PASS | — |
| T-106 topology API/WS | PASS | PASS | PASS | PASS (500 clients verified live) | PASS | — |
| T-107 topology UI | PARTIAL (F-06) | PARTIAL | PARTIAL | PARTIAL | PARTIAL/FAIL on measurement (F-07) | PASS |

Ground rules across all seven tasks: slog-only, `%w`-wrapped errors, strict TS — clean, with one
unchecked production cast (F-16).

## Findings — major

### F-01 · MAJOR · T-104 — departed cluster nodes are never retired from inventory
`pollClusterStatus` (`internal/collect/pve.go:56-84`) issues one node-scoped `ApplyPoll` per
*currently listed* member. A node that leaves `/cluster/status` gets no scoped call at all, so its
Node entity — and by the same pattern its pve-network interfaces, guests, and firewall rulesets
(`pollNodeNetwork`/`pollGuests`/`pollFirewall` iterate only current members) — persists as a stale
ghost until daemon restart. The comment at pve.go:50-55 explicitly claims the opposite ("a node
leaving the cluster is correctly retired"). Confirmed with a throwaway probe test replicating the
poll pattern. Inventory's `Scope` semantics work as documented; the collector simply never issues a
retiring poll.
**Fix:** remember the previously seen node set and issue empty scoped `ApplyPoll`s (per source) for
nodes that disappeared, or extend `Scope` to an enumerated node set. Fix the comment either way.

### F-02 · MAJOR · T-101 — IPAM reads deliverable is missing entirely, and silently
No IPAM method exists in `internal/pve` and pvemock has no IPAM routes. Unlike the other gaps
(token auth, per-node guest lists), which are carefully documented in code comments and tests, this
one is invisible: commit 835f28a's message lists every deliverable *except* IPAM, and doc.go's
endpoint enumeration omits it. IPAM data feeds docs/api.md's `/ipam/*` endpoints and the phase-4
IPAM work.
**Fix:** add typed reads (e.g. `GET /cluster/sdn/ipams` + status) plus pvemock support, or at
minimum record the deviation so phase 4 doesn't discover it cold.

### F-03 · MAJOR · T-102 — parser fuzz test is not run in CI (AC4's explicit requirement)
`FuzzParse` exists, is meaningful (accept ⇒ byte-identical render; reject ⇒ typed `*ParseError`;
never panic), and passed a 60s run during this audit — but no workflow or Makefile step runs it
(`grep -i fuzz` over ci.yml and Makefile: nothing). The T-102 commit message ("60s-clean native
fuzz test") reads as done. Zero regression protection for the parser's core invariant.
**Fix:** add a CI step: `go test -run='^$' -fuzz=FuzzParse -fuzztime=60s ./internal/host/`.

### F-04 · MAJOR · T-105 — TOTP criterion not met; misleading provenance claim in doc.go
No TOTP-required fixture user exists anywhere: pvemock's `handleTicket` never reads `otp` and no
`testdata/clusters/*.yaml` user requires one. OTP is exercised only via a stub-identity unit test
(`handlers_test.go:63`). Compounding: `internal/auth/doc.go:44-52` claims the task card "explicitly
sanctions" the stub, quoting wording that does not appear in planning/tasks/phase-1.md.
**Fix:** add OTP support + a TOTP fixture user to pvemock and an integration test; correct the
doc.go comment regardless.

### F-05 · MAJOR · T-101 (root cause T-004) — API-token auth has no success-path integration test
pvemock's `authenticate()` only checks the ticket cookie (documented at pvemock auth.go:139-147),
so AC1's "both auth modes" is met by a stub-server header-shape assertion plus an executable
documentation of the gap (`TestAPIToken_AgainstMockIsRejected`). Honest engineering, but the
criterion as written is unmet and the token path is unvalidated against anything PVE-shaped.
**Fix:** implement `Authorization: PVEAPIToken=` in pvemock and add a success-path test; flag
"needs hardware validation" until validated on real PVE.

### F-06 · MAJOR · T-107 — AC1's required verification artifact was never produced
No Playwright screenshot-baseline test exists (no dependency, config, or test anywhere) and no
documented executed manual checklist was found in docs/ or planning/. What exists —
`threeNodeVlan.render.test.tsx` rendering the real canvas in jsdom against a captured real-backend
fixture, asserting all four layers and toggle behavior — is good substitute evidence for structure,
but visual correctness is unverified and the criterion's explicit alternatives were not delivered.
**Fix:** minimal Playwright run against `make dev` + pvemock, or commit an executed checklist.

### F-07 · MAJOR · T-107 — 60fps pan/zoom measurement never documented
Criterion 5 explicitly says "document measurement". No measurement note exists anywhere in the repo
(only phase-6 T-607 *plans* a perf harness).
**Fix:** perform and commit the measurement (e.g. Chrome tracing over the three-node fixture), or
re-scope to T-607 in a committed report.

### F-08 · MAJOR · T-106 (affects T-107) — `GET /inventory/{ref}` returns no raw source
docs/api.md and the task card promise "raw source (interfaces stanza / PVE API object)";
`EntityDetail` (`internal/topology/types.go:132-138`) substitutes per-field provenance because the
inventory graph doesn't retain original text, and the UI "Raw source" tab therefore shows
provenance, not config (user-guide §2 promises "the raw config behind it"). Deviation is documented
in code comments but two shipped docs still promise the feature.
**Fix:** retain original stanza text / PVE JSON in the inventory graph (T-103 extension) and
surface it, or formally amend docs/api.md + user-guide.

### F-09 · MAJOR · T-105 — capability-matrix fixture personas incomplete
AC3 names root, auditor, sdn-only, vm-user. Fixtures contain only root/auditor/netops (netops also
carries `Sys.Modify`, so it is not sdn-only); there is no vm-user. The two missing personas are
covered only by synthetic privilege lists in a unit test (`caps_test.go:14-22` admits this), never
via real login.
**Fix:** add `sdn-only` and `vm-user` fixture users; extend the pvemock-backed integration test to
all four documented personas.

## Findings — minor

### F-10 · minor · T-105 — pvemock lacks `GET /access/permissions`; production derivation path untested
Integration tests substitute a fixture-reading decorator (`helpers_test.go:62`), so
`pve.Client.Permissions` — the sole data source for authorization — has zero test coverage anywhere.
**Fix:** implement the endpoint in pvemock; cover the full HTTP-path derivation.

### F-11 · minor · T-105 — hourly capability re-derivation implemented but untested
`renewal.go:79` / `DefaultCapRefreshInterval = 1h`; no test drives it.
**Fix:** renewal-loop test with a short `CapRefreshInterval` asserting caps refresh and
keep-old-caps-on-failure.

### F-12 · minor · T-104 — interfaces(5) file is read but never ingested
`hostPollOnce` discards `host.InterfacesFile` (`internal/collect/host.go:43-48`); no
`FromInterfaces` adapter exists, so `SourceHostInterfaces` — the top-precedence source for every
declared field in T-103's merge table — never contributes in production. Documented in-code as a
deferral to T-204, but the three-way reconciliation showcase is only exercised in unit tests.
**Fix:** track as an explicit open work item against the T-204/T-2xx work.

### F-13 · minor · T-104 — golden-test rigor gaps
(a) The "within two poll cycles" bound is not asserted (`waitFor` allows 3s ≈ 60 test cycles);
(b) convergence is `Len() == 35` + spot checks, not full ref-set equality, so an extra+missing pair
could cancel. Also: delta batches can double-report changes made by a concurrent loop inside a
diff window (`loop.go:85-93`, `refresh.go:32-60`) — benign for consumers, but RefreshNow's "exactly
one batch" holds only in isolation and deserves a comment.
**Fix:** full sorted-ref-list comparison in the golden test; comment the delta-attribution caveat.

### F-14 · minor · T-103 — boolean merge fields can't express "not reported"
`vlanAware`/`stp`/`linkUp` use `alwaysSet` (`merge.go:295`), so a source contributing a partial
implicitly "reports" false for flagged bools it didn't set → spurious provenance conflicts.
Currently harmless (`FromPVENetwork` always sets `BridgeVlanAware`), but a future host-interfaces
adapter must populate every flagged bool, or the fields need optional representation.

### F-15 · minor · T-101 — auth-lifecycle deviations
(a) "Short-TTL fixture flag" is a client config knob (`TicketRenewAfter`), not a fixture flag —
pvemock never expires tickets, so renewal-beats-expiry is never validated server-side.
(b) Plaintext password retained in memory for renewal (`auth.go:36-42`) because pvemock doesn't
implement ticket-as-password; in tension with docs/security.md's posture. Documented, memory-only.
**Fix:** pvemock ticket TTLs close (a) for free; implement ticket-as-password renewal with password
fallback for (b) — needs hardware validation.

### F-16 · minor · T-107 — unchecked production cast
`evt as unknown as TopologyDeltaEvent` at `web/src/topology/queries.ts:168` converts a
loosely-typed WS event without runtime validation of the delta payload shape (the outer event is
guarded), against the "no unchecked casts" ground rule. Everything else in web/src is clean.
**Fix:** type-guard the payload like `client.ts` does for error envelopes.

### F-17 · minor · T-106 — status derivation and filters untested
Degraded-bond / link-down derivation and the `missing-slave` badge (`project.go:260-352`) are never
exercised — golden fixtures only produce `ok`/`unknown`, yet these badges are named in AC1. The
server-side `layers=`/`node=` filters and API query-param parsing are also untested (only `vlan`
has a test).
**Fix:** table-test `statusOf`/`bondStatus` (down NIC, missing slave, inactive slave, degraded
zone); add filter cases + one API-level query-param test.

### F-18 · minor · T-107 — stale auth stub and missing spec §5 state
(a) Demo-mode auth bypass still enabled by default (`web/src/store/authStub.ts:12`,
`VITE_AUTH_STUB !== "false"`) though real auth landed; `web/README.md` still says auth "lands in
T-105". Client-side only — backend still 401s — but stale scaffolding.
(b) Topology spec §5's peer-unreachable state (greyed band + staleness banner) is not implemented
and no deviation is recorded; partly blocked on `/topology` not exposing per-node staleness.
**Fix:** flip the stub default and refresh the README; surface collector staleness per node and
render the banner, or record the deferral.

### F-19 · minor · T-102 — ethtool deviation with misquoted justification
Implementation is SIOCETHTOOL ioctl + sysfs fallback instead of the card's "netlink ethtool
preferred, exec fallback". The engineering rationale (ethtool.go:12-38) is sound, but the comment
quotes task-card wording ("exec-based ethtool fallback acceptable — document which you chose") that
does not appear in phase-1.md — the deviation reads as pre-authorized when it wasn't.
**Fix:** reword the comment to own the deviation; keep the implementation.

### F-20 · info — test-coverage odds and ends
`Permissions()` and `GetTaskLog()` have no in-package tests in `internal/pve`; `withJitter`/
`backoffFor` in collect are only exercised indirectly; fixture host-reader leaves per-port
VLANs/FDB empty (documented). PUT bodies sent as JSON are mock-validated only — bundle into the
"needs hardware validation" list along with real-PVE `/access/permissions` response shape and
ticket-as-password renewal.

### F-21 · info — no completion reports for phase-0/1 tasks
`planning/reports/` holds only T-201/T-202/T-204, yet phase-1 code comments repeatedly defer to
"the completion report" for decisions (layer vocabulary, raw-source meaning, pvemock gaps). Those
decisions currently live only in scattered code comments.
**Fix:** backfill reports or fold the decisions into docs/ — and treat comment-claims-card-said-X
as a review checkpoint going forward (three instances found: F-01, F-04, F-19).

---

# Remediation appendix (2026-07-10)

All findings addressed across four fix waves (phase-0/infra, pvemock+auth+pve-client,
inventory+collect, topology backend, web UI). Verification: full `make check` green including
`-race` on every touched package, the full 30s inventory stress soak, and the new opt-in Playwright
e2e suite (2/2). Details in `planning/reports/audit-remediation.md`; decisions of record in
`planning/reports/phase-0-1-decisions.md`; open hardware-only items in
`planning/reports/needs-hardware-validation.md`.

| Finding | Outcome |
|---|---|
| F-01 departed nodes never retired | **Fixed** — `retireDepartedNodes` diffs cluster membership and issues empty scoped ApplyPolls per PVE source; false comment corrected; `TestDepartedNodeRetired` asserts exact 25-ref survivor set |
| F-02 IPAM reads missing | **Fixed** — typed `ListIPAMs`/`GetIPAMStatus` + pvemock routes + fixture data (three-node-vlan, evpn-lab) + integration tests; doc.go enumeration updated |
| F-03 fuzz not in CI | **Fixed** — parallel `fuzz` job in ci.yml (`-fuzztime=60s`) |
| F-04 TOTP unmet + fabricated provenance | **Fixed** — pvemock OTP check + `totp-user@pve` fixture + `TestIntegration_TOTPLoginAgainstMock`; doc.go's false "card sanctions" claim replaced with an honest historical note. Real two-step NeedTFA flow: needs hardware validation |
| F-05 token auth no success path | **Fixed** — pvemock accepts `PVEAPIToken` header (fixture-declared tokens); `TestAPIToken_FullReadSurfaceAgainstMock` |
| F-06 no render-verification artifact | **Fixed** — Playwright screenshot-baseline e2e (`web/e2e/topology.spec.ts`, real stack + real login, committed baseline); opt-in `npm run e2e`, deliberately not in `make check`; docs/testing/topology-render-verification.md |
| F-07 60fps measurement undocumented | **Fixed (measured honestly)** — docs/testing/topology-performance.md: headless VM measures ~35fps mean with a 60fps idle control (environment floor, not verdict); dev-machine re-run remains on the hardware-validation list |
| F-08 raw source not returned | **Fixed** — inventory retains per-source raw text (verbatim stanza / PVE JSON); `EntityDetail.rawSource` pinned in docs/api.md; inspector Raw source tab renders it (provenance moved to its own tab) |
| F-09 personas incomplete | **Fixed** — `sdn-only@pve` + `vm-user@pve` fixtures; capability-matrix integration test logs in all four personas |
| F-10 /access/permissions missing in mock | **Fixed** — implemented (flat list at `/`, shape caveat documented); production identity factory now used unmodified in all auth integration tests; `TestPermissions_AgainstMock` |
| F-11 cap re-derivation untested | **Fixed** — `TestRenewalLoop_RederivesCapabilitiesOnInterval` (incl. keep-old-caps-on-failure) |
| F-12 interfaces file not ingested | **Fixed** — `FromInterfaces` adapter (bridges/bonds/vlans/OVS, multi-stanza folding, netmask canonicalization); wired into hostPollOnce; three-way reconciliation test |
| F-13 golden/delta rigor | **Fixed** — full sorted 35-ref-set comparison; double-attribution windows documented in code |
| F-14 booleans can't express unreported | **Fixed** — `VlanAwareSet`/`STPSet`/`LinkUpSet` companions; unset = no win/no conflict; merge contract comment updated; UI renders unknown |
| F-15 ticket TTL / plaintext password | **Fixed** — pvemock ticket TTLs (renewal validated against genuine server-side expiry); ticket-as-password renewal with password fallback, plaintext dropped after first success. Real-PVE acceptance window: needs hardware validation |
| F-16 unchecked cast | **Fixed** — real type predicate validating the full delta payload; cast removed; tested |
| F-17 status derivation/filters untested | **Fixed** — statusOf/bondStatus/badgesOf table tests (down/degraded/missing-slave/unreported→unknown); layers=/node= projection tests + API query-param test |
| F-18 stale stub / staleness state | **Fixed** — stub default flipped to opt-in (`VITE_AUTH_STUB=true`), README refreshed; `/topology` staleness section (3-consecutive-failures rule, pinned in docs/api.md) + banner and band-greying in the UI with captured stale fixture |
| F-19 misquoted ethtool justification | **Fixed** — comment rewritten as an owned, honestly-labeled DEVIATION; implementation kept |
| F-20 coverage odds and ends | **Fixed** — `TestGetTaskLog_AgainstMock`, `TestPermissions_AgainstMock`, `TestWithJitter`, `TestBackoffFor`; hardware-only items consolidated in needs-hardware-validation.md |
| F-21 decisions only in comments | **Fixed** — planning/reports/phase-0-1-decisions.md (decisions of record + comment-hygiene rule); needs-hardware-validation.md created |

Additional fix found during remediation: `GET /inventory/{ref}` rejected percent-encoded refs
(chi wildcard params keep their encoding; `encodeURIComponent`'d `:` → 400). Handler now
path-unescapes before `ParseRef`; `TestInventoryDetailRoute_PercentEncodedRef`.
