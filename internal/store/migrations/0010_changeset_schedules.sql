-- 0010_changeset_schedules.sql — T-1103 "Scheduled changesets & maintenance
-- windows": stage now, apply inside a window, reusing T-205/T-304's
-- existing apply/commit-confirm/rollback machinery unchanged — this table
-- only decides *when* Service.Apply gets called, never how it runs.
--
-- App-owned data per CLAUDE.md's storage rule: a schedule is vnprox's own
-- automation metadata, never a shadow copy of PVE config. One row per
-- changeset that currently has (or most recently had) a schedule —
-- changeset_id is the primary key, so scheduling a changeset again after an
-- earlier schedule resolved (fired/missed/cancelled/blocked/failed)
-- replaces that row rather than accumulating history; the changeset's own
-- audit trail (changeset.schedule_create/_cancel/_fire/_fire_blocked/
-- _missed) is the durable history of what happened, exactly like every
-- other T-205 lifecycle transition.
--
-- callback_token_hash never stores the callback token itself (delivered
-- once, in the POST /changesets/{id}/schedule response, and never
-- persisted in plaintext anywhere) — only a sha256 hex digest of it, so a
-- leaked database dump cannot be used to forge an ack. Verifying an
-- incoming ack only ever needs this stored hash (re-derived from the
-- presented token and compared), never the signing secret the token was
-- minted with — see internal/change/schedule.go's mintCallbackToken/
-- verifyCallbackToken doc comments for why that means callback tokens
-- survive a daemon restart even though the signing secret is a
-- process-lifetime-only value.
--
-- Migrations are forward-only: this file, once released, must never be
-- edited again. Schema changes land as a new NNNN_*.sql file with a higher
-- version number. Note for the next agent: 0009 (T-1007's finding_events)
-- is already taken — this file starts at 0010.

CREATE TABLE IF NOT EXISTS changeset_schedules (
  changeset_id        TEXT PRIMARY KEY,
  window_start        INTEGER NOT NULL,      -- unix seconds
  window_end          INTEGER NOT NULL,      -- unix seconds; window_end > window_start
  confirm_timeout_sec INTEGER NOT NULL,
  missed_window_policy TEXT NOT NULL,        -- "skip"|"applyImmediately"
  callback_token_hash TEXT NOT NULL,         -- sha256 hex of the one-time-delivered token
  status              TEXT NOT NULL,         -- pending|fired|missed|blocked|failed|cancelled
  created_by          TEXT NOT NULL,
  created_at          INTEGER NOT NULL,
  fired_at            INTEGER,               -- unix; set when the scheduler resolves this row (fired/missed/blocked/failed)
  cancelled_at        INTEGER
);

-- The scheduler's own tick (change.Service.TickSchedules) scans every
-- pending row each cycle; this index keeps that scan cheap even with a
-- large history of resolved rows accumulating status values other than
-- pending.
CREATE INDEX IF NOT EXISTS idx_changeset_schedules_status ON changeset_schedules (status);
