-- 0021_clusters.sql — T-1201 "Federation core": the app-owned cluster
-- registry plus the two additive cluster-dimension columns federation
-- threads through the existing changeset/audit tables.
--
-- Migration numbering note: this branch was cut from a base whose latest
-- migration is 0010; sibling Phase-11/12 tasks claim 0011-0020 on their own
-- branches, so this card is assigned 0021 to avoid a migration-number
-- collision at merge time (per T-1201's orchestration constraints). A gap in
-- this branch's own migration sequence is harmless — loadMigrations()/
-- migrate() key off each file's own version prefix, never contiguity — and
-- disappears once the sibling migrations land alongside it.
--
-- `clusters` holds which PVE clusters this vnprox instance (a designated
-- primary/aggregator) attaches and aggregates reads across — app-owned
-- registration intent only, per CLAUDE.md's storage rule / docs/architecture
-- §7's new-domain invariant. It is emphatically NOT a shadow copy of any
-- attached cluster's own network config: Proxmox stays each cluster's own
-- source of truth (federation federates *views and workflows*, never config
-- ownership — docs/roadmap-next.md's Phase 12 invariants).
--
-- `credential_enc` is AES-256-GCM ciphertext (nonce||ciphertext||tag), the
-- IDENTICAL session-secret encryption-at-rest primitive
-- sessions.pve_ticket_enc / alert_rules.target_secret_enc use — see
-- internal/store/cipher.go's SessionCipher, reused here, NOT a second cipher
-- or key pair (docs/security.md's federation credential-storage note). It
-- seals the attached cluster's PVE API credential (a ticket username/password
-- or an API token) as one blob; it is never returned by any API response —
-- GET /federation/clusters only ever reports non-secret registry fields.
--
-- `status` is the last aggregation pass's own best-effort reachability cache
-- ("unknown"|"ok"|"unreachable"), so GET /federation/clusters can render a
-- summary without a live fan-out on every list call — the aggregator itself
-- always probes fresh, never trusting this cache as authoritative.
--
-- Migrations are forward-only: once released, never edit this file; a schema
-- change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS clusters (
  id             TEXT PRIMARY KEY,   -- ULID
  name           TEXT NOT NULL,
  api_url        TEXT NOT NULL,      -- the attached cluster's PVE API base URL
  credential_enc BLOB NOT NULL,      -- AES-256-GCM ciphertext of the PVE credential
  status         TEXT NOT NULL DEFAULT 'unknown',  -- unknown|ok|unreachable, last pass's cache
  added_by       TEXT NOT NULL,
  added_at       INTEGER NOT NULL
);

-- Per-cluster changeset scoping (additive; a single-cluster deployment's
-- changesets keep working with the implicit default cluster '' this DEFAULT
-- supplies). internal/change rejects any op whose target Ref belongs to a
-- different cluster than the changeset's cluster_id at validation time — no
-- op type or API surface lets a changeset span clusters.
ALTER TABLE changesets ADD COLUMN cluster_id TEXT NOT NULL DEFAULT '';

-- Global audit trail's cluster dimension (docs/architecture §7): each audit
-- row is tagged with the cluster the action targeted. '' is the implicit
-- default/local cluster, so every pre-federation row keeps its meaning.
ALTER TABLE audit_log ADD COLUMN cluster_id TEXT NOT NULL DEFAULT '';
