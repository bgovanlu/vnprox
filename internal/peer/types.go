package peer

import (
	"encoding/json"
	"net/http"

	"github.com/bgovanlu/vnprox/internal/host"
)

// Wire shapes for the documented /api/peer/host/* routes (docs/api.md's
// Peer API section names the routes; the request/response bodies are this
// package's own internal-only contract, never exposed to the SPA).

// interfacesResponse is GET /api/peer/host/interfaces's body.
type interfacesResponse struct {
	Content string `json:"content"`
}

// statsResponse is GET /api/peer/host/stats's body.
type statsResponse struct {
	Stats map[string]host.IfaceStats `json:"stats"`
}

// servicesResponse is GET /api/peer/host/services's body (T-602).
type servicesResponse struct {
	Services map[string]bool `json:"services"`
}

// stageRequest is POST /api/peer/host/stage-interfaces and
// POST /api/peer/host/restore's shared body shape: the target node and the
// full interfaces(5) content to write.
type stageRequest struct {
	Node    string `json:"node"`
	Content string `json:"content"`
}

// nodeRequest is POST /api/peer/host/{ifreload,discard-staged}'s body: just
// the target node.
type nodeRequest struct {
	Node string `json:"node"`
}

// linksResponse is GET /api/peer/host/links's body (T-303: the
// netlink-equivalent link state a remote peer-API-backed host.Reader needs
// for internal/collect's cluster-wide host poller — docs/architecture.md
// §1's "peer vnproxd instances on other cluster nodes for node-local data").
type linksResponse struct {
	Links []host.LinkState `json:"links"`
}

// neighborsResponse is GET /api/peer/host/neighbors's body (T-805: a node's
// resolved ARP/IPv6-neighbor table — internal/ipam.NeighborSource's
// peer-routed fan-out target, following the `links`/`fdb` additive-route
// precedent this doc comment on linksResponse describes).
type neighborsResponse struct {
	Neighbors []host.Neighbor `json:"neighbors"`
}

// containerInteriorResponse is GET /api/peer/host/container-interior's
// body (T-1304: an lxc guest's raw host-side network-namespace read set —
// the string fields are host.ContainerInteriorRaw's []byte fields
// rendered as plain text, following dhcpLeasesResponse's own
// []byte-as-string precedent below rather than base64, since every field
// here is textual command output).
type containerInteriorResponse struct {
	AddrJSON   string `json:"addrJson"`
	RouteJSON  string `json:"routeJson"`
	ResolvConf string `json:"resolvConf"`
	Sockets    string `json:"sockets"`
}

// containerPingResponse is GET /api/peer/host/container-ping's body
// (T-1304).
type containerPingResponse struct {
	Reachable bool `json:"reachable"`
}

// firewallLogResponse is GET /api/peer/firewall/log's body (T-505: one
// node's own pve-firewall log tail/follow increment — internal/fwlog.
// Service.fetch calls this for every non-local node in the cluster,
// exactly the way internal/collect's host poller calls Links for a
// remote node's netlink state).
type firewallLogResponse struct {
	NextCursor string   `json:"nextCursor"`
	Lines      []string `json:"lines"`
}

// fdbResponse is GET /api/peer/host/fdb's body (T-306: the MAC/FDB
// browser's node-local read — every bridge's forwarding-database table,
// flattened and bridge-tagged; see host.FlattenFDB).
type fdbResponse struct {
	Entries []host.FDBRow `json:"entries"`
}

// frrResponse is GET /api/peer/host/frr/{bgp-summary,evpn-vni}'s shared
// body shape (T-404): Available is false (Content omitted) when node runs
// no FRR at all (host.ErrFRRUnavailable), true with the raw vtysh JSON
// output as Content otherwise. Wrapping rather than passing the raw vtysh
// bytes straight through (unlike handleLLDP) is what lets "FRR entirely
// absent" travel over the wire as a clean, distinguishable 200 instead of
// an error status.
type frrResponse struct {
	Content   json.RawMessage `json:"content,omitempty"`
	Available bool            `json:"available"`
}

// dhcpLeasesResponse is GET /api/peer/host/dhcp-leases' body (T-406): the
// raw dnsmasq lease-file content, the same "{content: string}" shape
// interfacesResponse uses — no {available} envelope like frrResponse,
// since an empty Content is itself a perfectly clean "no leases" result
// (see HostReader.DHCPLeases' doc comment), not a distinct absent/error
// state to distinguish.
type dhcpLeasesResponse struct {
	Content string `json:"content"`
}

