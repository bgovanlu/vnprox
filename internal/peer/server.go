// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/host"
)

// defaultPeerPageLimit and maxPeerPageLimit bound GET /audit and GET
// /snapshots' ?limit= query param on the peer side, mirroring
// internal/api's own audit/snapshots page-size conventions (docs/api.md's
// pagination convention) so a peer's per-request page size never grows
// unbounded regardless of what a (trusted, cluster-secret-authenticated)
// caller requests.
const (
	defaultPeerPageLimit = 50
	maxPeerPageLimit     = 200
)

// defaultPeerLogLines/maxPeerLogLines bound GET /api/peer/firewall/log's
// own ?maxLines= (T-505) — a materially higher ceiling than
// maxPeerPageLimit's audit/snapshot-page tuning, since internal/fwlog's
// storm handling (AC3: a 10k-lines/min fixture) needs a single peer fetch
// to be able to catch up on a real burst rather than trickling in 200
// lines at a time across many polling ticks.
const (
	defaultPeerLogLines = 500
	maxPeerLogLines     = 5000
)

// defaultMaxBodyBytes bounds request bodies the peer server will read
// (docs/data-model.md's largest per-node artifact is the interfaces(5)
// file, which is at most a few hundred KB even on a heavily-configured
// node) — generous headroom against an abusive/buggy caller, not a
// realistic limit.
const defaultMaxBodyBytes = 4 << 20 // 4 MiB

// HostReader is the read-side dependency for the documented
// `/api/peer/host/{interfaces,lldp,stats,links,fdb}` routes. host.Reader
// satisfies this directly: Go allows assigning a wider interface value to
// a narrower interface-typed field/parameter whenever its method set is a
// superset, so host.NewReal() (or a host.FixtureReader) can be passed to
// ServerOptions.Reader with no adapter.
//
// GET /api/peer/host/fdb (T-306) is deliberately *not* a separate interface
// method: a bridge's FDB is already embedded in Links()' BridgeDetail, so
// handleFDB below just calls Links and flattens it (host.FlattenFDB) —
// adding a distinct HostReader.FDB method would mean either Real doing a
// second, redundant netlink dump per request or FixtureReader needing a
// second adapter path, for data this interface's existing method already
// carries. The route exists as its own documented endpoint (T-301's
// deviation note flagged its absence as T-306's deliverable) because a
// caller that only wants the forwarding table — the MAC/FDB browser this
// route serves, docs/features/lldp-discovery.md §4 — shouldn't have to
// pull every bridge's full VLAN table and every physical NIC's link state
// over the wire to get it.
type HostReader interface {
	// InterfacesFile returns node's literal /etc/network/interfaces (or
	// interfaces.new when includePending) content.
	InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)

	// LLDP returns node's raw LLDP neighbor JSON.
	LLDP(ctx context.Context, node string) ([]byte, error)

	// Stats returns node's interface counters, keyed by interface name.
	Stats(ctx context.Context, node string) (map[string]host.IfaceStats, error)

	// Services returns node's systemd unit status for T-602's watched
	// service set (host.WatchedServices).
	Services(ctx context.Context, node string) (map[string]bool, error)

	// Links returns node's netlink-equivalent link state (physical NICs,
	// bonds, bridges, VLAN sub-interfaces), including bond runtime detail
	// and bridge VLAN/FDB tables. T-303 added this to the interface (and
	// the corresponding GET /api/peer/host/links route below) so
	// internal/collect's host poller can fan a remote node's netlink state
	// in through the peer API exactly like it already does for the
	// interfaces file, LLDP, and stats — host.Reader (the type every
	// production Reader value already is) has always had this method (see
	// that package's doc comment), so no wiring changes are needed to
	// start serving it.
	Links(ctx context.Context, node string) ([]host.LinkState, error)

	// FRRBGPSummary returns node's raw `vtysh -c "show bgp summary json"`
	// output (T-404's EVPN/BGP observability, docs/features/sdn.md §3).
	// Returns an error wrapping host.ErrFRRUnavailable when node runs no
	// FRR at all; handleFRRBGPSummary below translates that into the
	// documented `{available:false}` response rather than an error status.
	FRRBGPSummary(ctx context.Context, node string) ([]byte, error)

	// FRREVPNVNI returns node's raw `vtysh -c "show evpn vni json"`
	// output. Same host.ErrFRRUnavailable convention as FRRBGPSummary.
	FRREVPNVNI(ctx context.Context, node string) ([]byte, error)

	// DHCPLeases returns node's raw dnsmasq DHCP lease-file content
	// (T-406, docs/features/sdn.md §5), the concatenation of every
	// currently-configured SDN zone's dnsmasq .leases file on node.
	// Unlike FRRBGPSummary/FRREVPNVNI, an empty result is not a distinct
	// "unavailable" condition worth its own error/available-flag
	// convention — a node with no DHCP-managed SDN zone simply has no
	// leases, the common case.
	DHCPLeases(ctx context.Context, node string) ([]byte, error)

	// Neighbors returns node's resolved ARP (IPv4) / IPv6-neighbor table
	// (T-805, docs/features/ipam.md §1's ARP/neighbor enrichment source),
	// already filtered to resolved states by host.Reader.Neighbors — see
	// that method's doc comment.
	Neighbors(ctx context.Context, node string) ([]host.Neighbor, error)

	// ContainerInterior returns an lxc guest's raw host-side
	// network-namespace read set (T-1304's guest-interior inspector — see
	// host.ContainerInteriorRaw's doc comment). The lxc counterpart of the
	// qemu path's guest-agent exec, which reaches PVE's own
	// cluster-transparent REST API directly and so needs no peer route of
	// its own.
	ContainerInterior(ctx context.Context, node string, vmid int) (host.ContainerInteriorRaw, error)

	// ContainerPing returns whether targetIP answered a single best-effort
	// ping issued from inside vmid's network namespace on node (T-1304's
	// default-gateway reachability check).
	ContainerPing(ctx context.Context, node string, vmid int, targetIP string) (bool, error)
	// Conntrack returns node's live conntrack/NAT table (T-1305, docs/api.md
	// Conntrack section) — the remote-node counterpart of a local
	// host.Reader.Conntrack call, GET /conntrack's cluster fan-out
	// dependency.
	Conntrack(ctx context.Context, node string) ([]host.ConntrackEntry, error)
	// IPv6RA returns node's own bounded, host-local IPv6 RA/DHCPv6
	// observation (T-1404) — the remote-node counterpart of a local
	// host.Reader.IPv6RA call, GET /ipv6/segments' cluster fan-out
	// dependency.
	IPv6RA(ctx context.Context, node string) ([]host.IPv6RAObservation, error)

	// MDB returns node's raw `bridge -d -j mdb show` output (T-3902's
	// multicast/MDB browser) — the remote-node counterpart of a local
	// host.Reader.MDB call, GET /mdb's cluster fan-out dependency. Unlike
	// FDB (embedded in Links()), there is no netlink dump for MDB state
	// (see host/mdb.go's doc comment), so this is its own interface method
	// rather than a Links()-derived route, mirroring FRRBGPSummary/
	// DHCPLeases' own exec-then-forward shape.
	MDB(ctx context.Context, node string) ([]byte, error)
}

