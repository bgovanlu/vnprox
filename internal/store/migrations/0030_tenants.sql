-- 0030_tenants.sql — T-1703 multi-tenancy & self-service. Delegated views and
-- request-changeset workflows on the federation-era permission model: a tenant
-- sees only its own guests/VLANs/subnets/clusters (tenant_scopes) and its
-- members can REQUEST changes that route to an approver, converting to an
-- ordinary draft only after approval.
--
-- App-owned data per CLAUDE.md's storage rule: these tables hold vnprox's own
-- delegation model (who may see/request what), never a shadow copy of any
-- PVE-authoritative config. The refs in tenant_scopes are inventory Ref strings
-- (docs/data-model.md §1's Ref triplet, "kind:node:id") naming what is visible;
-- the live entities themselves are never persisted here.
--
-- SECURITY (docs/security.md, Tenant authorization): tenant scoping is enforced
-- server-side at the data-access layer. Every tenant-scoped read filters by the
-- authenticated principal's tenant against tenant_scopes; a request for another
-- tenant's resource returns not-found, never that resource. tenant_members.role
-- distinguishes a plain member (may request) from an approver (may approve a
-- request-changeset) — a member can never approve their own tenant's request.
--
-- Migrations are forward-only: this file, once released, must never be edited
-- again. Schema changes land as a new NNNN_*.sql file with a higher version.
-- Numbered 0030 by orchestration assignment (0028/0029 and 0031/0032 are
-- reserved for concurrent Phase-17 tasks); loadMigrations keys off each file's
-- own version prefix, never contiguity, so the gap is harmless.

CREATE TABLE IF NOT EXISTS tenants (
  id         TEXT PRIMARY KEY,            -- ULID
  name       TEXT NOT NULL,
  created_by TEXT NOT NULL,               -- the admin identity that created the tenant
  created_at INTEGER NOT NULL             -- unix seconds, UTC
);

-- One row per resource the tenant may see. scope_ref is an inventory Ref
-- string (e.g. "sdn-vnet::zone1/vnet1", "guest:pve1:100", "sdn-subnet::
-- 10.0.0.0/24", or "cluster:<id>") — a coarse scope (a VLAN/VNet) is expanded
-- to its member guests/subnets live against the inventory graph at read time,
-- never frozen here.
CREATE TABLE IF NOT EXISTS tenant_scopes (
  tenant_id TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  scope_ref TEXT NOT NULL,
  PRIMARY KEY (tenant_id, scope_ref)
);

-- One row per (tenant, identity). role is 'member' (may request changes, sees
-- the scoped view) or 'approver' (may additionally approve another member's
-- request-changeset — never their own act of requesting). identity is the
-- principal string the session/OIDC layer authenticates as (username, or an
-- OIDC-group-derived membership).
CREATE TABLE IF NOT EXISTS tenant_members (
  tenant_id TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  identity  TEXT NOT NULL,
  role      TEXT NOT NULL,                -- member|approver
  PRIMARY KEY (tenant_id, identity)
);

-- The identity->tenant lookup the scoping middleware runs on every request.
CREATE INDEX IF NOT EXISTS idx_tenant_members_identity ON tenant_members (identity);

-- One row per request-changeset (changeset created via POST /changesets
-- {tenantId} in status 'requested'). Records which tenant owns the request and
-- who raised it, so an approver check can reject the requester approving their
-- own request, and so the approval notification can be routed to the tenant's
-- approver group. The changeset itself lives in the changesets table exactly
-- like every other changeset — this table only carries the tenant linkage the
-- changesets table has no column for.
CREATE TABLE IF NOT EXISTS changeset_requests (
  changeset_id TEXT PRIMARY KEY,          -- changesets.id (ULID)
  tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  requested_by TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  approved_by  TEXT NOT NULL DEFAULT '',  -- set when an approver converts it to a draft; '' while pending
  approved_at  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_changeset_requests_tenant ON changeset_requests (tenant_id);
