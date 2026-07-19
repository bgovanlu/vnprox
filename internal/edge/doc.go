// Package edge implements T-1403's Edge & NAT cockpit projection: "how does
// traffic actually leave, and what's exposed inbound?" answered from
// already-collected data, never a second write path.
//
// This package is pure (like internal/sim, internal/fw.Resolve — no I/O of
// its own): ProjectRoutes and ProjectNAT take already-fetched per-node
// interfaces(5) file content plus an already-fetched SDN subnet list and
// project them into the read views docs/api.md's GET /edge/routes and
// GET /edge/nat serve. internal/api/edge.go is the thin HTTP adapter that
// gathers those inputs from already-wired services (the same
// ChangesetService.ReadRawInterfaces node-file read the raw interfaces
// editor uses, internal/sdn.Service.Tree, and an optional guest-correlation
// lookup) and calls this package.
//
// A nat.masquerade/nat.portforward/route.static rule has no dedicated
// store row anywhere in vnprox — its *only* record is the post-up/post-down
// stanza pair internal/change/ifaces/edgeop.go renders into
// /etc/network/interfaces, each line carrying a trailing marker comment
// (internal/host's EncodeNat*Marker/EncodeStaticRouteMarker) this package
// decodes back apart (internal/host's own DecodeNat*Marker/
// DecodeStaticRouteMarker — the identical decode path the mutator's own
// update/delete ops use to recover a rule's current state). This is what
// "surfacing inbound exposure must not itself become a write path" means in
// practice: GET /edge/nat and GET /edge/routes only ever parse; the on-disk
// file is the only place a nat.*/route.static.* op writes, full stop.
package edge
