package plugin

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
)

// APIVersion is the frozen SDK interface version every v1 extension point is
// pinned at. It is recorded on every installed plugin row (plugins.api_version)
// and compatibility-checked at register time: a plugin built against a version
// this build does not understand is refused rather than dispatched. The
// deprecation policy for advancing past v1 lives in docs/architecture.md §10 —
// widening a v1 interface is a breaking change that mints v2, never an in-place
// edit, exactly the API-stability precedent §10 already sets.
const APIVersion = "v1"

// SwitchDriver is the physical-switch extension point, reused verbatim from
// T-1205's internal/switchdrv — the SDK deliberately does not fork the contract.
// A switch-driver plugin is invoked BY the change engine during a bounded
// switch.port op (never the other way round): it configures exactly VLAN
// membership, port description, and LACP on one port and touches nothing else,
// and it only runs behind T-1205's existing dark-by-default feature guard. It is
// the one write-adjacent extension point, and it remains bounded by that op
// family's own capability gate (RequiredCap("switch.port.update") == netWrite).
type SwitchDriver = switchdrv.SwitchDriver

// IngressDiscoverer is the reverse-proxy discovery extension point, reused
// verbatim from T-1406's internal/ingress: exactly one read-only method taking a
// Target and returning its discovered ProxyState. A plugin registers a new proxy
// vendor by attaching an IngressDiscoverer for a new Kind; it issues only
// read-only requests against the operator-configured target it is handed.
type IngressDiscoverer = ingress.IngressDiscoverer

// FlowIngestor is the flow/telemetry ingestion extension point (T-1002 becomes
// pluggable). A plugin decodes one raw datagram from a flow exporter — a vendor
// export format the built-in NetFlow/IPFIX/sFlow decoders do not speak — into
// zero or more normalized flow.Record values. It is a pure read/decode seam: it
// is handed bytes and returns records, and has no access to the store, the
// change engine, or the network beyond the datagram it is given. node is the
// cluster node the datagram was observed on (stamped onto each Record.Node);
// src is the exporter's source address, for the ingestor's own template/context
// keying.
type FlowIngestor interface {
	// Ingest decodes one exporter datagram into normalized records. Returning a
	// nil slice with a nil error (e.g. a template-only packet that produces no
	// samples) is valid and expected. An error is logged and the datagram
	// dropped; it never propagates as a daemon fault.
	Ingest(ctx context.Context, node, src string, payload []byte) ([]flow.Record, error)
}

// FindingProducer is the finding-pack extension point: a plugin contributes
// additional read-only findings (a "finding pack") alongside the built-in
// producers in internal/findings. It is strictly read-only — it returns findings
// computed from whatever read-models the host exposes to it; it can never apply a
// remediation itself. A finding may be marked Fixable, but the fix is staged
// through the ordinary change-engine flow by a human, never by the producer.
type FindingProducer interface {
	// Produce returns this pack's current findings. An error degrades this one
	// pack (its findings are omitted) without failing the aggregate findings
	// response, the same graceful-degradation contract a dead out-of-process
	// plugin gets (T-1702 AC5).
	Produce(ctx context.Context) ([]findings.Finding, error)
}

// Tile is one dashboard tile a DashboardTileProvider contributes: a small,
// read-only, named summary datum with an optional deep-link into its owning page,
// mirroring the shape T-904's built-in tiles render client-side. It carries no
// action — a tile is display-only, never a control surface.
type Tile struct {
	// ID is the tile's stable identifier within its provider, for React keying.
	ID string `json:"id"`
	// Title is the tile's display heading.
	Title string `json:"title"`
	// Value is the headline datum, pre-formatted for display.
	Value string `json:"value"`
	// Detail is optional secondary text under the value.
	Detail string `json:"detail,omitempty"`
	// Link is an optional in-app route the tile deep-links to (e.g. "/findings").
	Link string `json:"link,omitempty"`
	// Severity is an optional advisory level ("info"|"warn"|"critical") the UI
	// uses purely for tile coloring; empty means neutral.
	Severity string `json:"severity,omitempty"`
}

// DashboardTileProvider is the dashboard-tile extension point (T-904 becomes
// pluggable). A plugin contributes read-only tiles to the home dashboard. Like
// FindingProducer it is display-only: a tile never issues a mutating request and
// the provider has no change-engine access.
type DashboardTileProvider interface {
	// Tiles returns this provider's current tiles. An error degrades this one
	// provider (its tiles are omitted) without failing the dashboard.
	Tiles(ctx context.Context) ([]Tile, error)
}
