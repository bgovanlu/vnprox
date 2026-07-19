-- 0011_guest_interior_toggles.sql — T-1304 "Guest network interior
-- inspector": the per-guest opt-in preference gating `GET /guests/{ref}/
-- interior` (off by default — the route reaches into the guest, or reads
-- an lxc container's netns from the host side, so an operator must
-- explicitly turn it on per guest before vnprox ever attempts either
-- read). App-owned UI state per CLAUDE.md's storage rule — this table
-- records only "has an operator opted this guest in", never a copy of any
-- PVE-owned config or the interior read set itself (which is never
-- persisted at all, live-read on every GET /guests/{ref}/interior call).
--
-- Keyed by the guest's Ref string (kind:node:id — always "guest:<node>:
-- <vmid>" in practice), one row per guest, following `annotations`'
-- ref-keyed shape rather than `layouts`' per-username shape: the toggle is
-- a shared, cluster-wide preference (any netRead-capable operator can see
-- and flip it), not private per-user data.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS guest_interior_toggles (
  ref        TEXT PRIMARY KEY,
  enabled    INTEGER NOT NULL,
  updated_by TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
