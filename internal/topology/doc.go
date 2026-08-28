// SPDX-License-Identifier: Apache-2.0

// Package topology projects the T-103 inventory graph into the renderable
// topology contract documented in docs/features/topology.md §3 and serves
// it (docs/api.md's "Inventory & topology" and "WebSocket /api/ws"
// sections): GET /topology (layered nodes/edges with status/badges and
// server-side layer/node/vlan filtering), GET /inventory/{ref} (entity
// detail with per-field provenance and per-source raw source text — the
// verbatim interfaces(5) stanza / PVE API object JSON the inventory graph
// retains via inventory.Snapshot.RawSource), GET /inventory/search (ranked
// fuzzy search), and a WebSocket hub fanning out topology.delta events
// translated from internal/collect's inventory.Delta callback.
//
// The Topology response additionally carries an optional per-source
// Staleness section (docs/features/topology.md §5's greyed-band/banner
// state). Project itself stays a pure function of an inventory snapshot;
// internal/api's /topology handler decorates the projection with staleness
// derived from collect.Collector.Status().
//
// This package never mutates internal/inventory, internal/collect, or
// internal/auth — it only reads inventory.Graph.Snapshot() and translates
// inventory.Delta. See each file's doc comment for the specific
// documented-but-underspecified decisions this package had to make
// (notably: the `layer` field's exact string vocabulary, the meaning of
// Topology.Layers, and the guest-collapse synthetic node id scheme).
package topology