// FirewallLogReader is the peer-server-side dependency for
// GET /api/peer/firewall/log (T-505): one node's own pve-firewall log,
// tail-or-follow depending on cursor. Its signature mirrors
// internal/fwlog.Source (minus that interface's `reset` return value,
// which this route folds into "nothing new yet" rather than exposing —
// see handleFirewallLog) so cmd/vnproxd's wiring can pass a
// *fwlog.FileSource straight through with a one-line adapter. Declared
// against this package's own signature (not importing internal/fwlog
// directly) for the same import-direction reason as AuditReader/
// SnapshotReader below: internal/peer must not depend on internal/fwlog.
type FirewallLogReader interface {
	FirewallLogTail(ctx context.Context, node, cursor string, maxLines int) (lines []string, nextCursor string, err error)
}

// AuditReader is the peer-server-side dependency for GET /api/peer/audit
// (T-303): one node's own local audit log, filtered and cursor-paginated
// exactly like docs/api.md's GET /audit. internal/api's cluster fan-out
// re-queries every peer with the same filter+cursor and merges the
// returned pages with its own local page — see internal/api's audit
// cluster-merge code. Declared against this package's own AuditFilter (not
// internal/store's) so internal/peer never imports internal/store;
// cmd/vnproxd adapts the concrete *store.AuditRepo to this shape.
type AuditReader interface {
	ListAuditPage(ctx context.Context, filter AuditFilter, cursor string, limit int) ([]AuditRecord, string, error)
}

// SnapshotReader is the peer-server-side dependency for GET
// /api/peer/snapshots (T-303): one node's own local snapshot list,
// cursor-paginated exactly like docs/api.md's GET /snapshots. Declared
// against this package's own SnapshotRecord (not internal/change's
// SnapshotSummary) for the same import-direction reason as AuditReader.
type SnapshotReader interface {
	ListSnapshotPage(ctx context.Context, cursor string, limit int) ([]SnapshotRecord, string, error)
}

// FlowReader is the peer-server-side dependency for GET /api/peer/flows
// (T-1002): one node's own local flow_samples ring, filtered and
// cursor-paginated exactly like docs/api.md's GET /flows. internal/api's
// cluster fan-out (fetchClusterFlows) re-queries every peer with the same
// filter+cursor and merges the returned pages with its own local page —
// see internal/api's flow cluster-merge code. Declared against this
// package's own FlowFilter/FlowRecord (not internal/store's or
// internal/flow's) for the same import-direction reason as AuditReader.
type FlowReader interface {
	ListFlowPage(ctx context.Context, filter FlowFilter, cursor string, limit int) ([]FlowRecord, string, error)
}

// HostWriter is the write-side dependency for the documented
// `/api/peer/host/{stage-interfaces,ifreload,restore}` routes: node-local
// /etc/network/interfaces(5) staging, reload, and direct restore. It is a
// small, T-301-owned interface (not change.NodeAgent) so this package never
// imports internal/change — the reverse dependency (internal/change /
// T-304's coordinator calling through a peer.Client to reach a remote
// node's NodeAgent-equivalent operations) is the intended direction.
// cmd/vnproxd's production wiring adapts the same host-writing agent
// internal/change's NodeAgent already uses to this shape.
type HostWriter interface {
	// StageInterfaces writes content to node's staged interfaces.new
	// without activating it.
	StageInterfaces(ctx context.Context, node, content string) error

	// ReloadInterfaces atomically applies node's already-staged
	// interfaces.new and reloads the network.
	ReloadInterfaces(ctx context.Context, node string) error

	// RestoreInterfaces writes content directly as node's committed
	// interfaces file and reloads — the rollback path (T-304), which does
	// not go through the normal stage/review flow since it is restoring a
	// known-good snapshot under time pressure.
	RestoreInterfaces(ctx context.Context, node, content string) error

	// DiscardStaged drops node's staged interfaces.new, if any, leaving the
	// committed file untouched — the peer-routed twin of change.NodeAgent's
	// same-named method (apply_seams.go), needed so a coordinator's
	// mid-apply rollback can clean up a remote node that was staged but
	// never reloaded (T-304; CLAUDE.md's "everything is cluster-aware" rule
	// applies to this NodeAgent operation same as the other three).
	DiscardStaged(ctx context.Context, node string) error
}

// LLDPInstaller is the T-302 guided-install dependency for
// `POST /api/peer/host/lldp/install` (docs/features/lldp-discovery.md §1:
// "one-click 'install lldpd on all nodes' runs through a changeset-like
// confirmation, executed via peer API apt install; audited"). Optional
// (nil-safe, ServerOptions.LLDPInstaller may be left unset): the route
// 503s rather than panicking when not wired, the same nil-safety pattern
// Reader/Writer already follow.
type LLDPInstaller interface {
	// InstallLLDPD installs and enables lldpd on this node.
	InstallLLDPD(ctx context.Context) error
	// StartService starts one of internal/host.WatchedServices on this
	// node (T-3604). Implementations MUST re-check the unit name
	// themselves — *host.Real.StartService does — rather than trusting
	// this server to have done it.
	StartService(ctx context.Context, unit string) error
}

// ReplicationSink is the peer-server-side dependency for
// POST /api/peer/ha/replicate (T-1704): it receives one HA replication batch
// as an opaque, already-HMAC-authenticated JSON payload and returns the ack
// payload. Declared against raw bytes (not internal/ha's Batch/Ack) so
// internal/peer never imports internal/ha or internal/store — the same
// import-direction discipline AuditReader/FlowReader follow; cmd/vnproxd wires
// an internal/ha adapter that marshals/unmarshals. Optional (nil-safe): a
// daemon with HA disabled 503s this route.
type ReplicationSink interface {
	Replicate(ctx context.Context, payload []byte) ([]byte, error)
}

