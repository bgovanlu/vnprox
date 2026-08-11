-- 0043_digest_schedules.sql — T-2807 "Scheduled digest reports".
--
-- Two tables, and both of them exist for a reason the feature would be wrong
-- without.
--
-- `digest_schedules` is the schedule ITSELF, in the database rather than in
-- config.toml. That is the whole of T-2807 AC5: a schedule read from a file is
-- a schedule that needs a daemon restart to change, and the runner re-reads
-- this row on every tick precisely so it does not. One row per schedule id;
-- the daemon uses exactly one ('default'), and the table is keyed rather than
-- single-row so a later per-tenant schedule is an INSERT and not a migration.
--
-- `digest_runs` is the BASELINE. T-2807 AC2 requires deltas to be computed
-- against the previous digest rather than against an arbitrary window, and
-- requires a first-ever digest to say it has no baseline instead of showing a
-- delta against zero. Both of those are properties of "is there a previous
-- run row, and what did it say" — so the previous digest's own summary has to
-- be durable. Without this table the only available baseline would be "some
-- window ending now", which is the arbitrary window the card refuses.
--
-- What is deliberately NOT here:
--
--   * No copy of the rendered digest. The document is regenerated from the
--     live surfaces (posture, findings, finding_events) and delivered; storing
--     it would make this table an archive of stale prose and would be the
--     shadow-copy-of-derived-state this schema's top-level rule forbids.
--   * No recipient table. Recipients are `alert_rules` rows — T-2407's
--     existing targets — and `rule_ids_json` below is a filter over them, not
--     a second address book. A recipient list that could disagree with the
--     alert targets would be exactly the second delivery path the card asks
--     this feature not to build.
--   * No delivery log. Delivery attempts are recorded in `alert_deliveries`
--     by the same WebhookNotifier every other alert goes through, so a digest
--     failure reads identically to any other failed alert.
--
--   digest_schedules
--     id            schedule key; the daemon's is 'default'.
--     enabled       0/1. A schedule that exists but is off is a real state:
--                   it preserves the operator's cadence while silencing it.
--     every_sec     the cadence in seconds (a week is 604800). The next
--                   digest is due at the previous run's period_end + this, so
--                   changing it moves the next digest immediately.
--     rule_ids_json JSON array of alert_rules.id to deliver to, or NULL for
--                   "every enabled rule that matches", which is the ordinary
--                   fan-out T-2407 already does.
--     updated_at    when the schedule last changed.
--     updated_by    who changed it, for the audit trail.
--
--   digest_runs
--     id              ULID.
--     schedule_id     the schedule that produced it.
--     period_start    the covered window's start — the PREVIOUS run's
--                     period_end, which is what makes the window
--                     non-arbitrary. 0 on a first-ever digest, which is also
--                     how "no baseline" is recognised.
--     period_end      the covered window's end; the next run's period_start.
--     generated_at    when the digest was rendered.
--     posture_overall the 0..100 score this digest carried, or -1 for "there
--                     was no posture score". -1 rather than NULL so the next
--                     digest can distinguish "scored 0" (bad) from "not
--                     scored" (unknown) without a nullable-column branch —
--                     the same not-evaluated sentinel discipline
--                     internal/posture.NotEvaluatedScore already uses.
--     opened_count    findings opened in the window.
--     closed_count    findings closed in the window.
--     drift_count     unresolved drift at generation time.
--     capacity_count  capacity projections crossing the horizon.
--     quiet           1 when the digest had nothing to report (the one-line
--                     form). Recorded so "why was last week's digest one
--                     line" is answerable from the row.
--     status          'delivered' | 'failed' | 'skipped'.
--     detail          human-readable outcome, including a delivery failure's
--                     own message. Never a credential: the only thing written
--                     here is the notifier's returned error, which names a
--                     rule id and an HTTP status, never a target secret.
--
-- Migrations are forward-only: once released, never edit this file.

CREATE TABLE IF NOT EXISTS digest_schedules (
    id            TEXT PRIMARY KEY,
    enabled       INTEGER NOT NULL DEFAULT 0,
    every_sec     INTEGER NOT NULL,
    rule_ids_json TEXT,
    updated_at    INTEGER NOT NULL,
    updated_by    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS digest_runs (
    id              TEXT PRIMARY KEY,
    schedule_id     TEXT NOT NULL,
    period_start    INTEGER NOT NULL DEFAULT 0,
    period_end      INTEGER NOT NULL,
    generated_at    INTEGER NOT NULL,
    posture_overall INTEGER NOT NULL DEFAULT -1,
    opened_count    INTEGER NOT NULL DEFAULT 0,
    closed_count    INTEGER NOT NULL DEFAULT 0,
    drift_count     INTEGER NOT NULL DEFAULT 0,
    capacity_count  INTEGER NOT NULL DEFAULT 0,
    quiet           INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT ''
);

-- The baseline lookup is "the newest run for this schedule", which is the
-- only read path this table has.
CREATE INDEX IF NOT EXISTS idx_digest_runs_schedule_period
    ON digest_runs (schedule_id, period_end DESC);
