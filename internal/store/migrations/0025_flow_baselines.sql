-- 0025_flow_baselines.sql — T-1601 flow baselining & anomaly findings: one
-- learned per-guest/per-segment traffic baseline (internal/baseline.Profile),
-- serialized, one row per inventory Ref.
--
-- App-owned SUMMARY data per CLAUDE.md's storage rule: a baseline_profiles
-- row is vnprox's own statistical summary of observed traffic (top talkers,
-- observed service-port set, per-hour-of-day byte-volume mean/stddev), NOT a
-- shadow copy of flow_samples' raw rows and NOT PVE-authoritative config. A
-- learned shape deliberately OUTLIVES the raw flows it was learned from —
-- flow_samples is a short bounded ring ([flows] retention_minutes, default
-- 60), whereas a baseline is retained for [baseline] profile_retention_days
-- (default 90) so "what normal looks like" survives long past the individual
-- flow rows, exactly the point of learning a summary rather than re-scanning
-- raw flows forever.
--
-- ref is the natural primary key: at most one current baseline per Ref, so a
-- re-learn upserts (BaselineProfileRepo.Put) rather than accumulating history
-- (this table is not a time-series ring like flow_samples/metric_samples —
-- it holds the single latest learned shape per Ref). window_start/window_end
-- record the learning window the profile_json summarizes; updated_at is the
-- learn time, and the sole basis for the retention prune (a profile older
-- than [baseline] profile_retention_days is dropped, the same tick-based
-- prune-loop pattern metric_samples' RunPruneLoop already establishes).
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS baseline_profiles (
  ref          TEXT PRIMARY KEY,             -- inventory Ref string (guest or segment)
  profile_json TEXT NOT NULL,                -- serialized internal/baseline.Profile
  window_start INTEGER NOT NULL,             -- unix seconds, learning window start
  window_end   INTEGER NOT NULL,             -- unix seconds, learning window end
  updated_at   INTEGER NOT NULL              -- unix seconds, when this profile was (re)learned
);

-- The retention prune tick scans by updated_at first.
CREATE INDEX IF NOT EXISTS idx_baseline_profiles_updated_at ON baseline_profiles (updated_at);
