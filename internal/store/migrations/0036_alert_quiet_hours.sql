-- 0036_alert_quiet_hours.sql — T-2407 "Alert quiet hours and digest
-- coalescing": per-rule delivery scheduling, and the durable queue that makes
-- "deferred, not dropped" true across a daemon restart.
--
-- WHY A TABLE AND NOT AN IN-MEMORY QUEUE. A quiet-hours window is routinely
-- eight hours long. An in-memory queue turns any restart inside that window —
-- a package upgrade, an OOM, a node reboot — into silently dropped alerts,
-- which is the exact failure the feature exists to prevent. Holding the
-- pending events in SQLite costs one table and makes the promise real.
--
-- alert_rules gains:
--   quiet_start/quiet_end  "HH:MM" local wall-clock, both NULL = no quiet
--                          hours. quiet_start > quiet_end means the window
--                          crosses midnight (22:00-06:00), which is the
--                          common case and therefore not an edge case.
--   quiet_tz               IANA zone name the two above are read in. NULL
--                          means the daemon's own local zone. Stored per rule
--                          rather than globally because "quiet hours" is a
--                          statement about a human's night, and a federated
--                          deployment has humans in more than one.
--   quiet_bypass_error     0|1, default 1: an `error`-severity finding is
--                          delivered during quiet hours anyway.
--
--                          NOTE ON VOCABULARY: T-2407's card says "critical
--                          severity bypasses quiet hours". vnprox has no
--                          `critical` severity — internal/findings' vocabulary
--                          is error|warning|info (types.go), and `error` is
--                          the top tier. This column implements the card's
--                          intent against the vocabulary that exists rather
--                          than inventing a fourth severity that every
--                          producer, filter and UI would then have to learn.
--   digest_window_sec      0 = deliver each event immediately (today's
--                          behaviour, and the default). >0 coalesces every
--                          event arriving within that many seconds of the
--                          first one into a single delivery.
--
-- alert_deliveries gains `detail`, so a deferred or coalesced delivery can say
-- why in the delivery log. "We never got paged" has to be answerable, and
-- `error` is the wrong column to answer it in — a deferral is not a failure.
--
-- Migrations are forward-only: this file, once released, must never be edited.

ALTER TABLE alert_rules ADD COLUMN quiet_start TEXT;
ALTER TABLE alert_rules ADD COLUMN quiet_end TEXT;
ALTER TABLE alert_rules ADD COLUMN quiet_tz TEXT;
ALTER TABLE alert_rules ADD COLUMN quiet_bypass_error INTEGER NOT NULL DEFAULT 1;
ALTER TABLE alert_rules ADD COLUMN digest_window_sec INTEGER NOT NULL DEFAULT 0;

ALTER TABLE alert_deliveries ADD COLUMN detail TEXT;

-- alert_pending is the durable deferral queue: one row per held event.
--
-- finding_json is the whole Finding as it was at the moment it fired, not a
-- reference to a live one. A held event describes something that was true when
-- it happened; re-reading the finding at flush time would deliver a different
-- (possibly resolved, possibly absent) fact under the original event's
-- timestamp.
--
-- flush_at is absolute unix seconds, computed once at enqueue from the rule's
-- wall-clock window. Storing the resolved instant rather than re-deriving it
-- on every flush is what makes a DST transition a non-event here: the arithmetic
-- happens once, in a known zone, at a known local time.
CREATE TABLE IF NOT EXISTS alert_pending (
  id           TEXT PRIMARY KEY,   -- ULID
  rule_id      TEXT NOT NULL,
  finding_id   TEXT NOT NULL,
  finding_json TEXT NOT NULL,      -- the Finding as it fired
  kind         TEXT NOT NULL,      -- new|escalated|resolved
  at           INTEGER NOT NULL,   -- when the event fired
  flush_at     INTEGER NOT NULL,   -- when it becomes deliverable
  reason       TEXT NOT NULL       -- why it was held, for the delivery log
);

CREATE INDEX IF NOT EXISTS idx_alert_pending_flush ON alert_pending (flush_at);
CREATE INDEX IF NOT EXISTS idx_alert_pending_rule ON alert_pending (rule_id);
