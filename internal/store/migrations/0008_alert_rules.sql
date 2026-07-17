-- 0008_alert_rules.sql — T-1005 "Alert routing": webhook delivery rules and
-- their delivery log. App-owned data per CLAUDE.md's storage rule (routing
-- rules and delivery history are vnprox's own UI state, never a shadow copy
-- of anything PVE owns).
--
-- `alert_rules` mirrors docs/data-model.md §2's shape: {id, name, enabled,
-- sourceFilter?, severityFilter?, targetKind, targetUrl, targetSecretEnc?,
-- createdAt, updatedAt}. The two filters are stored as JSON arrays (NULL or
-- an empty array both mean "no filter on this dimension" — matches every
-- other optional/ANDed filter contract in this codebase, e.g. GET /findings'
-- own ?source=&severity=). `target_secret_enc` is AES-256-GCM ciphertext
-- (nonce||ciphertext||tag), the same session-secret encryption-at-rest
-- pattern docs/security.md documents for sessions.pve_ticket_enc — see
-- internal/store/cipher.go's SessionCipher, reused here rather than a
-- second cipher implementation. NULL when the target needs no secret
-- (generic webhooks with no auth, or a Slack incoming-webhook URL, whose
-- token already lives in the URL itself).
--
-- `alert_deliveries` logs one row per delivery *attempt* (not one row per
-- logical delivery): `attempt` is the 1-based sequence number within a
-- rule+finding delivery's retry sequence, `status` is `"retrying"` (this
-- attempt failed but another is scheduled), `"delivered"` (this attempt
-- succeeded — terminal), or `"failed"` (this attempt failed and it was the
-- last one — terminal). Bounded by construction: a delivery sequence stops
-- after a small fixed attempt count (internal/findings/webhook.go's
-- maxAttempts), never retried indefinitely, so this table only ever grows
-- by real delivery events — no separate prune job is needed the way
-- metric_samples'/flow_samples' sampled rings need one, matching this
-- task's card ("small/event-driven rather than sampled").
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number. Note for the next agent: 0007 is reserved for a sibling
-- task (T-1002, flow ingestion) landing independently — this file
-- deliberately starts at 0008 to avoid a collision.

CREATE TABLE IF NOT EXISTS alert_rules (
  id                  TEXT PRIMARY KEY,   -- ULID
  name                TEXT NOT NULL,
  enabled             INTEGER NOT NULL,   -- 0|1
  source_filter_json  TEXT,               -- JSON []string, NULL/[] = any source
  severity_filter_json TEXT,              -- JSON []string, NULL/[] = any severity
  target_kind         TEXT NOT NULL,      -- generic|gotify|ntfy|slack
  target_url          TEXT NOT NULL,
  target_secret_enc   BLOB,               -- AES-256-GCM ciphertext, NULL if unset
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_deliveries (
  id          TEXT PRIMARY KEY,   -- ULID
  rule_id     TEXT NOT NULL,
  finding_id  TEXT NOT NULL,
  at          INTEGER NOT NULL,
  attempt     INTEGER NOT NULL,
  status      TEXT NOT NULL,      -- retrying|delivered|failed
  error       TEXT
);

CREATE INDEX IF NOT EXISTS idx_alert_deliveries_rule ON alert_deliveries (rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_deliveries_status ON alert_deliveries (status);
CREATE INDEX IF NOT EXISTS idx_alert_deliveries_at ON alert_deliveries (at);
