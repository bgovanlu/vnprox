package peer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/host"
)

// defaultMaxBodyBytes bounds request bodies the peer server will read
// (docs/data-model.md's largest per-node artifact is the interfaces(5)
// file, which is at most a few hundred KB even on a heavily-configured
// node) — generous headroom against an abusive/buggy caller, not a
// realistic limit.
const defaultMaxBodyBytes = 4 << 20 // 4 MiB

// HostReader is the read-side dependency for the documented
// `/api/peer/host/{interfaces,lldp,stats}` routes (docs/api.md's Peer API
// section — `Links` is deliberately not part of that contract, so this
// interface only needs the three methods those routes use). host.Reader
// satisfies this directly: Go allows assigning a wider interface value to
// a narrower interface-typed field/parameter whenever its method set is a
// superset, so host.NewReal() (or a host.FixtureReader) can be passed to
// ServerOptions.Reader with no adapter.
type HostReader interface {
	// InterfacesFile returns node's literal /etc/network/interfaces (or
	// interfaces.new when includePending) content.
	InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)

	// LLDP returns node's raw LLDP neighbor JSON.
	LLDP(ctx context.Context, node string) ([]byte, error)

	// Stats returns node's interface counters, keyed by interface name.
	Stats(ctx context.Context, node string) (map[string]host.IfaceStats, error)
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
	LLDPInstaller LLDPInstaller
	Secrets       *SecretStore
	Logger        *slog.Logger
	Now           func() time.Time
	Version       string
	MaxBodyBytes  int64
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
		r.Post("/host/stage-interfaces", s.handleStageInterfaces)
		r.Post("/host/ifreload", s.handleIfreload)
		r.Post("/host/restore", s.handleRestore)
		r.Post("/host/lldp/install", s.handleInstallLLDPD)
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

func (s *Server) writeHostError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, host.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", op+": "+err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", op+": "+err.Error())
}
