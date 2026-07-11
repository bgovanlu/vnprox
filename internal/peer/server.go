package peer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
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
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Reader        HostReader
	Writer        HostWriter
	Audit         AuditReader
	Snapshots     SnapshotReader
	Timers        TimerAgent
	LLDPInstaller LLDPInstaller
	// FirewallLog backs GET /api/peer/firewall/log (T-505). Optional
	// (nil-safe, the same 503-not-panic treatment as every other optional
	// ServerOptions dependency): a daemon that hasn't wired a firewall log
	// source simply can't serve its own log to peers yet.
	FirewallLog  FirewallLogReader
	Secrets      *SecretStore
	Logger       *slog.Logger
	Now          func() time.Time
	Version      string
	MaxBodyBytes int64
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
		r.Get("/host/links", s.handleLinks)
		r.Get("/host/fdb", s.handleFDB)
		r.Post("/host/stage-interfaces", s.handleStageInterfaces)
		r.Post("/host/ifreload", s.handleIfreload)
		r.Post("/host/restore", s.handleRestore)
		r.Post("/host/discard-staged", s.handleDiscardStaged)
		r.Post("/host/lldp/install", s.handleInstallLLDPD)

		r.Get("/audit", s.handleAudit)
		r.Get("/snapshots", s.handleSnapshots)
		r.Get("/firewall/log", s.handleFirewallLog)

		r.Post("/timer/arm", s.handleTimerArm)
		r.Post("/timer/cancel", s.handleTimerCancel)
		r.Get("/timer/status", s.handleTimerStatus)
	})
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
	if err := s.opts.Writer.StageInterfaces(r.Context(), req.Node, req.Content); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
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
	if err := s.opts.Writer.ReloadInterfaces(r.Context(), req.Node); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
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
	if err := s.opts.Writer.RestoreInterfaces(r.Context(), req.Node, req.Content); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
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
	if err := s.opts.Writer.DiscardStaged(r.Context(), req.Node); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
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
	if err := s.opts.LLDPInstaller.InstallLLDPD(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "host_write_failed", err.Error())
		return
	}
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