// RouteReader backs T-3903's route-explorer peer routes
// (/api/peer/host/route/*): a node's kernel FIB (every table, v4/v6),
// policy rules, and — when the node runs FRR — its RIB. Declared as its
// own small interface (rather than folded into the large HostReader
// interface above, the way MDB/conntrack/ipv6-ra were) and wired through
// its own optional ServerOptions.Route field so this task adds no method
// to HostReader — no ripple to HostReader's other implementers (test
// doubles across internal/change, internal/collect, cmd/vnproxctl) that a
// wider interface change would need. *host.Real satisfies this directly
// once its six route-fetch methods exist (internal/host/route.go); so
// does *pvemock.FixtureHostReader for tests — see internal/route/
// service.go's Fetcher, the same six-method shape this mirrors on the
// wire side.
type RouteReader interface {
	RouteTableV4(ctx context.Context, node string) ([]byte, error)
	RouteTableV6(ctx context.Context, node string) ([]byte, error)
	RouteRulesV4(ctx context.Context, node string) ([]byte, error)
	RouteRulesV6(ctx context.Context, node string) ([]byte, error)
	// FRRRIBV4/V6 return an error wrapping host.ErrFRRUnavailable when
	// node runs no FRR at all — same convention as HostReader's own
	// FRRBGPSummary/FRREVPNVNI.
	FRRRIBV4(ctx context.Context, node string) ([]byte, error)
	FRRRIBV6(ctx context.Context, node string) ([]byte, error)
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Reader    HostReader
	Writer    HostWriter
	Audit     AuditReader
	Snapshots SnapshotReader
	// Route backs the /api/peer/host/route/* routes (T-3903's route
	// explorer). Optional (nil-safe, the same 503-not-panic treatment as
	// every other optional ServerOptions dependency): a daemon that
	// hasn't wired internal/route yet simply can't serve its routing
	// state to a peer.
	Route RouteReader
	// Flows backs GET /api/peer/flows (T-1002). Optional (nil-safe, the same
	// 503-not-panic treatment as every other optional ServerOptions
	// dependency): a daemon that hasn't wired flow ingestion at all simply
	// has nothing to serve peers here yet.
	Flows         FlowReader
	Timers        TimerAgent
	LLDPInstaller LLDPInstaller
	// FirewallLog backs GET /api/peer/firewall/log (T-505). Optional
	// (nil-safe, the same 503-not-panic treatment as every other optional
	// ServerOptions dependency): a daemon that hasn't wired a firewall log
	// source simply can't serve its own log to peers yet.
	FirewallLog FirewallLogReader
	// Capture backs the /api/peer/capture/* routes (T-1301). Optional
	// (nil-safe, the same 503-not-panic treatment as every other optional
	// dependency): a daemon with no capture agent wired can't capture on
	// behalf of a coordinating peer.
	Capture CaptureAgent
	// Replication backs POST /api/peer/ha/replicate (T-1704). Optional
	// (nil-safe, 503 when unset): a daemon with HA disabled has no standby to
	// receive replication for.
	Replication ReplicationSink
	// WriteGuard (T-2902) is the receiving-side safety validation for the
	// /host/{stage-interfaces,restore} write routes — see hostwrite.go.
	// Production wiring (cmd/vnproxd) always sets it; nil (tests, partial
	// wiring) skips validation with a per-write WARN rather than silently,
	// because absence here weakens a documented guarantee.
	WriteGuard HostWriteGuard
	// WriteAudit (T-2902) records a receiving-side audit row for every
	// /host/* write — allowed, refused, or failed. Nil-safe (no rows).
	WriteAudit   HostWriteAuditor
	Secrets      *SecretStore
	Logger       *slog.Logger
	Now          func() time.Time
	Version      string
	MaxBodyBytes int64
	// RequireNonce (T-3703), when true, makes authMiddleware refuse any
	// request that doesn't carry a validly nonce-bound signature
	// (HeaderNonce + HeaderNonceSignature) instead of falling back to
	// the legacy four-field HeaderSignature check. Defaults to false
	// (lenient) so an operator upgrading one node at a time doesn't lose
	// legacy peers the moment the first node is patched — see
	// authMiddleware's doc comment for the full compatibility story.
	// This is a single, dumb switch: nothing in this package flips it
	// automatically (no auto-detection of "every peer has upgraded"),
	// by design — that determination is an operator's to make and
	// record, once pve001 (or whichever peer) is known to send nonces
	// too.
	RequireNonce bool
}

// Server implements the /api/peer/* HTTP surface: the HMAC auth middleware
// (middleware.go) wrapping the documented host-read, host-write, health,
// and version handlers below.
type Server struct {
	replay *replayCache
	opts   ServerOptions
}

