-- 0023_external_subnets.sql — T-1203 "Cross-cluster IPAM, external subnets &
-- bidirectional sync": external (non-PVE) subnets become first-class IPAM
-- records. Per CLAUDE.md's storage rule / docs/architecture.md's new-domain
-- invariant, this table holds ONLY app-owned intent — IP space vnprox tracks
-- that Proxmox itself has no knowledge of (a physical LAN, an upstream
-- transit range, a NetBox/phpIPAM-sourced prefix). It is emphatically NOT a
-- shadow copy of any PVE SDN subnet: real SDN subnets stay authoritative in
-- PVE and are read live through internal/ipam's PVE path, never persisted
-- here. External subnets are therefore read/write via dedicated
-- /ipam/external-subnets CRUD routes and NEVER via ipam.alloc.* changeset ops
-- (they are not PVE SDN subnets, so there is nothing in PVE to stage/apply).
--
-- `source` records provenance: 'manual' (an operator typed it in), 'netbox'
-- or 'phpipam' (it was imported from / is kept in sync with an external IPAM
-- system by the bidirectional-sync bridge). It is distinct from
-- GET /ipam/subnets' row-level `source` enum, whose external rows all render
-- as "external" regardless of this provenance value.
--
-- `cidr` is UNIQUE: one external record per network. A re-import of the same
-- prefix updates the existing row rather than duplicating it, mirroring the
-- idempotent upsert the sync bridge relies on.
--
-- Migration numbering (T-1203): 0023, immediately after 0021_clusters.sql /
-- 0022_switches.sql (the Phase-12 reservation). loadMigrations()/migrate()
-- key off each file's own version prefix, never contiguity.
--
-- Migrations are forward-only: once released, never edit this file; a schema
-- change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS external_subnets (
  id          TEXT PRIMARY KEY,   -- ULID
  cidr        TEXT NOT NULL,      -- the external network, e.g. 192.0.2.0/24
  label       TEXT NOT NULL DEFAULT '',      -- operator-facing short name
  source      TEXT NOT NULL DEFAULT 'manual', -- manual|netbox|phpipam (provenance)
  description TEXT NOT NULL DEFAULT '',
  created_by  TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_external_subnets_cidr ON external_subnets (cidr);
