-- 0046_push_subscriptions.sql — T-2005 "Mobile PWA with push": web-push
-- subscriptions for the on-call triage flow (docs/roadmap-universal.md
-- Phase 17's exit demo: "the on-call human confirms it from their phone").
-- App-owned data per CLAUDE.md's storage rule: a subscription describes
-- nothing PVE owns, only which browser endpoint on which device wants which
-- category of push, tied to the session that created it.
--
-- docs/security.md's Authentication section treats sessions.pve_ticket_enc
-- as a credential encrypted at rest; a push subscription's endpoint + keys
-- are the same class of secret (anyone holding them can push arbitrary
-- payloads to that browser until it unsubscribes), so they get the same
-- treatment: `endpoint_enc`/`p256dh_enc`/`auth_enc` are AES-256-GCM
-- ciphertext under the identical session-secret cipher/key
-- (internal/store/cipher.go's SessionCipher) `webhooks.secret_enc` and
-- `sessions.pve_ticket_enc` already use — decrypted only by cmd/vnproxd's
-- composition-root adapter when a push actually needs sending, never
-- returned by any route (GET /push/subscriptions responds with metadata
-- only — see internal/api/push.go).
--
-- `endpoint_hash` (SHA-256 hex of the RAW endpoint URL, computed before
-- encryption) exists purely so the daemon can recognize "the browser
-- resubscribed to the same push service endpoint" and detect/refuse an
-- exact duplicate without ever decrypting an existing row to compare it —
-- the same "hash for lookup, encrypt for storage" split
-- api_tokens.token_hash uses for a different reason (one-way there since a
-- token is never read back at all; here it's two-way because the daemon
-- itself must eventually decrypt the endpoint to deliver a push, but a
-- lookup/dedupe check never needs to).
--
-- `session_id` REFERENCES sessions(id) ON DELETE CASCADE is what makes
-- "subscriptions are per-session and die with it" (T-2005's card) a
-- property of the schema rather than a promise some handler has to
-- remember to keep: this database's DSN already opens with
-- `_pragma=foreign_keys(1)` (internal/store/store.go), so the moment
-- internal/auth's logout path (or session expiry reaping) deletes a
-- sessions row, SQLite removes every push_subscriptions row that pointed at
-- it in the same transaction — no cross-package hook required, and no
-- window where a dead session's subscription could still receive a push.
--
-- `categories_json` is a JSON array drawn from the fixed, closed vocabulary
-- internal/push documents (`critical`, `awaitingConfirm`, `drift` — T-2005's
-- card: "per-category opt-in: critical findings, awaiting-confirm
-- changesets, drift"), the same optional-allowlist convention
-- `webhooks.events_json` already uses, except never NULL/empty here: a
-- subscription with zero categories selected is pointless, so POST
-- /push/subscriptions requires at least one (enforced in internal/api, not
-- by a DB constraint SQLite's JSON1-less core can't express cleanly).
--
-- `device_label` is client-supplied, free text, capped short by the route
-- handler — display-only ("iPhone — Safari"), never parsed. Empty string
-- (not NULL) when the client sends nothing, matching map_regions.color's
-- "'' = default" convention rather than introducing a new NULL-handling
-- case for a purely cosmetic field.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number.

CREATE TABLE IF NOT EXISTS push_subscriptions (
  id              TEXT PRIMARY KEY,   -- ULID
  session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  username        TEXT NOT NULL,
  endpoint_hash   TEXT NOT NULL,      -- hex SHA-256 of the raw (unencrypted) endpoint URL
  endpoint_enc    BLOB NOT NULL,      -- AES-256-GCM ciphertext of the endpoint URL
  p256dh_enc      BLOB NOT NULL,      -- AES-256-GCM ciphertext of the subscription's p256dh key
  auth_enc        BLOB NOT NULL,      -- AES-256-GCM ciphertext of the subscription's auth secret
  categories_json TEXT NOT NULL,      -- JSON []string, subset of {"critical","awaitingConfirm","drift"}
  device_label    TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  last_used_at    INTEGER
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_push_subscriptions_endpoint_hash ON push_subscriptions (endpoint_hash);
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_session_id ON push_subscriptions (session_id);
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_username ON push_subscriptions (username);
