// SPDX-License-Identifier: Apache-2.0

// Package annotate is T-2806's map annotation layer: the read model over
// the two app-owned tables that hold what an operator KNOWS about the map,
// as opposed to what the map shows — free-text notes pinned to an entity
// ref (`annotations`), and labelled regions drawn on the canvas
// (`map_regions`).
//
// Nothing here is a shadow copy of PVE config (CLAUDE.md's storage rule).
// A note's `Ref` names a PVE entity, but the row carries only the sentence
// a human typed about it; a region carries a rectangle in the canvas's own
// coordinate space, which corresponds to no PVE object at all.
//
// This package exists because two properties of the feature must be
// decided in exactly one place, and neither can live in the store:
//
//   - Expiry is computed at READ time (T-2806 AC3) against the injected
//     Now clock. There is no expiry sweep and no `WHERE expires_at > ...`
//     in any query, so a daemon that was stopped for a week cannot come
//     back and serve a note that expired on day one — the note's staleness
//     is recomputed on the read that would have displayed it. This is the
//     same discipline internal/presence applies to entity_locks.
//
//   - Orphaning is DERIVED at read time from the live inventory (T-2806
//     AC2), never stored. An annotation whose entity no longer exists is
//     retained and reported Orphaned; nothing in this package, the store,
//     or any retention job deletes it. The card's reason is the whole
//     point of the feature: "the note may be the only record of why the
//     entity was removed." Deriving instead of storing also keeps the
//     boundary honest — whether an entity exists is PVE's truth, read
//     fresh, never a persisted mirror.
//
// The orphan derivation deliberately fails SAFE. When the inventory has no
// entities at all (a daemon in degraded mode, or one that has not completed
// its first collection), nothing is reported orphaned: mass-labelling every
// note "the entity is gone" because vnprox cannot currently see any entity
// would be the most alarming possible way to be wrong.
package annotate
