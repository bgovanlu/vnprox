// Package topology projects the T-103 inventory graph into the renderable
// topology contract documented in docs/features/topology.md §3 and serves
// it (docs/api.md's "Inventory & topology" and "WebSocket /api/ws"
// sections): GET /topology (layered nodes/edges with status/badges and
// server-side layer/node/vlan filtering), GET /inventory/{ref} (entity
// detail + provenance), GET /inventory/search (ranked fuzzy search), and a
// WebSocket hub fanning out topology.delta events translated from
// internal/collect's inventory.Delta callback.
//
// This package never mutates internal/inventory, internal/collect, or
// internal/auth — it only reads inventory.Graph.Snapshot() and translates
// inventory.Delta. See each file's doc comment for the specific
// documented-but-underspecified decisions this package had to make
// (notably: the `layer` field's exact string vocabulary, the meaning of
// Topology.Layers, the guest-collapse synthetic node id scheme, and what
// "raw source" means for GET /inventory/{ref} given the graph does not
// retain original interfaces(5)/PVE JSON text past ingestion) — all
// flagged again in the T-106 completion report for T-107 to confirm
// against.
package topology
