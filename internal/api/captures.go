package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/capture"
)

// capCapture is T-1301's dedicated packet-capture capability name (its JSON
// field name), gating every /captures route on top of netRead — a session
// must hold BOTH (docs/security.md's Authorization section). Kept as a local
// const mirroring capNetRead/capNetWrite's own in-package convention.
const capCapture = "capture"

// maxCaptureBodyBytes bounds the POST /captures request body — a capture
// request is a target ref, a short filter, and a few numeric caps, so this
// is generous headroom against an abusive client, matching
// maxProtectedBodyBytes' reasoning.
const maxCaptureBodyBytes = 1 << 16

// CaptureService is the subset of *capture.Coordinator the /captures routes
// need. *capture.Coordinator satisfies it directly (no adapter). The
// coordinator owns every safety decision (capability is enforced by the
// route; caps/filter/audit/retention are enforced inside it), so this
// handler is a thin HTTP shell over it — it never re-implements or weakens
// any cap or filter check.
type CaptureService interface {
	Start(ctx context.Context, req capture.StartRequest) (capture.Group, error)
	StopGroup(ctx context.Context, groupID, actor string) (capture.Group, error)
	Get(ctx context.Context, groupID string) (capture.Group, error)
	List(ctx context.Context) ([]capture.Group, error)
	// Download returns one session's raw pcap bytes (T-1302) — never a whole
	// group, since a multi-point capture has one file per node. Resolves
	// local vs. peer-node sessions transparently (cluster-aware, per
	// CLAUDE.md); the on-disk path itself is never part of this contract.
	Download(ctx context.Context, sessionID string) ([]byte, capture.Session, error)
}

// captureStartRequest is POST /captures' body (docs/api.md's Captures
// section). durationSec/maxBytes/maxPackets are *requests* — the server
// clamps them down to its configured, un-overridable ceilings; a client can
// never raise a cap past the ceiling here.
type captureStartRequest struct {
	TargetRef   string   `json:"targetRef"`
	Filter      string   `json:"filter"`
	PeerTargets []string `json:"peerTargets"`
	MaxBytes    int64    `json:"maxBytes"`
	MaxPackets  int64    `json:"maxPackets"`
	DurationSec int      `json:"durationSec"`
}

// captureListResponse is GET /captures' envelope.
type captureListResponse struct {
	Items []capture.Group `json:"items"`
}

// mountCaptureRoutes registers POST /captures, POST /captures/{id}/stop,
// GET /captures/{id}, and GET /captures (T-1301, docs/api.md's Captures
// section). Every route is gated netRead + capture; the mutating ones also
// require CSRF (starting/stopping a capture is a host-touching action, even
// though it bypasses the change engine — there is nothing to diff/rollback
// about a read-only packet capture). svc/auth nil-safe (routes not mounted);
// UsernameLookup is required so start/stop is attributable in the audit
// trail, matching mountLLDPInstallRoutes' own reasoning.
func mountCaptureRoutes(r chi.Router, svc CaptureService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Use(auth.RequireCap(capCapture))
		r.Get("/captures", handleListCaptures(svc))
		r.Get("/captures/{id}", handleGetCapture(svc))
		r.Get("/captures/{id}/download", handleDownloadCapture(svc))
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetRead))
		r.Use(auth.RequireCap(capCapture))
		r.Post("/captures", handleStartCapture(svc, lookup))
		r.Post("/captures/{id}/stop", handleStopCapture(svc, lookup))
	})
}

func handleStartCapture(svc CaptureService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCaptureBodyBytes))
		dec.DisallowUnknownFields()
		var req captureStartRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "invalid capture request body")
			return
		}
		if req.TargetRef == "" && len(req.PeerTargets) == 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "targetRef or peerTargets is required")
			return
		}
		group, err := svc.Start(r.Context(), capture.StartRequest{
			TargetRef:   req.TargetRef,
			Filter:      req.Filter,
			DurationSec: req.DurationSec,
			MaxBytes:    req.MaxBytes,
			MaxPackets:  req.MaxPackets,
			PeerTargets: req.PeerTargets,
			StartedBy:   username,
		})
		if err != nil {
			writeCaptureError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, group)
	}
}

func handleStopCapture(svc CaptureService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		group, err := svc.StopGroup(r.Context(), id, username)
		if err != nil {
			writeCaptureError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, group)
	}
}

func handleGetCapture(svc CaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		group, err := svc.Get(r.Context(), id)
		if err != nil {
			writeCaptureError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, group)
	}
}

func handleListCaptures(svc CaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groups, err := svc.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list captures")
			return
		}
		if groups == nil {
			groups = []capture.Group{}
		}
		writeJSON(w, http.StatusOK, captureListResponse{Items: groups})
	}
}

// handleDownloadCapture implements GET /captures/{id}/download?sessionId=
// (T-1302, docs/api.md's Captures section): the per-session pcap download
// button. {id} is the capture *group* id, matching every other /captures/{id}
// route's convention; ?sessionId= disambiguates which node's session to
// download for a multi-point group (defaults to the group's first/primary
// session — the common single-node case needs no query param at all). The
// coordinator resolves local-vs-peer transparently — this handler never
// touches a file path itself, only the bytes the coordinator hands back.
func handleDownloadCapture(svc CaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupID := chi.URLParam(r, "id")
		group, err := svc.Get(r.Context(), groupID)
		if err != nil {
			writeCaptureError(w, err)
			return
		}
		if len(group.Sessions) == 0 {
			writeJSONError(w, http.StatusNotFound, "not_found", "capture group has no sessions")
			return
		}
		sessionID := r.URL.Query().Get("sessionId")
		target := &group.Sessions[0]
		if sessionID != "" {
			target = nil
			for i := range group.Sessions {
				if group.Sessions[i].ID == sessionID {
					target = &group.Sessions[i]
					break
				}
			}
			if target == nil {
				writeJSONError(w, http.StatusNotFound, "not_found", "session not found in this capture group")
				return
			}
		}
		data, sess, err := svc.Download(r.Context(), target.ID)
		if err != nil {
			writeCaptureError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
		w.Header().Set("Content-Disposition", `attachment; filename="`+sess.ID+`.pcap"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// writeCaptureError maps the capture engine's sentinel errors to HTTP status
// codes: a rejected filter or unresolvable target is a 400 (the client's
// request was bad), an unknown group/session or a purged file is a 404,
// everything else a 500.
func writeCaptureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, capture.ErrInvalidFilter):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
	case errors.Is(err, capture.ErrUnresolvableTarget):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
	case errors.Is(err, capture.ErrNoTargets):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
	case errors.Is(err, capture.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "capture not found")
	case errors.Is(err, capture.ErrFileUnavailable):
		writeJSONError(w, http.StatusNotFound, "not_found", "this capture's file is no longer available (purged past retention)")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "capture operation failed")
	}
}
