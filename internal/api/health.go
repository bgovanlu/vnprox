package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type healthResponse struct {
	Status     string                  `json:"status"`
	Version    string                  `json:"version"`
	Collectors []CollectorSourceStatus `json:"collectors,omitempty"`
}

// CollectorHealth is the subset of T-104's *collect.Collector the health
// handler needs: a per-source staleness snapshot. Declared as an interface
// here (rather than importing internal/collect's concrete type) for the
// same reason AuthService is: it keeps this package's dependency on that
// task's package a one-method seam. cmd/vnproxd supplies a small adapter
// converting collect.Collector.Status() into this shape (see that
// package's own doc comment on why the conversion lives there rather than
// on either side importing the other).
type CollectorHealth interface {
	CollectorStatus() []CollectorSourceStatus
}

// CollectorSourceStatus is one poll loop's ("pve", "host", or "lldp")
// staleness snapshot, surfaced in GET /api/v1/health's optional
// "collectors" field (deliverable 4: "staleness tracking per source
// exposed on /api/v1/health").
type CollectorSourceStatus struct {
	LastSuccess         time.Time `json:"last_success,omitempty"`
	LastAttempt         time.Time `json:"last_attempt,omitempty"`
	Name                string    `json:"name"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
}

// healthHandler serves GET /api/v1/health ->
// {"status":"ok","version":"...","collectors":[...]}. collectors is nil
// (and the field omitted) when ch is nil, preserving this endpoint's
// original minimal shape for callers/tests that don't care about
// collector staleness.
func healthHandler(version string, ch CollectorHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := healthResponse{Status: "ok", Version: version}
		if ch != nil {
			resp.Collectors = ch.CollectorStatus()
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
