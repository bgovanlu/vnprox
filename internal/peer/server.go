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

	// DiscardStaged drops node's staged interfaces.new, if any, leaving the
	// committed file untouched — the peer-routed twin of change.NodeAgent's
	// same-named method (apply_seams.go), needed so a coordinator's
	// mid-apply rollback can clean up a remote node that was staged but
	// never reloaded (T-304; CLAUDE.md's "everything is cluster-aware" rule
	// applies to this NodeAgent operation same as the other three).
	DiscardStaged(ctx context.Context, node string) error
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Reader       HostReader
	Writer       HostWriter
	Timers       TimerAgent
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
		r.Post("/host/stage-interfaces", s.handleStageInterfaces)
		r.Post("/host/ifreload", s.handleIfreload)
		r.Post("/host/restore", s.handleRestore)
		r.Post("/host/discard-staged", s.handleDiscardStaged)

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