// AuditRecord is one row of GET /api/peer/audit's page (T-303). Its fields
// mirror docs/api.md's GET /audit list item shape field-for-field so
// internal/api's cluster merge can decode a peer's page directly into the
// same representation it renders locally, with no lossy conversion.
type AuditRecord struct {
	Username    string          `json:"username"`
	Action      string          `json:"action"`
	Target      string          `json:"target,omitempty"`
	ChangesetID string          `json:"changesetId,omitempty"`
	Result      string          `json:"result"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	ID          int64           `json:"id"`
	At          int64           `json:"at"`
}

// AuditFilter narrows GET /api/peer/audit exactly like docs/api.md's GET
// /audit query params. This is peer's own copy of that filter shape (rather
// than importing internal/store's store.AuditFilter) so this package never
// depends on internal/store — see server.go's AuditReader doc comment for
// why that direction matters.
type AuditFilter struct {
	User        string `json:"user,omitempty"`
	Action      string `json:"action,omitempty"`
	Target      string `json:"target,omitempty"`
	Result      string `json:"result,omitempty"`
	ChangesetID string `json:"changesetId,omitempty"`
	From        int64  `json:"from,omitempty"`
	To          int64  `json:"to,omitempty"`
}

// auditPageResponse is GET /api/peer/audit's body: one page of this node's
// own local audit log, same envelope shape as docs/api.md's GET /audit.
type auditPageResponse struct {
	NextCursor string        `json:"nextCursor,omitempty"`
	Items      []AuditRecord `json:"items"`
}

// SnapshotRecord is one row of GET /api/peer/snapshots' page (T-303),
// mirroring docs/api.md's GET /snapshots list item shape field-for-field.
type SnapshotRecord struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	ChangesetID string   `json:"changesetId,omitempty"`
	Note        string   `json:"note,omitempty"`
	Nodes       []string `json:"nodes"`
	TakenAt     int64    `json:"takenAt"`
}

// snapshotPageResponse is GET /api/peer/snapshots' body.
type snapshotPageResponse struct {
	NextCursor string           `json:"nextCursor,omitempty"`
	Items      []SnapshotRecord `json:"items"`
}

// FlowFilter narrows GET /api/peer/flows exactly like docs/api.md's GET
// /flows query params. This is peer's own copy of that filter shape (rather
// than importing internal/store's store.FlowFilter or internal/flow's
// Filter) so this package never depends on either — see AuditFilter's doc
// comment for the identical reasoning.
type FlowFilter struct {
	Guest  string `json:"guest,omitempty"`
	Subnet string `json:"subnet,omitempty"`
	Source string `json:"source,omitempty"`
	VLAN   int    `json:"vlan,omitempty"`
	Port   int    `json:"port,omitempty"`
	Proto  int    `json:"proto,omitempty"`
	FromTs int64  `json:"fromTs,omitempty"`
	ToTs   int64  `json:"toTs,omitempty"`
}

// FlowRecord is one row of GET /api/peer/flows' page (T-1002): the fields
// mirror docs/api.md's flow.Record shape field-for-field, plus ID — a
// peer-wire-only field (never surfaced in the public GET /flows response;
// internal/api strips it after using it as the cluster-merge sort tiebreak,
// the same role AuditRecord.ID plays for GET /audit) since flow.Record
// itself carries no stable identifier and unix-second timestamps alone
// collide often at realistic ingestion rates.
type FlowRecord struct {
	Node    string `json:"node"`
	SrcIP   string `json:"srcIp"`
	DstIP   string `json:"dstIp"`
	SrcRef  string `json:"srcRef,omitempty"`
	DstRef  string `json:"dstRef,omitempty"`
	Source  string `json:"source"`
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	Bytes   int64  `json:"bytes"`
	Packets int64  `json:"packets"`

	SrcPort        int `json:"srcPort,omitempty"`
	DstPort        int `json:"dstPort,omitempty"`
	Proto          int `json:"proto"`
	VLAN           int `json:"vlan,omitempty"`
	IngressIfIndex int `json:"ingressIfIndex,omitempty"`
	EgressIfIndex  int `json:"egressIfIndex,omitempty"`
}

// flowPageResponse is GET /api/peer/flows' body: one page of this node's
// own local flow_samples ring, same envelope shape as
// auditPageResponse/snapshotPageResponse — no partial/failedNodes (a peer
// only ever reports its own node-local page; the fan-out/merge happens on
// the calling daemon).
type flowPageResponse struct {
	NextCursor string       `json:"nextCursor,omitempty"`
	Items      []FlowRecord `json:"items"`
}

// installLLDPRequest is POST /api/peer/host/lldp/install's body: an
// explicit confirmation flag (docs/features/lldp-discovery.md §1's
// "changeset-like confirmation" for the guided-install flow).
type installLLDPRequest struct {
	Confirm bool `json:"confirm"`
}

// okResponse is the generic success body for the write endpoints.
type okResponse struct {
	OK bool `json:"ok"`
}

// armTimerRequest is POST /api/peer/timer/arm's body: T-304's local-timer
// protocol arms a rollback deadline on the receiving node before the
// coordinator's first mutating step for it (docs/features/
// change-management.md §4).
type armTimerRequest struct {
	ChangesetID string `json:"changesetId"`
	Node        string `json:"node"`
	Content     string `json:"content"`
	Deadline    int64  `json:"deadline"`
}

// timerRequest is POST /api/peer/timer/cancel and GET /api/peer/timer/
// status's shared identifying key.
type timerRequest struct {
	ChangesetID string `json:"changesetId"`
	Node        string `json:"node"`
}

// timerResponse is the body every /api/peer/timer/* route returns: the
// resulting (or current, for status) TimerRecord.
type timerResponse struct {
	Record TimerRecord `json:"record"`
}

// errorEnvelope mirrors docs/api.md's global error shape:
// {"error":{"code","message","details"}}. Used both to write server
// responses and to decode them back out client-side.
type errorEnvelope struct {
	Error struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	var env errorEnvelope
	env.Error.Code = code
	env.Error.Message = message
	writeJSON(w, status, env)
}