// NewServer builds a Server. opts.Secrets must be non-nil.
func NewServer(opts ServerOptions) *Server {
	if opts.Secrets == nil {
		panic("peer: NewServer requires a non-nil SecretStore")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	return &Server{opts: opts, replay: newReplayCache()}
}

// MountRoutes registers the full /api/peer/* subtree (docs/api.md's Peer
// API section) on r, with authMiddleware applied to the whole subtree via
// chi's Use — chi.Mux wraps its middleware stack around the entire
// ServeHTTP for anything routed under the mount, including a within-subtree
// 404/405, so no request ever reaches route-matching (let alone a handler)
// without first passing signature verification (T-301 AC2).
func (s *Server) MountRoutes(r chi.Router) {
	r.Route("/api/peer", func(r chi.Router) {
		r.Use(s.authMiddleware)

		r.Get("/health", s.handleHealth)
		r.Get("/version", s.handleVersion)

		r.Get("/host/interfaces", s.handleInterfaces)
		r.Get("/host/lldp", s.handleLLDP)
		r.Get("/host/stats", s.handleStats)
		r.Get("/host/services", s.handleServices)
		r.Get("/host/links", s.handleLinks)
		r.Get("/host/fdb", s.handleFDB)
		r.Get("/host/neighbors", s.handleNeighbors)
		r.Get("/host/container-interior", s.handleContainerInterior)
		r.Get("/host/container-ping", s.handleContainerPing)
		r.Get("/host/conntrack", s.handleConntrack)
		r.Get("/host/ipv6-ra", s.handleIPv6RA)
		r.Get("/host/frr/bgp-summary", s.handleFRRBGPSummary)
		r.Get("/host/frr/evpn-vni", s.handleFRREVPNVNI)
		r.Get("/host/dhcp-leases", s.handleDHCPLeases)
		r.Get("/host/mdb", s.handleMDB)
		r.Get("/host/route/fib-v4", s.handleRouteTableV4)
		r.Get("/host/route/fib-v6", s.handleRouteTableV6)
		r.Get("/host/route/rules-v4", s.handleRouteRulesV4)
		r.Get("/host/route/rules-v6", s.handleRouteRulesV6)
		r.Get("/host/route/frr-rib-v4", s.handleFRRRIBV4)
		r.Get("/host/route/frr-rib-v6", s.handleFRRRIBV6)
		r.Post("/host/stage-interfaces", s.handleStageInterfaces)
		r.Post("/host/ifreload", s.handleIfreload)
		r.Post("/host/restore", s.handleRestore)
		r.Post("/host/discard-staged", s.handleDiscardStaged)
		r.Post("/host/lldp/install", s.handleInstallLLDPD)
		r.Post("/host/service/start", s.handleStartService)

		r.Get("/audit", s.handleAudit)
		r.Get("/snapshots", s.handleSnapshots)
		r.Get("/flows", s.handleFlows)
		r.Get("/firewall/log", s.handleFirewallLog)

		r.Post("/timer/arm", s.handleTimerArm)
		r.Post("/timer/cancel", s.handleTimerCancel)
		r.Get("/timer/status", s.handleTimerStatus)

		r.Post("/capture/start", s.handleCaptureStart)
		r.Post("/capture/stop", s.handleCaptureStop)
		r.Get("/capture/status", s.handleCaptureStatus)
		r.Get("/capture/download", s.handleCaptureDownload)

		r.Post("/ha/replicate", s.handleReplicate)
	})
}

// handleReplicate implements POST /api/peer/ha/replicate (T-1704): the active
// daemon pushes one HA replication batch (opaque JSON) to the standby. The
// body is already HMAC-authenticated and body-size-capped by authMiddleware;
// this handler hands it verbatim to the sink and returns the ack payload.
func (s *Server) handleReplicate(w http.ResponseWriter, r *http.Request) {
	if s.opts.Replication == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "HA replication not configured")
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "reading replication payload: "+err.Error())
		return
	}
	ack, err := s.opts.Replication.Replicate(r.Context(), payload)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "replication_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ack)
}

// handleCaptureStart implements POST /api/peer/capture/start (T-1301): a
// coordinating daemon asks this node to run one node-local capture. The
// receiving coordinator (opts.Capture) re-validates the filter and re-clamps
// the caps against this node's own config before running — the caller's
// arithmetic is never trusted.
func (s *Server) handleCaptureStart(w http.ResponseWriter, r *http.Request) {
	if s.opts.Capture == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "capture agent not configured")
		return
	}
	var spec CaptureSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil || spec.SessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "sessionId and a capture spec are required")
		return
	}
	res, err := s.opts.Capture.StartLocal(r.Context(), spec)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "capture_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCaptureStop implements POST /api/peer/capture/stop (T-1301).
func (s *Server) handleCaptureStop(w http.ResponseWriter, r *http.Request) {
	if s.opts.Capture == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "capture agent not configured")
		return
	}
	var req captureStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "sessionId is required")
		return
	}
	res, err := s.opts.Capture.StopLocal(r.Context(), req.SessionID)
	if err != nil {
		s.writeCaptureError(w, "stopping capture", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCaptureStatus implements GET /api/peer/capture/status?sessionId=
// (T-1301).
func (s *Server) handleCaptureStatus(w http.ResponseWriter, r *http.Request) {
	if s.opts.Capture == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "capture agent not configured")
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "sessionId is required")
		return
	}
	res, err := s.opts.Capture.StatusLocal(r.Context(), sessionID)
	if err != nil {
		s.writeCaptureError(w, "reading capture status", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCaptureDownload implements GET /api/peer/capture/download?sessionId=
// (T-1302): a coordinating daemon fetching one session's raw pcap bytes from
// the node that actually captured it. Payload bytes cross the wire exactly
// once here, HMAC-authenticated like every other peer route — never logged,
// never persisted by this handler.
func (s *Server) handleCaptureDownload(w http.ResponseWriter, r *http.Request) {
	if s.opts.Capture == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "capture agent not configured")
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "sessionId is required")
		return
	}
	data, err := s.opts.Capture.DownloadLocal(r.Context(), sessionID)
	if err != nil {
		s.writeCaptureError(w, "downloading capture", err)
		return
	}
	writeJSON(w, http.StatusOK, captureDownloadResponse{Content: data})
}

func (s *Server) writeCaptureError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, host.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", op+": "+err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", op+": "+err.Error())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.opts.Version})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, VersionInfo{Version: s.opts.Version, ProtocolVersion: ProtocolVersion})
}

func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	pending := r.URL.Query().Get("pending") == "true"
	content, err := s.opts.Reader.InterfacesFile(r.Context(), node, pending)
	if err != nil {
		s.writeHostError(w, "reading interfaces file", err)
		return
	}
	writeJSON(w, http.StatusOK, interfacesResponse{Content: content})
}

func (s *Server) handleLLDP(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := s.opts.Reader.LLDP(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading LLDP neighbors", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	stats, err := s.opts.Reader.Stats(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading interface stats", err)
		return
	}
	writeJSON(w, http.StatusOK, statsResponse{Stats: stats})
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	services, err := s.opts.Reader.Services(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading service status", err)
		return
	}
	writeJSON(w, http.StatusOK, servicesResponse{Services: services})
}

func (s *Server) handleLinks(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	links, err := s.opts.Reader.Links(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading netlink link state", err)
		return
	}
	writeJSON(w, http.StatusOK, linksResponse{Links: links})
}

// handleFDB implements GET /api/peer/host/fdb (T-306): node's bridge
// forwarding-database tables, flattened out of Links() (see the HostReader
// doc comment above for why this isn't its own Reader method).
func (s *Server) handleFDB(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	links, err := s.opts.Reader.Links(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading netlink link state for fdb", err)
		return
	}
	writeJSON(w, http.StatusOK, fdbResponse{Entries: host.FlattenFDB(links)})
}

// handleNeighbors implements GET /api/peer/host/neighbors (T-805): node's
// resolved ARP/IPv6-neighbor table, the peer-routed counterpart of a local
// host.Reader.Neighbors call — internal/ipam.NeighborSource's fan-out
// dependency, following handleLinks/handleFDB's precedent exactly (a plain
// read, no {available} envelope needed).
func (s *Server) handleNeighbors(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	neighbors, err := s.opts.Reader.Neighbors(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading neighbor table", err)
		return
	}
	writeJSON(w, http.StatusOK, neighborsResponse{Neighbors: neighbors})
}

