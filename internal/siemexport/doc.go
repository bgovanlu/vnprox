// SPDX-License-Identifier: Apache-2.0

// Package siemexport implements T-4012: a bounded, best-effort streaming
// export of vnprox's audit log and findings stream to an operator-run SIEM,
// in RFC 5424 syslog or newline-delimited JSON.
//
// # Why this exists
//
// vnprox is explicitly not a long-horizon warehouse (docs/features.md):
// internal/store's audit_log and internal/findings' stream are both bounded
// and pruned (store.AuditRepo.RunPruneLoop, [retention] audit_keep_days).
// An operator who wants ninety days of audit history is expected to keep it
// in a SIEM they already run, not in vnprox's own SQLite — this package is
// the "ship it out" half of that contract. It is fed from the exact points
// that already produce the primary record (store.AuditRepo.Append via its
// existing SetOnAppend hook, and internal/findings.Engine's per-cycle
// finding set via its existing Config.OnCycle hook) — a second consumer of
// each, never a new capture path, exactly as T-4012's card requires.
//
// # Delivery semantics: at-most-once, chosen deliberately
//
// Every event is attempted at most one time. Exporter.enqueue appends to a
// bounded in-memory ring buffer (capacity SIEMExportConfig.BufferSize);
// when full, the OLDEST buffered-but-not-yet-attempted event is evicted to
// make room for the newest one — "old events dropped rather than unbounded
// queue growth," the same bounded-ring shape internal/capture's retention
// sweep and internal/metrics' self-observability ring both already use
// elsewhere in this codebase. Exporter.Run then dequeues the oldest
// remaining event and hands it to the configured Sink exactly once: on
// success it is done, on failure it is NOT retried or requeued — it is
// dropped and counted, and Run backs off before attempting the next one so
// a sustained outage does not spin-loop against a dead endpoint.
//
// This is a deliberate choice, not an oversight. The alternative
// (at-least-once, retrying a failed send) would mean an event that a SIEM
// received but whose ACK was lost to a dropped TCP connection gets resent —
// silently producing duplicate audit rows in the far end, which is a
// correctness problem a security team has to notice and work around for
// every query they ever write against that data. At-most-once instead
// trades "a SIEM might be missing an event" for "a SIEM is never lied to by
// a duplicate" — and the missing half is never silent: every drop
// (buffer-full eviction or a failed send) increments Exporter.Stats().Dropped
// and calls the registered DropObserver, so the gap is itself observable.
// This is safe to choose here specifically because of acceptance
// criterion 2: vnprox's own audit_log and findings stream are the primary,
// durable record regardless of what this package delivers — export loss
// never touches them.
//
// # Field mapping (the documented parser contract)
//
// Every exported event, audit or finding, carries these normalized fields
// (Event, event.go) regardless of transport:
//
//	kind       "audit" | "finding"
//	at         event timestamp, UTC
//	severity   "error" | "warning" | "info" (audit: derived from Result —
//	           "denied"/"blocked" -> warning, "error"/"failed"/"failure" ->
//	           error, anything else -> info; finding: Finding.Severity
//	           verbatim)
//
// Audit-only fields (mirror internal/store.AuditEntry / docs/api.md's GET
// /audit `auditEntryResponse`):
//
//	auditId, username, action, target, changesetId, result, ip, detail
//
// Finding-only fields (mirror internal/findings.Finding / docs/api.md's GET
// /findings shape), plus transition (this package's own addition — see
// below):
//
//	findingId, source, check, transition, nodes, refs, findingDetail
//
// transition is "new" | "changed" | "resolved": computed by this package's
// own OnCycle observer (cmd/vnproxd's siemFindingsTracker), independent of
// internal/findings.Engine's own Notifier transition tracking
// (notify.go's TransitionKind). That independence is deliberate: Engine
// only calls its Notifier above Config.NotifyThreshold, which is an
// alerting knob an operator sets for paging/webhook noise — an audit/SIEM
// export must not silently drop every info-severity finding just because
// the alert threshold happens to be "warning". siemFindingsTracker instead
// diffs the full findings list Config.OnCycle already hands it every
// cycle against its own previous-cycle snapshot, so every finding that
// ever appears, changes severity, or resolves is exported, regardless of
// NotifyThreshold — while still never re-sending an unchanged finding
// every single cycle forever, which is what OnCycle's raw feed would do if
// forwarded uncoalesced.
//
// JSONL: one JSON object per line, field names exactly as above (event.go's
// jsonlRecord). Syslog (RFC 5424): the same fields carried as
// SD-PARAMs in a single `vnproxAudit@32473` / `vnproxFinding@32473`
// SD-ELEMENT (32473 is RFC 5424 §7.2.2's reserved "example" enterprise
// number — vnprox has no IANA-registered PEN of its own), with a
// human-readable summary in MSG for a SIEM parser that only reads MSG.
//
// # Redaction
//
// Every audit Detail (raw JSON) and finding Detail (free text) passes
// through internal/redact — redact.JSON for the former, redact.Scrub for
// the latter — inside NewAuditEvent/NewFindingEvent, so a caller cannot
// forget to redact before exporting: there is no exported constructor for
// Event that skips it. See those functions' doc comments for exactly what
// internal/redact catches and what it does not; this package adds no
// redaction rules of its own; it depends entirely on that package's
// vocabulary.
//
// # What this package is not
//
// It does not retain anything vnprox does not already retain — Exporter
// holds only the bounded in-flight buffer, nothing durable, nothing
// queryable. It does not add a query API over exported events (out of
// scope per this task's card). And it never blocks or slows the primary
// audit/findings write path: ExportAudit/ExportFinding only ever append to
// an in-memory slice under a mutex and return.
package siemexport
