-- 0028_changeset_origin.sql — T-1701 MCP server & AI operator readiness.
-- Additive provenance columns on changesets so an operator can always tell an
-- AI-staged draft (origin 'mcp') from a human-staged one ('ui') or a CLI one
-- ('cli'), and trace it back to the exact automation token that staged it.
--
-- This is the audit-trail half of T-1701's central safety invariant: the MCP
-- surface can STAGE a draft changeset but never apply/confirm/rollback it, and
-- every AI-originated changeset is labelled here so the labelling is
-- structural, not a convention a later reader has to trust. `origin` defaults
-- to 'ui' so every pre-existing row (and every ordinary UI-staged changeset)
-- keeps its meaning without a backfill; `origin_token_id` is NULL for anything
-- not minted through a bearer token (i.e. all UI-originated changesets).
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own record of
-- who staged one of vnprox's own changesets, never a shadow of any
-- PVE-authoritative config.
--
-- Migrations are forward-only: this file, once released, must never be edited
-- again. Schema changes land as a new NNNN_*.sql file with a higher version.

ALTER TABLE changesets ADD COLUMN origin TEXT NOT NULL DEFAULT 'ui';
ALTER TABLE changesets ADD COLUMN origin_token_id TEXT;
