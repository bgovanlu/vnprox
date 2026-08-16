# Phase 29 — Make v4.0 true

**Roadmap:** [`docs/roadmap-earned.md`](../../docs/roadmap-earned.md) ·
**Plan:** [`../implementation-plan-earned.md`](../implementation-plan-earned.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/security.md`, `docs/api.md`.

This phase exists because the 2026-08-15 audit found that several things v4.0.0 claims are
not true in production, and several things `docs/security.md` documents as guarantees have
holes. Every card here either makes a shipped claim true or closes a verified gap. None of
them adds a feature.

---

## T-2901 · Un-break the PWA and the embeds ★

**kind:** implementation · **depends on:** —
**context:** `internal/api/middleware.go`, `internal/api/embed.go`, `web/public/sw.js`,
`web/public/manifest.webmanifest`, `web/src/push/registerServiceWorker.ts`,
`docs/security.md` §CSP, `docs/features/monitoring.md` §11

`securityHeadersMiddleware` (`internal/api/middleware.go:84`) ships the T-604 policy whose
comment asserts the SPA has "no Worker()/service worker … and no web app manifest". T-2005
made both false: `web/public/sw.js` is registered on every load and `manifest.webmanifest`
exists. Under `worker-src 'none'; manifest-src 'none'` a production browser refuses both —
**the installable app and push notifications shipped in v4.0.0 cannot work**. Separately, the
three `/embed/*` views exist to be iframed (`docs/security.md:34`) and the global
`frame-ancestors 'none'` + `X-Frame-Options: DENY` forbid exactly that.

- Change `worker-src` and `manifest-src` to `'self'`. Nothing else in the policy widens.
  Rewrite the stale comment to describe the actual policy and why.
- Per-route headers for `/embed/*`: drop `X-Frame-Options`, set `frame-ancestors` from a new
  `[server] embed_frame_ancestors` config list, **default `'self'`**. Empty/absent config means
  same-origin embedding only; the operator opts into specific origins. Document in
  `docs/security.md` and `docs/deployment.md`.
- The service worker's existing invariant is untouched: it never caches `/api/*`.

**Acceptance**

1. A header-level test asserts the **full** CSP string on an app route (worker/manifest
   `'self'`, everything else unchanged) — the exact header, not substring matches.
2. A Playwright spec loads the app in Chromium and asserts `navigator.serviceWorker.ready`
   resolves and the registration is active, and that the manifest link resolves with 200 and
   `Content-Type` a manifest type. (This is the assertion that would have caught the defect.)
3. An `/embed/map` response carries no `X-Frame-Options` and a `frame-ancestors` matching
   config; an app route still carries `DENY` + `frame-ancestors 'none'`. A Playwright spec
   renders an embed inside a same-origin iframe.
4. With `embed_frame_ancestors = ["https://wiki.example"]`, the emitted directive is exactly
   `frame-ancestors 'self' https://wiki.example`; a malformed origin in config fails startup
   with a message naming the key and value.
5. `docs/security.md`'s CSP section and the embed paragraph describe the new behavior;
   the monitoring feature doc's §11 no longer needs a caveat.
6. Real-device verification (installability on iOS/Android, push via FCM/APNs/autopush) is
   recorded in `planning/reports/needs-hardware-validation.md` as the remaining open half,
   referencing this card.

---

## T-2902 · Peer host-write safety parity + audit attribution ★

**kind:** implementation · **depends on:** — · **migration:** `0047`
**context:** `internal/peer/server.go` (`/host/stage-interfaces`, `/host/ifreload`,
`/host/restore`, `/host/discard-staged`, `/host/lldp/install`), `internal/change/protected.go`,
`internal/change/service.go` (audit append), `internal/store/audit.go`,
`internal/auth/handlers.go` (`clientIP`), `docs/security.md` §audit, §change-engine

`POST /api/peer/host/stage-interfaces` decodes `{node, content}` and hands it straight to the
host writer (`internal/peer/server.go:946`) — no protected-interface detection, no safety
validation, no audit row, no user attribution. Anything holding the cluster secret rewrites
`/etc/network/interfaces` cluster-wide, bypassing every interlock the product documents as
absolute. The interlocks live only on the coordinating node.

- Enforce protected-path safety validation (`DetectProtected` and friends) on the **receiving**
  side of every `/api/peer/host/*` write before the writer is invoked. The coordinating node's
  validation remains; this is defense in depth, not a replacement.
- Distributed rollback must keep working: a restore whose content re-arms the management path
  from a snapshot this node previously staged is legitimate. Exempt by provenance (the restore
  references a known staged/snapshot artifact on the receiving node), never by skipping
  validation wholesale.
- Thread originating attribution through the peer envelope: acting username and coordinating
  node, plus the coordinator's client IP where one exists. Append an audit row on the
  **receiving** node for every host write (stage/reload/restore/discard/lldp-install),
  including refusals.
- Migration `0047`: nullable `ip` column on the audit table. Thread `clientIP(r)` into every
  mutating `/api/v1` handler's audit append (the API layer owns extraction; the change engine
  passes it through). `docs/security.md:451` — "user, source IP, changeset id, op summaries,
  result" — becomes true.
- Update `docs/data-model.md` (audit shape) and the peer-protocol section of
  `docs/architecture.md` in the same commit.

**Acceptance**

1. A peer `stage-interfaces` body that removes the management path is refused with an error
   naming the interlock; the refusal is audited on the receiving node. The same content
   through the coordinating change engine is refused identically (parity, asserted in one
   table-driven test).
2. A legitimate distributed-rollback restore (provenance-exempt) still succeeds end-to-end in
   the multi-node mock harness; a "restore" of content the node never staged is validated like
   any write.
3. Every `/api/peer/host/*` write produces exactly one audit row on the receiving node with
   action, result, origin node, acting user, and content digest — asserted for success and
   refusal.
4. Migration `0047` adds the nullable `ip` column; `versionSeeds` gains a seed for the new
   version; the from-each corpus passes unmodified.
5. Mutating `/api/v1` requests produce audit rows whose `ip` matches the test client;
   pre-existing rows read back with empty IP.
6. No new write path: the peer routes still reach the host only through the existing writer;
   grep-level assertion (or type-level, mirroring `internal/mcp/stageonly.go`) that nothing
   else acquired the writer.

---

## T-2903 · Bearer tokens honor `read_only`; token expiry; CSRF constant-time

**kind:** implementation · **depends on:** wave 1 merged · **migration:** `0048`
**context:** `internal/auth/middleware.go` (`authenticateBearer`, CSRF check),
`internal/auth/handlers.go` (`forceReadOnly`), `internal/auth/tokens.go`,
`internal/store` (tokens table), `docs/security.md:318`, `docs/api.md` §tokens

`forceReadOnly` is applied only on the cookie-session path (`internal/auth/handlers.go:221`).
The bearer path builds `Caps` straight from stored scopes (`internal/auth/middleware.go:176`),
so a write-scoped token minted before `read_only = true` keeps full write capability after a
restart — breaking the exact invariant `docs/security.md:318` documents. Tokens also never
expire, and the CSRF token is compared with `!=` (`middleware.go:218`) in a codebase whose
stated convention is constant-time comparison everywhere.

- Apply `forceReadOnly` to bearer-derived caps in `authenticateBearer` when the deployment is
  read-only.
- Migration `0048`: nullable `expires_at` on the tokens table. Mint accepts optional
  `expiresAt`; **default 90 days** for newly minted tokens; explicitly `null` (documented) for
  non-expiring where the operator insists. Existing tokens get no retroactive expiry. Expired
  tokens are refused like revoked ones and swept from `last_used` bookkeeping.
- Aggregate `token.use` audit rows: per token, one row per UTC hour (first use stamps it, a
  counter updates it) instead of one per request. The row shape stays within the existing
  audit contract.
- CSRF comparison via `subtle.ConstantTimeCompare`.
- `docs/api.md` token section documents `expiresAt` (request + response) and the default;
  `docs/openapi.json` updated in the same commit; release-note text explains rotation.

**Acceptance**

1. In a `read_only = true` deployment, a `netWrite`-scoped bearer token gets 403 on a mutating
   route and 200 on reads — proven by a test that flips the flag against the same stored token.
2. An expired token is refused with the same error shape as a revoked one; a token with
   `expires_at` in the future works; minting without `expiresAt` yields one 90 days out.
3. Migration `0048` + `versionSeeds` seed; from-each corpus passes unmodified.
4. N requests with one token inside an hour produce one `token.use` row with count N.
5. CSRF check uses `ConstantTimeCompare`; behavior identical for match/mismatch/empty.
6. Contract check: `docs/openapi.json` diff is additive-only.

---

## T-2904 · Hub plugin install hardening

**kind:** implementation · **depends on:** —
**context:** `cmd/vnproxd/hubinstall.go` (`buildRegistration`), `internal/api/hub.go`
(`trustUnsigned` gate), `internal/plugin/registry`, `internal/config`,
`docs/hub-registry.md`, `docs/security.md` §plugins

`buildRegistration` runs `exec.Command(m.Endpoint)` on a string delivered in a registry
manifest — a bare name resolves via `$PATH` — and the signature requirement is bypassable by
`trustUnsigned: true` **in the request body** (`internal/api/hub.go:348`). Net: a PVE user
with `Sys.Modify` on a registry-configured deployment can reach arbitrary root process
execution.

- Constrain the endpoint: must be an absolute path inside a vnprox-owned install root
  (default `/var/lib/vnprox/plugins`); containment asserted on fully resolved paths
  (`filepath.EvalSymlinks` on both root and candidate); reject relative names, `..`, paths
  outside the root, and non-regular files. No `$PATH` resolution ever.
- Move unsigned-trust to config: `[hub] trust_unsigned = true` with a startup `WARN` naming
  the knob (the `[peer] tls_trust` precedent). The request field stays schema-valid but is no
  longer sufficient: request `trustUnsigned` **and** config off → refusal whose error says the
  server config forbids it. Signature verification itself is untouched and never optional for
  signed artifacts.
- Capability gates stay as they are; this card narrows what a capability can reach, it does
  not re-gate.

**Acceptance**

1. Manifest endpoints `foo`, `./foo`, `/usr/bin/foo`, `<root>/../foo`, and a symlink escaping
   the root are all refused with errors naming the constraint; an executable regular file
   inside the root launches.
2. Unsigned artifact + request `trustUnsigned: true` + config off → refused, audited, error
   names the config key. Config on → startup WARN emitted (asserted), install proceeds through
   the existing unsigned-audit path.
3. A signed artifact with a bad signature is refused regardless of any trust flag —
   re-asserted here so the never-optional invariant has a fixture in this card too.
4. `docs/hub-registry.md` and `docs/security.md` describe the install root and the config
   knob; API schema unchanged (additive-only check passes).

---

## T-2905 · Auth and daemon hardening punch list

**kind:** implementation · **depends on:** wave 1 merged; runs beside T-2903 —
**does not touch** `internal/auth/middleware.go` or `internal/auth/tokens.go` (T-2903 owns them)
**context:** `internal/auth/ratelimit.go`, `internal/auth/renewal.go`, `internal/auth/service.go`,
`internal/store/sessions.go`, `internal/api/webhooks.go`, `internal/automation/dispatcher.go`,
`cmd/vnproxd/server.go`, `internal/mtuprobe/prober.go`, `internal/api/wan.go`,
`internal/api/guestinterior.go`, `internal/config`, `packaging/Makefile`, `packaging/systemd/`,
`packaging/debian/postinst`

The remainder of the 2026-08-15 security audit. Each item is bounded and mechanical; if one
grows a design question, split it out and report rather than absorb.

1. **Session lifecycle:** periodic sweeper deletes rows past idle/hard expiry;
   `renewAndRefreshOne` drops (never renews) a session past the 12h hard cap; the in-memory
   `live` map is pruned with it. The push-subscription cascade documented at
   `docs/security.md:82` now actually has the sweep it relies on.
2. **Login limiter:** TTL sweep / size cap on `tokenBucket.buckets` (mirror
   `replayCache.seenBefore`); charge the per-username bucket only after the per-IP bucket
   passes, so unauthenticated strangers cannot lock out a known username globally.
3. **Webhook SSRF guard:** refuse loopback, RFC1918, link-local (v4+v6), and metadata ranges
   by default; `[webhooks] allow_private_targets = true` opts out with startup WARN; require
   `https` unless `allow_insecure_targets` (separate knob, also WARN). Applied at validation
   **and** at dispatch (DNS may change between the two).
4. **HTTP server timeouts:** `ReadTimeout`/`WriteTimeout`/`IdleTimeout` on the :8007 server,
   with the WebSocket route exempted from `WriteTimeout` via the hijack path — prove with a
   long-lived WS test.
5. **MTU prober:** `--` end-of-options guard in the `ping` argv (parity with
   `internal/latmesh/prober.go:89`); validate `PUT /wan/targets` host as IP or hostname.
6. **Config file permissions:** install `vnprox.toml` 0640 and chmod in `postinst`
   (it can carry `dev_ticket_password`).
7. **Dev-knob visibility:** startup `WARN` naming each active dev knob
   (`dev_login_rate_*`, `dev_ticket_*`), matching the `fwlog`/`server` precedent.
8. **Guest-interior toggle:** gate the `PUT` on a write capability (`netWrite`), not
   `netRead`; document in `docs/api.md` (permission change, called out in release notes).
9. **systemd:** add `UMask=0077`, `RestrictNamespaces=~CLONE_NEWUSER`; **verify whether
   `nsenter`/`setns` still works under `SystemCallFilter`** — if the guest-interior path is
   broken under the shipped unit, fix the filter with the narrowest allow and record it; if it
   cannot be verified without hardware, record in `needs-hardware-validation.md`.

**Acceptance**

1. Expired sessions disappear without being touched; a session at hard cap stops renewing —
   both under the fake clock the auth tests already use.
2. Limiter memory is bounded under a flood of distinct usernames (asserted via bucket count),
   and a spray against one username from many IPs no longer locks out that username's real IP.
3. Webhook targets `127.0.0.1`, `10.0.0.1`, `[fe80::1]`, `169.254.169.254`, and `http://`
   public hosts are refused by default; each opt-in knob admits its class and WARNs at startup.
4. Slow-loris body against an API route is cut at the deadline; a WS connection survives past
   `WriteTimeout`.
5. A `-` prefixed probe target reaches `ping` as an operand, not an option (argv-level test).
6. Packaged config mode is 0640 (packaging test or postinst assertion), and each active dev
   knob emits exactly one startup WARN.
7. `PUT /guests/{ref}/interior-toggle` refuses a read-only session.
8. `make deb` output passes; the systemd analysis (`systemd-analyze security` locally if
   available, else directive-level assertions) is recorded in the report.

---

## T-2906 · Documentation truth pass + single doc index

**kind:** docs · **depends on:** — (runs in wave 1; must not edit sentences whose truth
T-2901/2902/2903 are changing — leave those to their cards)
**context:** the 2026-08-15 docs audit (in the arc roadmap §"What the audit found"),
`docs/project-status.md`, `docs/status-matrix.md`, `docs/datasheet.md`, `README.md`,
`docs/README.md`, `docs/_sidebar.md`, `docs/deployment.md`, `CHANGELOG.md`,
`docs/roadmap-*.md`, `planning/tasks/phase-2*.md`, `planning/reports/`

Make the documentation stop lying by omission, without rewriting history.

- `docs/project-status.md`: rewrite §1–§5 for v4.0.0 (release, arc table with an Arc 5 row and
  Arc 4 closed, phase-21 line); collapse the §6–§9 snapshot accretion into one current-state
  section, keeping dated history beneath it; correct "GitHub Actions IS running".
- `docs/status-matrix.md`: header to v4.0.0; fix line ~143 CI row and the §5.7 double-negation
  (state plainly: disabled since 2026-08-13, billing); refresh rows 72–77 (doctor, PWA,
  contract, apt-repo tooling); mark T-2505-followup-02 fixed 2026-08-14 with the corrected
  root cause (undefined queryFn sentinel, not missing invalidation). Adding the ~24 missing
  Arc-5/v4.0 rows is **in scope as a stub section** listing the areas with `●/◐/B` marks and a
  pointer, not a full 77-row re-audit.
- `docs/datasheet.md`: version/date header, route count marked "as of v3.0.4" or recounted,
  "not yet shipped" list corrected (PWA, compat matrix, doctor shipped; registry/demo/apt
  hosting still not).
- `README.md`: "Shipped through v3.5.0" → v4.0.0; status paragraph names Arc 5 + Arc 6.
- **On the record:** T-2006 (i18n) and T-2102 (apt-repo hosting) were not delivered by
  v4.0.0's "phases 20 and 21 complete"; both are rescheduled (T-3106, T-3301). Add a dated
  correction note under CHANGELOG 4.0.0 (do not rewrite the entry) and fix
  `project-status.md`/`status-matrix.md` accordingly.
- Roadmap set: status lines of `roadmap-next/-universal/-proven/-leverage/-adopted` marked
  shipped (with release + "v3.1/v3.2/v3.3 were never tagged" notes at the mapping points);
  `CHANGELOG.md:7-11` preamble maps 3.5.0/4.0.0 to their roadmap docs.
- Indexing: `docs/README.md` and `docs/_sidebar.md` gain Specs (the nine `docs/features/*.md`),
  Roadmaps (all seven), and entries for `status-matrix.md`, `security-verification.md`,
  `development.md`, `hub-registry.md`; reconcile the two indexes to the same link set.
- `docs/deployment.md`: add "Upgrading a v3.x install to v4.0" (ordinary package upgrade,
  migrations through 0046); extend the migration list that stops at 0034; fix the "pre-v3.2
  daemon" phrasing (version never tagged).
- `planning/reports/`: create `T-2505-followup-01.md` and `-02.md` (both are referenced by
  other docs; content from phase-25.md's inline records). Add inline delivery-record sections
  to `planning/tasks/phase-2{1,2,3,6,7,8}.md` following the phase-24/25 pattern — one honest
  paragraph per card from CHANGELOG/git, not 57 retro-written reports.
- CI claims in `docs/security.md:468` and `docs/security-verification.md:76`: state the gate
  is `scripts/ci-local.sh` on the dev host while workflows are disabled. Touch nothing else in
  `docs/security.md`.

**Acceptance**

1. Grep gates the card can state: no doc outside CHANGELOG/history sections claims v3.0.4 or
   v3.5.0 is the latest release; no doc asserts GitHub Actions currently runs; the strings
   `v3.1`, `v3.2`, `v3.3` appear only alongside a never-tagged note.
2. Both indexes list the same link set; every `docs/*.md` and `docs/features/*.md` is
   reachable from at least one of them.
3. The two `T-2505-followup` files exist and match what cites them.
4. Phase files 21/22/23/26/27/28 each carry a delivery-record section naming every card.
5. `make check` passes (docs changes can still break help `docRef` checks and link gates).

---

## Delivery record (2026-08-15)

The four wave-1 implementation agents were killed mid-flight by an API spend
limit; T-2901/T-2904/T-2906 partials were reviewed, verified, and completed
by the orchestrator, and T-2902/T-2903/T-2905 were implemented by the
orchestrator directly. Gate discipline unchanged: `make check` green on every
commit, new browser-level behavior exercised in real Chromium.

**T-2901 — shipped** (wave-1 commit). CSP `worker-src`/`manifest-src` →
`'self'`; per-route `/embed/*` frame relaxation behind
`[server] embed_frame_ancestors` (origin-validated, default same-origin);
`.webmanifest` mime registration; `web/e2e/pwa.spec.ts` (3/3 in real
Chromium against a fresh stack). Bonus deliverable forced by the
status-matrix reconciliation gate: `vnproxctl verify` check `pwa.servable`
(row 73) with pass + fail fixtures — run against a live daemon it detects
exactly the defect class that shipped in v4.0.0. Two findings from the spec
run worth keeping: Chromium's service-worker fetch ignores Playwright's
`ignoreHTTPSErrors` (needs `--ignore-certificate-errors`, a test-env
accommodation), and same-origin iframing is impossible BY DESIGN (every
page pins `frame-src 'none'`) — the embedding consumer is an external
origin, so AC3's iframe assertion became headers + session-free top-level
render, recorded as a deviation. Real-device install/push remain open in
`needs-hardware-validation.md` §T-2901.

**T-2902 — shipped** (wave-1 commit). Receiving-side validation via
`change.Service.ValidatePeerHostWrite` (parity with the local raw-op
pipeline asserted in one table); snapshot-provenance exemption for
distributed rollback (validation *never consulted* in the exempt case —
asserted); receiving-side audit rows with wire-carried attribution;
migration 0047 (`audit_log.ip`) + client-IP threading through
API→change/auth appends and `GET /audit`. Deviation: `ip` is
`NOT NULL DEFAULT ''` rather than nullable — same compatibility property,
matches the store's `''`-means-absent convention.

**T-2903 — shipped** (wave-2 commit). `forceReadOnly` on the bearer path;
migration 0048 (`api_tokens.expires_at`, 90-day default, explicit-`null`
opt-out, never retroactive); expired ⇒ same 401 as revoked; CSRF compare →
`subtle.ConstantTimeCompare`. **AC4 amended in delivery:** audit rows are
append-only by design (no Update exists), so "one row with count N" became
one row per token per UTC hour at FIRST use (prompt visibility), with the
finished previous hour's count carried in the next row's
`prevHourCount` — same bound, honest mechanics.

**T-2905 — shipped** (wave-2 commit). Session sweep (`DeleteExpired` +
renewal-loop hook; 0046 CASCADE to push subscriptions proven in-test) and
hard-cap renewal stop; limiter map sweep + 65k ceiling (IP-before-username
charging already existed — audit item was stale on that half); webhook
destination policy (`automation.TargetPolicy`: registration check + dial-time
resolved-address re-check, `[webhooks]` knobs, startup WARNs); server
Read/Write/Idle timeouts (WS unaffected post-hijack); mtuprobe `--` guard +
WAN host validation; config 0640 + postinst upgrade fix; dev-knob startup
WARNs; `PUT /guests/{ref}/interior-toggle` → `netWrite` + CSRF; systemd
`UMask=0077` + `RestrictNamespaces=~user`. The audit's "nsenter probably
broken under the syscall filter" suspicion was **refuted by inspection**:
`setns` is in `@process` ⊂ `@system-service` and in no denied group
(`systemd-analyze syscall-filter`), recorded as a comment in the unit.

**T-2904 — shipped** (wave-1 commit). Endpoint containment
(`resolvePluginEndpoint`, 10-case table incl. symlink escape),
config-gated `trustUnsigned` with startup WARN; signed-artifact
verification untouched and re-asserted.

**T-2906 — partial at wave 2.** `project-status.md` + `status-matrix.md`
rewritten for v4.0.0 (agent partial, reviewed; one overstated cell — Arc 4
"26/26" — corrected to 24/26 with the two reschedules named). Remaining
scope (datasheet, README, roadmap status lines, index reconciliation,
deployment upgrade section, followup report files, phase delivery records
for 21/22/23/26/27/28) is open and tracked; the CI-claims corrections in
`security.md`/`security-verification.md` are also still open.

---

## `T-2905-bug-01` · `a11y.spec.ts` "Switch view" is start-timing dependent (open)

**Found:** 2026-08-15, during wave-2 verification. **Not caused by wave 2** — proven below.

`a11y.spec.ts:165` ("axe: Topology (Switch view, the default)") waits up to 90s for a
`.grayscale` element via `waitForAStaleEntity`. Whether that element ever appears depends on
**how fast the daemon starts**, not on the code under test:

| Code | Daemon start path | Result |
|---|---|---|
| pre-wave-1 (`544219e`) | `go run` (no prebuilt binary) | pass ×2 |
| wave-1 (`666234d`) | `go run` | pass ×2 |
| **wave-1 (`666234d`)** | **prebuilt binary** | **FAIL** |
| wave-2 (full tree) | prebuilt binary | FAIL ×4 |
| wave-2, `internal/auth`+`internal/store` only | prebuilt binary | FAIL |
| **wave-2 (full tree)** | **`go run`** | **pass ×2** |

The mechanism: the fixture's pve2/pve3 peers are unreachable, so their host collectors need
three consecutive failures (~15s at `host_interval = 5s`) before `/topology` reports them
stale. With `go run`, compilation delays daemon readiness enough that staleness already
exists when the browser first paints. With a prebuilt binary — **which is what
`scripts/e2e-shards.sh` always does, so this is the path `make e2e` takes** — the page loads
first, and the stale transition must then arrive at an already-rendered page. It does not
arrive within 90s.

Independently verified NOT to be the cause (each disabled in isolation, clean ports, failure
persisted): the T-2901 CSP change (service worker now registering), the T-2905 HTTP server
timeouts, and the T-2905 session sweep. Also verified: the **product is fine** — driving the
real stack manually with the full wave-2 daemon, `.grayscale` renders within 10s of page load
and `GET /topology` reports `staleness.stale=true` with `host:pve2`/`host:pve3` stale, which
is byte-identical to what the wave-1 daemon reports.

So this is a **test defect, not a product defect**, and it is latent in `main` today: the
flake ledger already lists this test at 11% (1/9). What changed is that the dev host now
builds fast enough to lose the race consistently.

**Fix (not attempted here — it belongs to whoever owns T-3204's test-debt sweep):**
`waitForAStaleEntity` should make staleness deterministic rather than race it — e.g. drive
the collector clock, seed an already-stale fixture, or assert on the API's `staleness`
payload (which is correct and immediate) instead of a CSS class that depends on when the
browser happened to connect. Do **not** simply raise the 90s timeout: the wait is for a
transition that has already happened, so a longer wait does not make it arrive.

**Investigation cost note for the next agent:** three of the intermediate experiments in this
investigation silently died on port collisions with a leftover manual probe stack and were
briefly mistaken for real failures. When A/B-ing e2e behavior, always assert
`grep -c "already used"` on the run log before believing its verdict.