// handleContainerInterior implements GET /api/peer/host/container-interior
// (T-1304): an lxc guest's raw host-side network-namespace read set, the
// peer-routed counterpart of a local host.Reader.ContainerInterior call —
// following handleNeighbors' precedent exactly (a plain read, no
// {available} envelope). ?vmid= must parse as a positive int; a malformed
// value is 400, not silently coerced to 0.
func (s *Server) handleContainerInterior(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	vmid, err := strconv.Atoi(r.URL.Query().Get("vmid"))
	if err != nil || vmid <= 0 {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "vmid must be a positive integer")
		return
	}
	raw, err := s.opts.Reader.ContainerInterior(r.Context(), node, vmid)
	if err != nil {
		s.writeHostError(w, "reading container interior", err)
		return
	}
	writeJSON(w, http.StatusOK, containerInteriorResponse{
		AddrJSON: string(raw.AddrJSON), RouteJSON: string(raw.RouteJSON),
		ResolvConf: string(raw.ResolvConf), Sockets: string(raw.Sockets),
	})
}

// handleContainerPing implements GET /api/peer/host/container-ping
// (T-1304): the peer-routed counterpart of a local
// host.Reader.ContainerPing call. ?ip= is required (the target address to
// ping from inside the container's netns).
func (s *Server) handleContainerPing(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	targetIP := r.URL.Query().Get("ip")
	vmid, err := strconv.Atoi(r.URL.Query().Get("vmid"))
	if err != nil || vmid <= 0 || targetIP == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "vmid must be a positive integer and ip must be set")
		return
	}
	reachable, err := s.opts.Reader.ContainerPing(r.Context(), node, vmid, targetIP)
	if err != nil {
		s.writeHostError(w, "pinging from container", err)
		return
	}
	writeJSON(w, http.StatusOK, containerPingResponse{Reachable: reachable})
}

// handleConntrack implements GET /api/peer/host/conntrack (T-1305): node's
// live conntrack/NAT table, the peer-routed counterpart of a local
// host.Reader.Conntrack call — following handleNeighbors' precedent
// exactly (a plain read, no {available} envelope needed: an empty table is
// itself a clean, unremarkable answer).
func (s *Server) handleConntrack(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	entries, err := s.opts.Reader.Conntrack(r.Context(), node)
	if err != nil {
		if errors.Is(err, host.ErrConntrackUnavailable) {
			// Distinguishable from an ordinary read failure (T-3711): the
			// client-side counterpart, mapConntrackUnavailable, rewraps
			// this code back into errors.Is(err, host.ErrConntrackUnavailable)
			// so GET /conntrack's cluster fan-out can put this node in
			// unavailableNodes rather than failedNodes, the same
			// distinction handleFRRBGPSummary's {available:false} makes
			// for FRR — carried here via the error-envelope code instead,
			// since conntrackResponse's success shape is deliberately not
			// an {available} envelope (see its doc comment).
			writeJSONError(w, http.StatusServiceUnavailable, errCodeConntrackUnavailable, "reading conntrack table: "+err.Error())
			return
		}
		s.writeHostError(w, "reading conntrack table", err)
		return
	}
	if entries == nil {
		entries = []host.ConntrackEntry{}
	}
	writeJSON(w, http.StatusOK, conntrackResponse{Entries: entries})
}

// handleIPv6RA implements GET /api/peer/host/ipv6-ra (T-1404): node's
// bounded, host-local IPv6 RA/DHCPv6 observation, the peer-routed
// counterpart of a local host.Reader.IPv6RA call — following
// handleConntrack's precedent exactly (a plain read, no {available}
// envelope needed).
func (s *Server) handleIPv6RA(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	items, err := s.opts.Reader.IPv6RA(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading ipv6 RA observations", err)
		return
	}
	if items == nil {
		items = []host.IPv6RAObservation{}
	}
	writeJSON(w, http.StatusOK, ipv6RAResponse{Items: items})
}

// handleFRRBGPSummary implements GET /api/peer/host/frr/bgp-summary
// (T-404): node's raw `vtysh -c "show bgp summary json"` output, wrapped
// in {available, content} rather than passed through raw (unlike
// handleLLDP) so a node with no FRR installed at all can report that
// cleanly ({"available":false}) instead of as an error status —
// docs/features/sdn.md §3's "absent FRR on a node reports no EVPN
// cleanly" requirement, at the transport layer.
func (s *Server) handleFRRBGPSummary(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := s.opts.Reader.FRRBGPSummary(r.Context(), node)
	if err != nil {
		if errors.Is(err, host.ErrFRRUnavailable) {
			writeJSON(w, http.StatusOK, frrResponse{Available: false})
			return
		}
		s.writeHostError(w, "reading bgp summary", err)
		return
	}
	writeJSON(w, http.StatusOK, frrResponse{Available: true, Content: json.RawMessage(data)})
}

// handleFRREVPNVNI implements GET /api/peer/host/frr/evpn-vni (T-404):
// node's raw `vtysh -c "show evpn vni json"` output, same
// {available, content} convention as handleFRRBGPSummary.
func (s *Server) handleFRREVPNVNI(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := s.opts.Reader.FRREVPNVNI(r.Context(), node)
	if err != nil {
		if errors.Is(err, host.ErrFRRUnavailable) {
			writeJSON(w, http.StatusOK, frrResponse{Available: false})
			return
		}
		s.writeHostError(w, "reading evpn vni table", err)
		return
	}
	writeJSON(w, http.StatusOK, frrResponse{Available: true, Content: json.RawMessage(data)})
}

// handleDHCPLeases implements GET /api/peer/host/dhcp-leases (T-406):
// node's raw dnsmasq DHCP lease-file content, verbatim.
func (s *Server) handleDHCPLeases(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := s.opts.Reader.DHCPLeases(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading dhcp leases", err)
		return
	}
	writeJSON(w, http.StatusOK, dhcpLeasesResponse{Content: string(data)})
}

