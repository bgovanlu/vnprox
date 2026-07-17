-- 0005_sim_divergence_findings.sql — T-806 "Verify live": a persisted
-- finding for every path-simulator tuple whose live guest-agent probe
-- (POST /simulate/verify, T-802) disagreed with the static simulator's own
-- verdict for the identical src/dst/proto/port. App-owned data per
-- CLAUDE.md's storage rule: this is a fact *this daemon observed* during a
-- user-triggered diagnostic action, not a copy of any PVE-owned config —
-- and unlike drift/lldp/ipam/health, it cannot be re-derived from polled
-- state on the next findings cycle (nothing re-runs a live guest-agent
-- probe on its own), so it needs a table of its own rather than living
-- only in memory.
--
-- One row per distinct (src,dst,proto,port) tuple: `id` is the same
-- content-derived key internal/findings.Finding.ID uses
-- ("probe:sim_divergence|<tuple>" — see internal/api/simulate.go's
-- simDivergenceTupleKey), so re-verifying the identical tuple upserts the
-- same row (never accumulates duplicates) and a tuple that stops
-- diverging on a later re-verify is deleted (findings.Engine.Findings()
-- only ever reflects what was true as of the most recent verify call for
-- that exact tuple).
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS sim_divergence_findings (
  id                TEXT PRIMARY KEY,   -- content-derived, see doc comment above
  src_ref           TEXT NOT NULL,      -- guest-nic Ref string (the probed src)
  dst_kind          TEXT NOT NULL,      -- guest-nic|ip|external
  dst_ref           TEXT NOT NULL DEFAULT '',  -- set iff dst_kind = guest-nic
  dst_ip            TEXT NOT NULL DEFAULT '',  -- set iff dst_kind = ip
  proto             TEXT NOT NULL,      -- tcp|icmp
  port              INTEGER NOT NULL DEFAULT 0,
  simulated_verdict TEXT NOT NULL,      -- sim.Verdict at the time of this verify call
  observed_outcome  TEXT NOT NULL,      -- probe.Outcome at the time of this verify call
  detail            TEXT NOT NULL DEFAULT '',
  created_at        INTEGER NOT NULL,   -- unix seconds, first time this tuple diverged
  updated_at        INTEGER NOT NULL    -- unix seconds, most recent divergence
);
