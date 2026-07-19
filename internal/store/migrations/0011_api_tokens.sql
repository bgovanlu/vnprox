-- 0011_api_tokens.sql — T-1104 "Event stream & automation tokens": scoped
-- API tokens for automation callers, and webhook registrations that receive
-- the same WS "events" envelope over HTTP. App-owned data per CLAUDE.md's
-- storage rule: neither table shadows anything PVE owns — a token is a
-- vnprox-local, capability-scoped delegated credential a logged-in user
-- mints (docs/security.md's Authentication section, "distinguished from the
-- PVE ticket bridge"), and a webhook registration is purely vnprox's own
-- delivery-target config.
--
-- `api_tokens` mirrors docs/api.md's Tokens section: {id, name, scopes,
-- createdBy, createdAt, lastUsedAt?, revokedAt?} — the raw token value
-- itself is never stored, only `token_hash` (hex SHA-256 of the raw
-- 256-bit token; unlike sessions.pve_ticket_enc this is a one-way hash, not
-- reversible encryption, since the only thing a request ever needs to prove
-- is "I know a token whose hash matches a live row" — POST /tokens reveals
-- the raw value exactly once, at creation, matching the PVE-API-token UX
-- convention docs/api.md's Tokens section describes). `scopes_json` is a
-- JSON array of internal/auth.Cap strings (the existing capability-flag
-- vocabulary plus the new "automation" scope, docs/api.md's Tokens
-- section) — never exceeding the creating user's own derived capabilities
-- at mint time (enforced in internal/auth's token-minting code, not by any
-- DB constraint). `revoked_at` is set (never deleted) so a revoked token's
-- audit trail (`token.create`/`token.revoke` entries, and any `token.use`
-- rows already recorded against it) stays intact.
--
-- `webhooks` mirrors docs/api.md's Webhooks section: {id, url, events,
-- createdBy, createdAt}. `events_json` is a JSON array of event names
-- (empty/NULL = every event, the same optional/ANDed-filter convention
-- alert_rules' source_filter_json/severity_filter_json use). `secret_enc`
-- is AES-256-GCM ciphertext of the caller-supplied HMAC signing secret
-- (internal/store/cipher.go's SessionCipher — the same cipher/key
-- alert_rules.target_secret_enc and sessions.pve_ticket_enc already use),
-- never returned by any route once set. `consecutive_failures`/
-- `last_attempt_at`/`last_success_at`/`last_error` back the
-- `webhook_unhealthy` finding (N consecutive failures raise it; a
-- subsequent success resets the counter to 0 and clears it on the next
-- findings cycle) — computed live from these columns each findings cycle
-- (internal/findings' WebhookProvider seam), not a second persisted finding
-- table, mirroring how internal/ipam's conflict findings are recomputed
-- fresh from live state rather than stored.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number. Note for the next agent: 0009 (T-1007's finding_events)
-- is the current max as of this task; 0010 is reserved for T-1103
-- (scheduled changesets), landing independently — this file deliberately
-- starts at 0011 to avoid a collision with either.

CREATE TABLE IF NOT EXISTS api_tokens (
  id           TEXT PRIMARY KEY,   -- ULID
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL,      -- hex SHA-256 of the raw bearer token
  scopes_json  TEXT NOT NULL,      -- JSON []string, internal/auth.Cap names
  created_by   TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at   INTEGER
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_token_hash ON api_tokens (token_hash);

CREATE TABLE IF NOT EXISTS webhooks (
  id                   TEXT PRIMARY KEY,   -- ULID
  url                  TEXT NOT NULL,
  events_json          TEXT,               -- JSON []string, NULL/[] = every event
  secret_enc           BLOB NOT NULL,       -- AES-256-GCM ciphertext of the HMAC secret
  created_by           TEXT NOT NULL,
  created_at           INTEGER NOT NULL,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_attempt_at      INTEGER,
  last_success_at      INTEGER,
  last_error           TEXT
);
