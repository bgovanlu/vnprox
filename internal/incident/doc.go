// Package incident implements T-2804's incident mode: one timeline that
// stitches the diagnosis ladder, captures, findings, recent flows, the
// changeset history and the T-2704 point-in-time diff into a single
// chronological view of a window an operator is investigating.
//
// # An incident is a VIEW, not a mode
//
// This is the whole design, and every structural decision below follows from
// it. Opening an incident:
//
//   - starts no collector, subscribes to no stream, and arms no timer;
//   - copies no event into any table — there is no incident_events table, and
//     the migration (0041) says why there must never be one;
//   - changes nothing about what the daemon does, so two daemons, one with an
//     incident open and one without, do exactly the same work.
//
// A timeline is assembled at READ time by querying history vnprox already
// records — finding_events, audit_log, capture_sessions, flow_samples — over
// the incident's window. GET /history/events (T-1007) already does this for
// two of those four sources; this package widens it to five and adds the one
// class of event nothing else records, the operator's own annotations.
//
// The consequence that makes the feature worth having is that an incident can
// be opened RETROACTIVELY, over a window that closed hours ago, and contains
// exactly what an incident opened live at that moment would have contained.
// Nobody has to remember to press "start incident" before the network breaks.
// That is acceptance criterion 1, and it is a property of the schema rather
// than of anyone's discipline: the only input to the assembly is the window.
//
// # Read-only, structurally
//
// Every seam this package holds (Store aside, which owns only the incident
// record and its annotations) has read methods and nothing else:
// FindingEventSource, AuditSource, CaptureSource, FlowSource and
// TopologyDiffService cannot collect, poll, stage or apply, because those
// interfaces give them nowhere to say so. This is the same "small interface,
// no mutation method" shape internal/mcp and internal/gitsync use for the
// same reason — a service that cannot reach a collector cannot grow a call to
// one by accident.
//
// # What the timeline does not claim
//
// Three limits are reported in the timeline itself rather than documented
// somewhere a reader will not be:
//
//   - Flows are capped (Config.FlowLimit). A window with more flow samples
//     than the cap reports source status "truncated" with the cap, never a
//     silently short list.
//   - The T-2704 diff covers exactly the paths its own Coverage names
//     (/etc/network/interfaces today; SDN entities are not diffed). The
//     caveat is DERIVED from that Coverage, so it cannot describe a scope the
//     diff has stopped having.
//   - A node captured at only one end of the diff range is named. An absent
//     entity on such a node is not a deletion, and the timeline says so
//     instead of letting a reader assume it.
//
// A diff that cannot be computed is surfaced with the change engine's own
// message — which names the snapshots that DO exist — never swallowed into an
// empty diff, which would read as "nothing changed".
package incident
