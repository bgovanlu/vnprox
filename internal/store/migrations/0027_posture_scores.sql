-- 0027_posture_scores.sql — T-1607 network posture score & report. One row
-- per scheduled posture computation: the overall 0..100 score plus the full
-- named-factor breakdown, so the report can render a trend line an operator can
-- show someone else (docs/features/blueprints.md §4's config-doc export,
-- extended with a posture section).
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own computed
-- summary of its own read-models (T-1604's SPOF inventory, T-1601's anomaly
-- findings, T-1602's applied segmentation, internal/fw's resolved firewall
-- view, internal/drift's open findings), never a shadow copy of any
-- PVE-authoritative config. The score is always recomputable from live state;
-- these rows exist only to give the report a history to trend.
--
-- BOUNDED retention (NOT a warehouse — unlike T-1606's deliberate
-- capacity_aggregates exception, this table stays within the arc's ordinary
-- bounded-retention rule): the scheduled job keeps the most recent
-- DefaultPostureKeepCount (90) computations OR anything within
-- DefaultPostureRetentionDays (400) by age, whichever is smaller, pruned on the
-- same tick-based prune-loop pattern finding_events (0009) and metric_samples
-- (0001) already establish. factors_json is the serialized []posture.Factor so
-- the export can render each factor's weight/value/contribution without
-- recomputation.
--
-- Migrations are forward-only: this file, once released, must never be edited
-- again. Schema changes land as a new NNNN_*.sql file with a higher version.

CREATE TABLE IF NOT EXISTS posture_scores (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  computed_at  INTEGER NOT NULL,             -- when this score was computed (unix seconds, UTC)
  overall      INTEGER NOT NULL,             -- overall 0..100 posture score (weighted mean of evaluated factors)
  qualified    INTEGER NOT NULL DEFAULT 0,   -- 1 => partial/qualified (>=1 factor unknown or caveated); never read as a clean bill of health
  factors_json TEXT NOT NULL                 -- serialized []posture.Factor: name/weight/value/scorePct/contribution/evaluated/caveat
);

-- The prune-by-age tick and the "keep last N" bound both scan by computed_at;
-- GET /posture (latest) and GET /posture/history (trend) read newest-first.
CREATE INDEX IF NOT EXISTS idx_posture_scores_computed_at ON posture_scores (computed_at);
