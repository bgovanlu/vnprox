-- 0041_incidents.sql — T-2804 "Incident mode".
--
-- Two tables, and what they do NOT hold is the design.
--
-- An incident is a VIEW over history vnprox already records, not a mode the
-- daemon runs in. Opening one starts no collector, subscribes to no stream and
-- copies no event: the timeline is assembled at read time by querying
-- finding_events, audit_log, capture_sessions and flow_samples over the
-- incident's window, exactly as GET /history/events already does for two of
-- those four. So there is no `incident_events` table here, and there must
-- never be one — an event table would make an incident a recorder, which is
-- precisely the thing the card refuses ("collects no data that is not already
-- collected"), and it would make a retroactive incident structurally unable to
-- contain what a live one did.
--
-- What genuinely has nowhere else to live is stored, and only that:
--
--   incidents             — the window (start/end), who opened it, and whether
--                           it is open. The window is the only input the
--                           timeline query needs.
--   incident_annotations  — the operator's own observations, which are the one
--                           class of timeline event no other subsystem records.
--
-- Because no event is copied in, CLOSING an incident deletes nothing (there is
-- nothing of the timeline here to delete) and REOPENING re-runs the same query
-- over the same window — T-2804 acceptance criterion 5 is a property of this
-- schema, not of the code that reads it.
--
-- App-owned data per CLAUDE.md's storage rule: an incident is vnprox's own
-- bookkeeping about an operator's investigation. It holds no PVE-authoritative
-- config and it is never a shadow copy of one.
--
-- Migrations are forward-only: this file, once released, must never be edited
-- again. Schema changes land as a new NNNN_*.sql file with a higher version.

CREATE TABLE IF NOT EXISTS incidents (
  id TEXT PRIMARY KEY,
  -- Operator-typed free text; scrubbed on the way into an export, never on
  -- the way in here (the store keeps what the operator wrote).
  title TEXT NOT NULL,
  -- 'open' | 'closed'. Closed is a status, not a deletion.
  status TEXT NOT NULL,
  opened_by TEXT NOT NULL,
  -- When the incident record was created. Distinct from started_at on
  -- purpose: a retroactively-opened incident has opened_at > started_at, and
  -- that difference is the evidence that a view over a past window is what
  -- this feature actually is.
  opened_at INTEGER NOT NULL,
  -- The window. started_at is inclusive; ended_at is inclusive, with 0
  -- meaning "runs to now" (an incident still unfolding). Closing an
  -- open-ended incident freezes ended_at at the close instant; reopening one
  -- sets it back to 0 so the window is live again.
  started_at INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0,
  closed_at INTEGER NOT NULL DEFAULT 0
);

-- The list view is "most recent first", and the window start is what an
-- operator orders by, not the row's creation time.
CREATE INDEX IF NOT EXISTS incidents_started_at_idx ON incidents (started_at DESC);

CREATE TABLE IF NOT EXISTS incident_annotations (
  id TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  -- Annotation timestamps are supplied, not inferred: "the link flapped at
  -- 14:02" is a note about 14:02, written at 14:20. An annotation whose
  -- caller supplies no timestamp gets the current time, which is the same
  -- thing said the boring way.
  at INTEGER NOT NULL,
  author TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS incident_annotations_incident_idx
  ON incident_annotations (incident_id, at);
