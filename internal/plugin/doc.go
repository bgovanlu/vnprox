// Package plugin is T-1702's capability-scoped extension SDK: stable, versioned
// extension points for the read/discovery/ingest/render seams third parties
// keep asking to extend — switch drivers (T-1205's internal/switchdrv), flow/
// telemetry ingestors (T-1002), finding producers, ingress discoverers (T-1406's
// internal/ingress), and dashboard tiles (T-904) — plus a registry that installs,
// enables, disables, and uninstalls plugins with an audited capability scope.
//
// # The safety boundary (docs/security.md, docs/architecture.md §10)
//
// A plugin extends read/discovery/ingest/render seams. It is NOT a new mutation
// path. Two structural invariants make that true, enforced server-side rather
// than by convention:
//
//   - A plugin can stage, never bypass. The only change-engine surface handed to
//     plugin code is the Stager interface (stager.go): exactly Create and
//     Validate, the stage-only pair. No Apply/Confirm/Rollback method is
//     reachable from a plugin, in-process or over the out-of-process transport.
//     A human (or the confirm machinery) remains the sole apply authority, the
//     same guarantee every mutation has had since T-205. This is verified by an
//     interface-surface test, not asserted in prose (T-1702 AC3).
//
//   - Capability scope is a ceiling, not a grant. A plugin declares capabilities
//     drawn from internal/auth's existing AllCaps vocabulary — this SDK adds no
//     new privilege beyond what change-engine ops already gate. Every op a plugin
//     tries to stage is mapped to the capability that op class already requires
//     (caps.go's RequiredCap); if the plugin's declared scope does not cover it,
//     the op is rejected before it ever reaches internal/change (T-1702 AC2). An
//     extension point a plugin's scope does not cover cannot be registered at all.
//
// The out-of-process boundary (procshim) is real but bounded: an out-of-process
// plugin is a subprocess vnproxd spawns and supervises, never given direct DB or
// file access — it speaks only the length-delimited JSON wire protocol
// (procshim/wire.proto) over its own stdio, and every call it makes back into
// vnprox goes through the same capability-scoped Stager. Its residual risk
// (unconstrained OS-level network egress from the plugin's own process) is stated
// plainly in the report rather than engineered away, mirroring T-1205's
// stated-not-hidden residual-risk pattern.
//
// # Extension points
//
// The five v1 extension points are enumerated by ExtensionPoint (caps.go). Two
// reuse an already-frozen interface verbatim — SwitchDriver (switchdrv) and
// IngressDiscoverer (ingress) — proving the SDK does not fork existing contracts;
// the other three (FlowIngestor, FindingProducer, DashboardTileProvider) are
// defined here at v1 (interfaces.go). Built-ins register through the same
// registry as third-party plugins, so the interfaces are proven by use, not just
// documented (T-1702 AC4).
package plugin