// handleMDB implements GET /api/peer/host/mdb (T-3902): node's raw
// `bridge -d -j mdb show` output, the peer-routed counterpart of a local
// host.Reader.MDB call. Same {content: string}, no-{available}-envelope
// convention as handleDHCPLeases — an empty-but-successfully-read MDB table
// is a clean, unremarkable answer (see host/mdb.go's ParseMDB doc comment),
// not a distinct absent/error condition. Unlike DHCPLeases, a genuine read
// failure (including host.ErrMDBUnavailable — the `bridge` binary itself
// missing, an uncommon anomaly rather than an expected state the way "no
// FRR here" is) is a real error here, not silently downgraded to an empty
// result: writeHostError below reports it as such.
func (s *Server) handleMDB(w http.ResponseWriter, r *http.Request) {
	if s.opts.Reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := s.opts.Reader.MDB(r.Context(), node)
	if err != nil {
		s.writeHostError(w, "reading bridge mdb", err)
		return
	}
	writeJSON(w, http.StatusOK, mdbResponse{Content: string(data)})
}

// handleRouteTableV4/V6 implement GET /api/peer/host/route/fib-v4 and
// fib-v6 (T-3903's route explorer): node's raw `ip -j route show table
// all` / `ip -j -6 route show table all` output, the remote-node
// counterpart of a local internal/route.Fetcher.RouteTableV4/V6 call.
// `ip` is a hard OS dependency on every PVE node, so — unlike the FRR
// routes below — there is no {available} envelope to consider: a read
// failure here is always a genuine error.
func (s *Server) handleRouteTableV4(w http.ResponseWriter, r *http.Request) {
	s.handleRouteContent(w, r, "reading ipv4 route table", func(ctx context.Context, node string) ([]byte, error) {
		return s.opts.Route.RouteTableV4(ctx, node)
	})
}

func (s *Server) handleRouteTableV6(w http.ResponseWriter, r *http.Request) {
	s.handleRouteContent(w, r, "reading ipv6 route table", func(ctx context.Context, node string) ([]byte, error) {
		return s.opts.Route.RouteTableV6(ctx, node)
	})
}

// handleRouteRulesV4/V6 implement GET /api/peer/host/route/rules-v4 and
// rules-v6: node's raw `ip -j rule show` / `ip -j -6 rule show` output.
func (s *Server) handleRouteRulesV4(w http.ResponseWriter, r *http.Request) {
	s.handleRouteContent(w, r, "reading ipv4 policy rules", func(ctx context.Context, node string) ([]byte, error) {
		return s.opts.Route.RouteRulesV4(ctx, node)
	})
}

func (s *Server) handleRouteRulesV6(w http.ResponseWriter, r *http.Request) {
	s.handleRouteContent(w, r, "reading ipv6 policy rules", func(ctx context.Context, node string) ([]byte, error) {
		return s.opts.Route.RouteRulesV6(ctx, node)
	})
}

// handleRouteContent is the shared body for the four plain
// (no-{available}-envelope) route-explorer reads above: 503 when Route
// isn't wired, writeHostError's usual not-found/internal-error split on a
// genuine read failure, routeContentResponse otherwise.
func (s *Server) handleRouteContent(w http.ResponseWriter, r *http.Request, op string, fetch func(context.Context, string) ([]byte, error)) {
	if s.opts.Route == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "route reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := fetch(r.Context(), node)
	if err != nil {
		s.writeHostError(w, op, err)
		return
	}
	writeJSON(w, http.StatusOK, routeContentResponse{Content: json.RawMessage(data)})
}

// handleFRRRIBV4/V6 implement GET /api/peer/host/route/frr-rib-v4 and
// frr-rib-v6 (T-3903): node's raw `vtysh -c "show ip route json"` /
// "show ipv6 route json" output, wrapped in the same {available, content}
// envelope as handleFRRBGPSummary/handleFRREVPNVNI (Available is false
// when node runs no FRR at all — host.ErrFRRUnavailable — rather than an
// error status).
func (s *Server) handleFRRRIBV4(w http.ResponseWriter, r *http.Request) {
	s.handleFRRRIBContent(w, r, "reading frr ipv4 rib", func(ctx context.Context, node string) ([]byte, error) {
		return s.opts.Route.FRRRIBV4(ctx, node)
	})
}

func (s *Server) handleFRRRIBV6(w http.ResponseWriter, r *http.Request) {
	s.handleFRRRIBContent(w, r, "reading frr ipv6 rib", func(ctx context.Context, node string) ([]byte, error) {
		return s.opts.Route.FRRRIBV6(ctx, node)
	})
}

func (s *Server) handleFRRRIBContent(w http.ResponseWriter, r *http.Request, op string, fetch func(context.Context, string) ([]byte, error)) {
	if s.opts.Route == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "route reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	data, err := fetch(r.Context(), node)
	if err != nil {
		if errors.Is(err, host.ErrFRRUnavailable) {
			writeJSON(w, http.StatusOK, frrResponse{Available: false})
			return
		}
		s.writeHostError(w, op, err)
		return
	}
	writeJSON(w, http.StatusOK, frrResponse{Available: true, Content: json.RawMessage(data)})
}

// handleFirewallLog implements GET /api/peer/firewall/log (T-505): node's
// own pve-firewall log, tailed from the beginning (no ?cursor=) or
// followed from a previous nextCursor. maxLines defaults to
// defaultPeerPageLimit and clamps to maxPeerPageLimit, the same convention
// parsePeerPageLimit already establishes for audit/snapshots (named
// ?maxLines= here rather than ?limit= since "how many lines" reads more
// clearly than "how many items" for a log tail, but the semantics and
// bounds are identical).
func (s *Server) handleFirewallLog(w http.ResponseWriter, r *http.Request) {
	if s.opts.FirewallLog == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "firewall log reader not configured")
		return
	}
	node := r.URL.Query().Get("node")
	maxLines, ok := parsePeerLineLimit(w, r)
	if !ok {
		return
	}
	lines, next, err := s.opts.FirewallLog.FirewallLogTail(r.Context(), node, r.URL.Query().Get("cursor"), maxLines)
	if err != nil {
		s.writeHostError(w, "reading firewall log", err)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, firewallLogResponse{Lines: lines, NextCursor: next})
}

// parsePeerLineLimit is parsePeerPageLimit's ?maxLines= counterpart (GET
// /api/peer/firewall/log's own query param name — see handleFirewallLog's
// doc comment for why it's spelled differently from ?limit=).
func parsePeerLineLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := defaultPeerLogLines
	if v := r.URL.Query().Get("maxLines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "maxLines must be a positive integer")
			return 0, false
		}
		if n > maxPeerLogLines {
			n = maxPeerLogLines
		}
		limit = n
	}
	return limit, true
}

