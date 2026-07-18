-- 0009_finding_events.sql — T-1007 "History playback": a lightweight,
-- bounded log of finding transitions (new/escalated/resolved), populated
-- from findings.Notifier's EXISTING transition detection (notify.go's
-- evaluateNotifications/fireNotification) via a new Notifier implementation
-- (internal/findings/findingevents.go's FindingEventsNotifier) composed
-- alongside PVENotifier/WebhookNotifier at the cmd/vnproxd composition
-- root (multiNotifier) — no duplicated transition-detection logic.
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own record
-- of when its findings stream changed, never a shadow copy of PVE state.
-- Bounded to the same window as metric_samples (store.MetricRetention,
-- 24h) and pruned on the same cadence — see FindingEventRepo.RunPruneLoop's
-- doc comment; cmd/vnproxd wires its prune loop alongside (not instead of)
-- metric_samples' own hourly prune loop. GET /history/events
-- (internal/api/history.go) merges this table with a filtered slice of
-- audit_log (changeset apply/confirm/rollback lifecycle rows) into one
-- timeline-marker feed for web/src/topology/history/HistoryTimeline.tsx's
-- scrubber.
--
-- id is a plain autoincrement surrogate key (like flow_samples, unlike
-- metric_samples' natural (ref, at) key): the same finding id can
-- legitimately transition more than once within the retention window
-- (new -> escalated -> resolved), and each transition is its own row.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number. Note for the next agent: 0007 (T-1002's flow_samples) and
-- 0008 (T-1005's alert_rules/alert_deliveries) are already taken — this
-- file starts at 0009.

CREATE TABLE IF NOT EXISTS finding_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  finding_id TEXT NOT NULL,
  at         INTEGER NOT NULL,      -- unix seconds
  transition TEXT NOT NULL          -- "new"|"escalated"|"resolved"
);

-- The prune-by-age tick and GET /history/events' ?fromTs=/?toTs= filters
-- both scan by at first; finding_id backs a possible future "history for
-- this one finding" lookup (not exposed by this task's own route, which is
-- cluster-timeline-wide, but cheap to index now alongside the table it
-- belongs to).
CREATE INDEX IF NOT EXISTS idx_finding_events_at ON finding_events (at);
CREATE INDEX IF NOT EXISTS idx_finding_events_finding_id ON finding_events (finding_id);
