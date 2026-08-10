# Phase 28 — Adoption

**Roadmap:** [`docs/roadmap-adopted.md`](../../docs/roadmap-adopted.md) ·
**Plan:** [`../implementation-plan-adopted.md`](../implementation-plan-adopted.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/data-model.md`.

`project-status.md` observes that "nothing external can consume vnprox until some of [Phase 21]
lands". Phase 21 is the distribution mechanism. This phase is the reason someone would want it:
a way to try the product without a cluster, a place to get extensions, and the operator surfaces
that make a large feature set usable by more than one person at a time.

The first three cards are about people who have never run vnprox. The last five are about the
people who already do.

---

## T-2801 · One-command install and built-in demo mode ★

**kind:** implementation · **depends on:** T-2505
**context:** `packaging/`, `internal/pvemock/`, `docs/deployment.md`, `T-2102` (apt repository)

Evaluating vnprox currently requires a Proxmox cluster. That is a hard gate in front of every
potential user, including the ones who would validate it on hardware we do not have — which makes
it a gate in front of `T-2501` too.

- `curl -fsSL <url> | sh` detects the platform, verifies a signature, installs from the signed apt
  repository where available and falls back to a binary tarball. **Signature verification is not
  skippable**; there is no `--insecure`.
- `vnproxd --demo` runs against an embedded synthetic cluster — a realistic multi-node topology
  with SDN zones, guests, findings, drift, and flows — with **no PVE and no network access**.
- Demo mode is unmistakable: a persistent banner, a distinct accent colour, and every write path
  is a no-op that reports what it would have done.
- The demo dataset is a fixture in the repository, so it is versioned, reviewable, and usable as a
  test corpus rather than being generated at runtime.

**Acceptance**

1. `--demo` starts with no PVE reachable and no outbound network, and the topology, findings, and
   flow screens all render populated — asserted end-to-end in the e2e suite, not by unit test.
2. Every mutating API in demo mode returns a "would have" result and touches nothing; a store
   checksum before and after a full staged-and-applied changeset is unchanged.
3. Demo mode cannot be enabled against a real PVE endpoint, and a real endpoint cannot be
   configured while in demo mode. Both directions refused with a named error.
4. The installer refuses a bad signature and exits non-zero; asserted against a deliberately
   corrupted artifact.
5. The installer is idempotent — running it twice leaves the same versions and no duplicate
   sources entry.
6. The demo banner is present on every screen, asserted by the e2e suite sweeping all routes.

## T-2802 · Hosted read-only demo and guided tour

**kind:** implementation · **depends on:** T-2801
**context:** `T-2801`'s demo dataset, `web/src/onboarding/`, `docs/datasheet.md`

The demo dataset from `T-2801`, published, turns a datasheet into something clickable. The
existing onboarding walkthrough is the tour engine; it needs a script written against the demo
data rather than against a real cluster.

- Public instance serving demo mode with all writes disabled at the edge, not merely in the UI.
- A scripted tour covering the six surfaces the datasheet leads with, resumable and skippable.
- Rate-limited and resource-capped; a hostile visitor cannot degrade it for others.

**Acceptance**

1. Every mutating route returns 403 at the edge, asserted by driving the API directly rather than
   the UI, across the full route list from `docs/openapi.json`.
2. The tour completes end to end in the e2e suite without a real cluster.
3. Session state is per-visitor; one visitor's layout changes are invisible to another.
4. Resource caps are enforced and exceeding them degrades that session only.

## T-2803 · Hosted signed registry for blueprints and plugins

**kind:** implementation · **depends on:** —
**context:** `internal/hub/`, `internal/blueprint/`, `internal/plugin/`, `T-2104`

`internal/hub` is a complete signed-registry **client** with no registry to talk to, and the
plugin SDK has five extension points and nowhere to publish to. The client's signature
verification is already the security boundary; the server is mostly hosting and index generation.

- Static, signable index served from object storage or GitHub Pages — no bespoke service to
  operate, matching the `T-2102` decision.
- Publisher tooling: `vnproxctl hub publish` signs and submits; submissions are reviewed before
  indexing.
- Revocation: a revoked artifact is refused by the existing client, and revocation is checkable
  offline from the signed index rather than requiring a live call.

**Acceptance**

1. The existing `internal/hub` client consumes the real index **unmodified** — if the client needs
   changing, the index format is wrong.
2. An artifact signed by an untrusted key is refused by the client; one signed correctly installs.
3. A revoked artifact is refused, and the revocation is honoured with no network access beyond the
   already-fetched signed index.
4. Publishing is idempotent; the same artifact published twice yields one index entry.
5. A corrupted index fails verification rather than partially loading.

## T-2804 · Incident mode

**kind:** implementation · **depends on:** T-2704
**context:** `internal/diagnose/`, `internal/capture/`, `internal/findings/`, `internal/flow/`,
`internal/backup/` (support bundle)

When a network breaks, an operator needs the diagnosis ladder, a capture, the current findings,
recent flows, and what changed — and today they must open five screens and correlate by hand,
under time pressure, which is exactly when hand-correlation fails.

Every component exists. The assembly does not.

- "Start incident" opens a timeline that records, from that moment: findings as they appear and
  clear, changesets staged or applied, diagnosis ladder runs, captures, and the `T-2704` diff from
  the incident's start.
- Annotations with timestamps, so an operator's own observations sit on the same timeline.
- "Close and export" produces one artifact — the timeline plus a support bundle — through the
  existing redaction path.
- An incident is a **view**, not a mode: it changes nothing about what the daemon does, collects
  no data that is not already collected, and can be opened retroactively over a past window.

**Acceptance**

1. An incident opened retroactively over a past window contains the same events as one opened
   live at that time, asserted against a seeded event history. This proves it is a view.
2. Opening an incident changes no collection behaviour — a test asserts identical collector call
   counts with and without one open.
3. The exported artifact passes the same secret-redaction assertions as the `T-1902` support
   bundle, reusing those tests rather than new ones.
4. Events from all five sources appear on one timeline in strict chronological order, asserted
   with interleaved timestamps across sources rather than same-source runs.
5. Closing an incident does not delete its events; reopening shows the same timeline.

## T-2805 · Multi-user presence and changeset locking

**kind:** implementation · **depends on:** —
**context:** `internal/change/changeset.go`, `internal/api/`, `web/src/changesets/`

Nothing stops two operators staging conflicting changes to the same bridge at the same time. The
change engine serialises the *apply*, so the outcome is safe but arbitrary: one person's work is
silently superseded, and neither of them knows it happened.

- Advisory locks on entities with a staged draft: a second operator staging against the same
  entity is warned, told who holds it, and can proceed deliberately.
- Presence: who else is viewing this changeset or entity, updated over the existing event stream.
- Locks expire on session end and on a timeout, so a closed laptop never blocks the cluster.
- **Advisory, not mandatory.** A lock never prevents an emergency change; it prevents an
  *accidental* one. Overriding is recorded.

**Acceptance**

1. A second staging attempt against a locked entity is warned with the holder's identity and can
   proceed; the override is audited.
2. A lock expires on timeout, proven with a clock rather than a wait, and expiry frees the entity.
3. A disconnected session's locks are released, asserted by dropping the connection rather than
   calling a release endpoint.
4. Locks never block apply — a held lock on an entity in an approved changeset does not prevent
   applying it.
5. Presence is per-entity and per-changeset and does not leak identities to a caller lacking the
   capability to see them.

## T-2806 · Map annotation layer

**kind:** implementation · **depends on:** —
**context:** `web/src/topology/`, `internal/store/` (layout tables), `docs/features/topology.md`

The map shows what is true. It cannot show what is *known* — "this uplink is temporary until the
switch swap", "do not touch, vendor-managed". That knowledge currently lives in a wiki that is
wrong within a month, because it is not next to the thing it describes.

- Free-text notes anchored to an entity, and labelled regions drawn on the canvas.
- Stored as app-owned layout data — this is precisely the category `CLAUDE.md` permits, and it is
  emphatically **not** a shadow copy of PVE config.
- Annotations carry an author and timestamp, and optionally an expiry, so a temporary note
  announces its own staleness rather than becoming permanent.
- Visible in the config-doc export, since that is where a note is most useful to a reader who
  cannot see the map.

**Acceptance**

1. An annotation anchored to an entity survives the entity being re-collected and re-rendered.
2. An annotation on a deleted entity is retained and marked orphaned, not silently dropped — the
   note may be the only record of why the entity was removed.
3. Expiry is computed at read time, so a stopped daemon cannot leave an expired note displayed.
4. Annotations appear in the doc export.
5. Regions persist across layout changes and view switches.
6. Annotation text is escaped in every render path; one assertion per path.

## T-2807 · Scheduled digest reports

**kind:** implementation · **depends on:** —
**context:** `internal/posture/`, `internal/capacity/`, `internal/drift/`, `internal/docexport/`,
`internal/findings/webhook.go` (T-2407's delivery path)

Posture scores, capacity forecasts, and drift are computed continuously and looked at when someone
remembers. A weekly digest turns three pull surfaces into one push, and reuses `T-2407`'s
scheduling and delivery machinery wholesale.

- Configurable schedule and recipients, delivered through the existing alert targets.
- Content: posture score with its named factors and the delta since last digest, capacity
  projections crossing the horizon, unresolved drift, and findings opened/closed in the period.
- **A digest with nothing to report says so in one line** and does not manufacture content — the
  fastest way to make people ignore a digest is to send a full one every week regardless.
- Rendered by `docexport`, so the format is the one already tested.

**Acceptance**

1. A digest on a quiet period is one line and is asserted to be under a stated size.
2. Deltas are computed against the previous digest, not against an arbitrary window; a first-ever
   digest states that it has no baseline rather than showing spurious deltas.
3. Delivery reuses `T-2407`'s path, asserted by the digest respecting quiet hours.
4. A failed delivery is retried and recorded with detail, matching existing alert semantics.
5. Schedule changes take effect without a restart.

## T-2808 · In-app assistant over the MCP read tools

**kind:** implementation · **depends on:** T-2705
**context:** `internal/mcp/`, `web/src/`, `docs/security.md`

The MCP surface answers real questions — "which guests can reach the internet", "what changed on
vmbr0 this week" — and is reachable only by an external MCP client. Most operators will never
configure one, so the capability is invisible to the people it was built for.

- A panel that runs the **existing** MCP tools against the local daemon. No new backend
  capability, no new data path, no separate authorisation model.
- Answers cite the tools and entities they came from and link into the relevant screen; an answer
  with no citation is not rendered.
- With `T-2705` available, the assistant may **stage** a changeset and always hands off to the
  normal review surface. It never applies, and the constructor-level guarantee from `T-2705` is
  what makes that structural rather than a promise.
- The model backend is configurable and **absent by default**; with none configured the panel
  states that plainly and nothing is sent anywhere.

**Acceptance**

1. With no backend configured, no outbound request is made, asserted with a transport that fails
   the test if called.
2. Every rendered answer carries at least one citation resolving to a real entity or tool result;
   an uncited answer is not rendered, asserted with a fixture producing one.
3. The panel's authorisation is the caller's own — a user lacking a capability cannot reach data
   through the assistant that they cannot reach directly. One assertion per restricted surface.
4. A staged changeset from the assistant is tagged as such and lands in normal review.
5. No apply path is reachable, inherited from `T-2705`'s compile-time guarantee and re-asserted
   here.
6. Prompt content and answers are excluded from logs and support bundles by default.