// parsePeerPageLimit parses the shared ?limit= convention GET /audit and
// GET /snapshots use, defaulting/clamping exactly like internal/api's own
// list handlers. Returns ok=false (after writing the 400 itself) on a
// non-positive-integer value.
func parsePeerPageLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := defaultPeerPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
			return 0, false
		}
		if n > maxPeerPageLimit {
			n = maxPeerPageLimit
		}
		limit = n
	}
	return limit, true
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if s.opts.Audit == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "audit reader not configured")
		return
	}
	limit, ok := parsePeerPageLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := AuditFilter{
		User: q.Get("user"), Action: q.Get("action"), Target: q.Get("target"),
		Result: q.Get("result"), ChangesetID: q.Get("changesetId"),
	}
	if v := q.Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "from must be a unix-seconds integer")
			return
		}
		filter.From = n
	}
	if v := q.Get("to"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "to must be a unix-seconds integer")
			return
		}
		filter.To = n
	}

	items, next, err := s.opts.Audit.ListAuditPage(r.Context(), filter, q.Get("cursor"), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "listing audit page: "+err.Error())
		return
	}
	if items == nil {
		items = []AuditRecord{}
	}
	writeJSON(w, http.StatusOK, auditPageResponse{Items: items, NextCursor: next})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.opts.Snapshots == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "snapshot reader not configured")
		return
	}
	limit, ok := parsePeerPageLimit(w, r)
	if !ok {
		return
	}
	items, next, err := s.opts.Snapshots.ListSnapshotPage(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "listing snapshot page: "+err.Error())
		return
	}
	if items == nil {
		items = []SnapshotRecord{}
	}
	writeJSON(w, http.StatusOK, snapshotPageResponse{Items: items, NextCursor: next})
}

// handleFlows implements GET /api/peer/flows (T-1002), the same filter
// query params as docs/api.md's GET /flows (?guest=&vlan=&subnet=&port=
// &protocol=&fromTs=&toTs=&limit=&cursor=, protocol already resolved to a
// numeric proto by the caller — see internal/api/flows.go).
func (s *Server) handleFlows(w http.ResponseWriter, r *http.Request) {
	if s.opts.Flows == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "flow reader not configured")
		return
	}
	limit, ok := parsePeerPageLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := FlowFilter{Guest: q.Get("guest"), Subnet: q.Get("subnet"), Source: q.Get("source")}
	if v := q.Get("vlan"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "vlan must be an integer")
			return
		}
		filter.VLAN = n
	}
	if v := q.Get("port"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "port must be an integer")
			return
		}
		filter.Port = n
	}
	if v := q.Get("proto"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "proto must be an integer")
			return
		}
		filter.Proto = n
	}
	if v := q.Get("fromTs"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "fromTs must be a unix-seconds integer")
			return
		}
		filter.FromTs = n
	}
	if v := q.Get("toTs"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "toTs must be a unix-seconds integer")
			return
		}
		filter.ToTs = n
	}

	items, next, err := s.opts.Flows.ListFlowPage(r.Context(), filter, q.Get("cursor"), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "listing flow page: "+err.Error())
		return
	}
	if items == nil {
		items = []FlowRecord{}
	}
	writeJSON(w, http.StatusOK, flowPageResponse{Items: items, NextCursor: next})
}

