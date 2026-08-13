-- 0045_map_annotations.sql — T-2806 "Map annotation layer".
--
-- Two additive things, both squarely inside CLAUDE.md's storage rule ("only
-- app-owned data ... never a shadow copy of PVE config"):
--
--   1. annotations.expires_at — an optional self-destruct date on a sticky
--      note, so "temporary until the switch swap" announces its own
--      staleness instead of quietly becoming permanent.
--   2. map_regions — labelled rectangles drawn on the canvas ("vendor-
--      managed, do not touch"), which describe an operator's own mental
--      grouping of the map and correspond to nothing PVE knows about.
--
-- What these rows are NOT:
--
--   * NOT a copy of PVE state. `annotations.ref` names a PVE entity, and a
--     region's rectangle may visually enclose several, but neither row says
--     anything about any entity's configuration — only what a human wrote
--     about it. The same category `layouts`, `annotations` (0006) and
--     `entity_locks` (0044) already occupy, per docs/architecture.md §7.
--
--   * NOT authoritative about whether the annotated entity still exists.
--     That is read from the live inventory at read time (internal/annotate),
--     never stored here. An annotation whose entity has been deleted is
--     RETAINED and reported as orphaned — see below.
--
--   expires_at (both tables)
--     unix seconds, 0 = never expires (the default, and what every row
--     predating this migration gets). Expiry is judged at READ time against
--     the daemon's injected clock, never in a background job and never in
--     SQL against a database-side now(): a stopped daemon must not be able
--     to leave an expired note on display, and one clock must decide expiry
--     everywhere. This is the identical discipline 0044's entity_locks.
--     expires_at documents; the two features share the reasoning, not the
--     code.
--
--     There is deliberately NO sweep that deletes expired rows. An expired
--     note stops being displayed but stays readable (and deletable) through
--     the includeExpired read — the note is often the only record of why
--     something was done, and this feature exists to preserve exactly that.
--
--   map_regions
--     id          ULID, like annotations.
--     label       the region's free text; opaque to vnproxd, escaped by
--                 every renderer (docs/api.md's Map annotation layer).
--     x/y/w/h     the rectangle in the canvas's own GRAPH coordinate space
--                 (web/src/topology/canvasScene.ts) — the same space node
--                 positions use, so a region keeps its relationship to the
--                 entities it encloses under any pan/zoom.
--     color       an optional client-chosen palette key ('' = default).
--     created_by  the author, for display; NOT an ownership key. Regions are
--                 a shared team artifact exactly like annotations: any
--                 netRead-capable user sees and may remove any region.
--
--     Regions live in their OWN table rather than inside `layouts`'
--     layout_json for the reason T-2806 AC5 states: `layouts` is per-user
--     and is rewritten wholesale every time the canvas layout changes, so a
--     region stored there would be private to one user and would be
--     destroyed by the next auto-save. A separate, shared table is what
--     makes "regions persist across layout changes and view switches" a
--     property of the schema rather than a promise about client behaviour.
--
-- Migrations are forward-only: once released, never edit this file.

ALTER TABLE annotations ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS map_regions (
    id         TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    x          REAL NOT NULL,
    y          REAL NOT NULL,
    w          REAL NOT NULL,
    h          REAL NOT NULL,
    color      TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL DEFAULT 0
);
