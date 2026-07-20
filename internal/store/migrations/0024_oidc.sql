-- 0024_oidc.sql — T-1207 "OIDC SSO": the app-owned OIDC-group→PVE-identity
-- linkage table, part of the federation cluster registry's data surface.
--
-- Migration numbering note: migrations through 0022 exist at this card's
-- base; 0023 is reserved for a concurrent Phase-12 task, so this card is
-- assigned 0024 (per T-1207's orchestration constraints). loadMigrations()/
-- migrate() key off each file's own version prefix, never contiguity, so a
-- gap is harmless and disappears once the sibling migration lands.
--
-- WHY THIS TABLE EXISTS. OIDC authenticates the *human* to vnprox, but PVE
-- authorization still gates every cluster-scoped action per cluster
-- (docs/security.md's Authentication section, T-1207). An OIDC identity holds
-- no PVE ticket by itself, so a cluster-scoped action needs a resolved PVE
-- authorization for that cluster: an admin-configured OIDC-group→PVE identity
-- mapping, one row per (cluster, group). With no matching row, the OIDC user
-- is authenticated but holds zero cluster-scoped capability on that cluster
-- (the authn/authz split, proven by T-1207 AC2) — writes fall back to the
-- first-use PVE-credential path, never to the OIDC bundle alone.
--
-- `credential_enc` is AES-256-GCM ciphertext (nonce||ciphertext||tag), the
-- IDENTICAL session-secret encryption-at-rest primitive
-- sessions.pve_ticket_enc / clusters.credential_enc use — internal/store's
-- SessionCipher, reused here, NOT a second cipher or key pair
-- (docs/security.md's federation credential-storage note, extended to OIDC).
-- It seals the mapped PVE credential (an API token or a username/password) as
-- one JSON blob; it is never returned by any API response.
--
-- `cluster_id` is the federation cluster this linkage authorizes on; '' is the
-- implicit default/local cluster (the same convention changesets.cluster_id /
-- audit_log.cluster_id use), so a single-cluster deployment maps its groups
-- with cluster_id = ''.
--
-- Migrations are forward-only: once released, never edit this file; a schema
-- change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS oidc_pve_links (
  id             TEXT PRIMARY KEY,               -- ULID
  cluster_id     TEXT NOT NULL DEFAULT '',       -- '' = local/default cluster
  oidc_group     TEXT NOT NULL,                  -- the OIDC group claim value
  pve_username   TEXT NOT NULL,                  -- display/audit label (e.g. automation@pve); not a secret
  credential_enc BLOB NOT NULL,                  -- AES-256-GCM sealed PVE credential
  created_by     TEXT NOT NULL,
  created_at     INTEGER NOT NULL
);

-- One PVE identity per (cluster, group): re-linking a group replaces its row.
CREATE UNIQUE INDEX IF NOT EXISTS ux_oidc_pve_links_cluster_group
  ON oidc_pve_links (cluster_id, oidc_group);
