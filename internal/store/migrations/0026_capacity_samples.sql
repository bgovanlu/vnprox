-- 0026_capacity_samples.sql — T-1606 capacity forecasting: the arc's ONE
-- deliberate retention extension. A downsampled, daily rollup of link and
-- IPAM-pool utilization, kept far longer than any raw-sample ring so a
-- growth curve can be fit across weeks/months for a "vmbr1 uplink full in
-- ~5 weeks" forecast.
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own
-- computed summary of its own observations (metric_samples' 24h ring for
-- links, internal/ipam's live allocation counts for pools), never a shadow
-- copy of any PVE-authoritative config.
--
-- This is a DOWNSAMPLED aggregate, NOT a raw-data warehouse: one row per
-- (ref, kind, day), not per sample. Explicitly bounded by
-- [capacity] aggregate_retention_days (default 400 — ~13 months, enough for
-- year-over-year trend without being unbounded), pruned on the same
-- tick-based prune-loop pattern metric_samples (0001_init.sql) and
-- flow_samples (0007_flows.sql) already establish. It is the single, named
-- exception to the bounded-24h-class-retention rule every other card in
-- this arc stays within — see docs/data-model.md's capacity_aggregates
-- entry for the contrast with metric_samples/flow_samples.
--
-- Numbering note: 0025 is deliberately skipped (reserved for a concurrent
-- task landing independently); a gap is harmless — migrate() applies every
-- migration whose version exceeds the DB's current schema_version, in order,
-- and only rejects duplicate version numbers, not gaps.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS capacity_aggregates (
  ref             TEXT NOT NULL,             -- link: inventory Ref string; ipam_pool: subnet CIDR
  kind            TEXT NOT NULL,             -- "link"|"ipam_pool"
  bucket_at       INTEGER NOT NULL,          -- start-of-day (UTC) unix seconds — one bucket per ref per day
  avg_utilization REAL NOT NULL DEFAULT 0,   -- mean utilization over the day, percent (0-100)
  max_utilization REAL NOT NULL DEFAULT 0,   -- peak utilization over the day, percent (0-100)
  created_at      INTEGER NOT NULL,          -- when this rollup row was written (unix seconds)
  PRIMARY KEY (ref, kind, bucket_at)         -- one row per (ref, kind, day): re-running a day's rollup upserts, never duplicates
);

-- The prune-by-age tick scans by bucket_at; GET /capacity/export and the
-- forecast producer both read a single ref's history in bucket order.
CREATE INDEX IF NOT EXISTS idx_capacity_aggregates_bucket ON capacity_aggregates (bucket_at);
CREATE INDEX IF NOT EXISTS idx_capacity_aggregates_ref ON capacity_aggregates (ref, kind, bucket_at);