func (s *Server) handleStageInterfaces(w http.ResponseWriter, r *http.Request) {
	if s.opts.Writer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host writer not configured")
		return
	}
	var req stageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "node and content are required")
		return
	}
	audit := HostWriteAudit{
		Action: "peer.host.stage", Node: req.Node,
		Actor: req.Actor, OriginNode: req.OriginNode, OriginIP: req.OriginIP,
		ContentSHA256: contentSHA256(req.Content),
	}
	// T-2902: the receiving node runs its own change-engine safety
	// validation before anything touches the host writer — a peer call is
	// not a privileged shortcut past the interlocks the coordinator
	// enforces. Refusals are audited and carry the findings on the wire.
	if s.opts.WriteGuard == nil {
		s.opts.Logger.Warn("peer: host stage-interfaces without a WriteGuard — receiving-side safety validation skipped (T-2902 production wiring always sets one)", "node", req.Node)
	} else if findings := s.opts.WriteGuard.ValidateStagedContent(r.Context(), req.Node, req.Content); len(findings) > 0 {
		audit.Result, audit.Detail = "refused", strings.Join(findings, "; ")
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusBadRequest, "safety_refused", "receiving-side safety validation refused this write: "+strings.Join(findings, "; "))
		return
	}
	if err := s.opts.Writer.StageInterfaces(r.Context(), req.Node, req.Content); err != nil {
		audit.Result, audit.Detail = "failed", err.Error()
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
	audit.Result = "allowed"
	s.auditHostWrite(r.Context(), audit)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

func (s *Server) handleIfreload(w http.ResponseWriter, r *http.Request) {
	if s.opts.Writer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host writer not configured")
		return
	}
	var req nodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "node is required")
		return
	}
	// T-2902: no content to validate (the staged file was validated when it
	// was staged); the reload itself is still a host mutation and is
	// audited on the receiving side like every other one.
	audit := HostWriteAudit{
		Action: "peer.host.ifreload", Node: req.Node,
		Actor: req.Actor, OriginNode: req.OriginNode, OriginIP: req.OriginIP,
	}
	if err := s.opts.Writer.ReloadInterfaces(r.Context(), req.Node); err != nil {
		audit.Result, audit.Detail = "failed", err.Error()
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
	audit.Result = "allowed"
	s.auditHostWrite(r.Context(), audit)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if s.opts.Writer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host writer not configured")
		return
	}
	var req stageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "node and content are required")
		return
	}
	audit := HostWriteAudit{
		Action: "peer.host.restore", Node: req.Node,
		Actor: req.Actor, OriginNode: req.OriginNode, OriginIP: req.OriginIP,
		ContentSHA256: contentSHA256(req.Content),
	}
	// T-2902: a restore whose content matches a snapshot this node itself
	// recorded is a distributed rollback to a known-good state — exempt by
	// provenance (a rollback legitimately re-arms the management path a
	// fresh write may not touch), never by skipping validation wholesale.
	// Content this node has no record of is just a write, validated like
	// one.
	switch {
	case s.opts.WriteGuard == nil:
		s.opts.Logger.Warn("peer: host restore without a WriteGuard — receiving-side safety validation skipped (T-2902 production wiring always sets one)", "node", req.Node)
	case s.opts.WriteGuard.KnownContent(r.Context(), req.Node, req.Content):
		audit.Provenance = "snapshot"
	default:
		if findings := s.opts.WriteGuard.ValidateStagedContent(r.Context(), req.Node, req.Content); len(findings) > 0 {
			audit.Result, audit.Detail = "refused", strings.Join(findings, "; ")
			s.auditHostWrite(r.Context(), audit)
			writeJSONError(w, http.StatusBadRequest, "safety_refused", "receiving-side safety validation refused this restore (content matches no snapshot on this node): "+strings.Join(findings, "; "))
			return
		}
	}
	if err := s.opts.Writer.RestoreInterfaces(r.Context(), req.Node, req.Content); err != nil {
		audit.Result, audit.Detail = "failed", err.Error()
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
	audit.Result = "allowed"
	s.auditHostWrite(r.Context(), audit)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

func (s *Server) handleDiscardStaged(w http.ResponseWriter, r *http.Request) {
	if s.opts.Writer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host writer not configured")
		return
	}
	var req nodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "node is required")
		return
	}
	// T-2902: discarding a staged file destroys no committed state, but it
	// is still a host mutation with an actor behind it — audited.
	audit := HostWriteAudit{
		Action: "peer.host.discard-staged", Node: req.Node,
		Actor: req.Actor, OriginNode: req.OriginNode, OriginIP: req.OriginIP,
	}
	if err := s.opts.Writer.DiscardStaged(r.Context(), req.Node); err != nil {
		audit.Result, audit.Detail = "failed", err.Error()
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
	audit.Result = "allowed"
	s.auditHostWrite(r.Context(), audit)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

// handleInstallLLDPD implements POST /api/peer/host/lldp/install: the
// guided-install flow's node-local step. Requires an explicit
// {"confirm":true} body field — this is the "changeset-like confirmation"
// the spec calls for at the transport layer; the caller (a coordinating
// daemon acting on an operator's explicit request) is responsible for
// having obtained that confirmation and for audit-logging the action, same
// division of responsibility as the stage/ifreload/restore handlers above
// (this package never itself talks to internal/store).
func (s *Server) handleInstallLLDPD(w http.ResponseWriter, r *http.Request) {
	if s.opts.LLDPInstaller == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "lldp installer not configured")
		return
	}
	var req installLLDPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if !req.Confirm {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "confirm must be true")
		return
	}
	// T-2902: package installation is a host mutation; the receiving node
	// records it (the doc comment above previously delegated all audit to
	// the coordinator — the coordinator's row still exists, this one says
	// what happened *here*).
	audit := HostWriteAudit{
		Action: "peer.host.lldp-install",
		Actor:  req.Actor, OriginNode: req.OriginNode, OriginIP: req.OriginIP,
	}
	if err := s.opts.LLDPInstaller.InstallLLDPD(r.Context()); err != nil {
		audit.Result, audit.Detail = "failed", err.Error()
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
	audit.Result = "allowed"
	s.auditHostWrite(r.Context(), audit)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

// handleStartService implements POST /api/peer/host/service/start (T-3604).
//
// Starting a systemd unit is genuinely new power for this product, so the
// checks are deliberately doubled up rather than delegated:
//
//   - The unit name is validated HERE, on the node that will run the
//     command, against internal/host.IsWatchedService. The coordinator
//     validates too, but a receiving node that trusts its caller's
//     validation has no allow-list at all — it has a convention. This is
//     the check that actually holds if a coordinator is compromised, buggy,
//     or simply a different (older or newer) version.
//   - *host.Real.StartService checks a THIRD time, in the function that
//     builds the argv. That is not redundancy for its own sake: it is the
//     only check that is still in scope if some future caller reaches the
//     host layer by another path.
//
// A refused unit is a 400 and is audited as a refusal — an attempt to start
// something outside the allow-list is exactly the event an audit log exists
// to record, and dropping it silently would make the interesting case the
// invisible one.
func (s *Server) handleStartService(w http.ResponseWriter, r *http.Request) {
	if s.opts.LLDPInstaller == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "host service manager not configured")
		return
	}
	var req startServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if !req.Confirm {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "confirm must be true")
		return
	}
	audit := HostWriteAudit{
		Action: "peer.host.service-start",
		Actor:  req.Actor, OriginNode: req.OriginNode, OriginIP: req.OriginIP,
		Detail: req.Unit,
	}
	if !host.IsWatchedService(req.Unit) {
		audit.Result = "refused"
		audit.Detail = "unit not allow-listed: " + req.Unit
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusBadRequest, "validation_failed",
			"unit is not one of vnprox's watched services")
		return
	}
	if err := s.opts.LLDPInstaller.StartService(r.Context(), req.Unit); err != nil {
		audit.Result, audit.Detail = "failed", err.Error()
		s.auditHostWrite(r.Context(), audit)
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
	audit.Result = "allowed"
	s.auditHostWrite(r.Context(), audit)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

func (s *Server) handleTimerArm(w http.ResponseWriter, r *http.Request) {
	if s.opts.Timers == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "timer agent not configured")
		return
	}
	var req armTimerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChangesetID == "" || req.Node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "changesetId and node are required")
		return
	}
	rec, err := s.opts.Timers.ArmTimer(r.Context(), req.ChangesetID, req.Node, req.Content, req.Deadline)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "timer_arm_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, timerResponse{Record: rec})
}

func (s *Server) handleTimerCancel(w http.ResponseWriter, r *http.Request) {
	if s.opts.Timers == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "timer agent not configured")
		return
	}
	var req timerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChangesetID == "" || req.Node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "changesetId and node are required")
		return
	}
	rec, err := s.opts.Timers.CancelTimer(r.Context(), req.ChangesetID, req.Node)
	if err != nil {
		s.writeTimerError(w, "cancelling timer", err)
		return
	}
	writeJSON(w, http.StatusOK, timerResponse{Record: rec})
}

func (s *Server) handleTimerStatus(w http.ResponseWriter, r *http.Request) {
	if s.opts.Timers == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "peer_unavailable", "timer agent not configured")
		return
	}
	changesetID := r.URL.Query().Get("changesetId")
	node := r.URL.Query().Get("node")
	if changesetID == "" || node == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "changesetId and node are required")
		return
	}
	rec, err := s.opts.Timers.TimerStatus(r.Context(), changesetID, node)
	if err != nil {
		s.writeTimerError(w, "reading timer status", err)
		return
	}
	writeJSON(w, http.StatusOK, timerResponse{Record: rec})
}

func (s *Server) writeTimerError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrTimerNotFound) {
		writeJSONError(w, http.StatusNotFound, errCodeTimerNotFound, op+": "+err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", op+": "+err.Error())
}

func (s *Server) writeHostError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, host.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", op+": "+err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", op+": "+err.Error())
}
