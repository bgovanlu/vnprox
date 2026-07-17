package api

import (
	"encoding/json"
	"net/http"
)

// InstanceInfo is the non-secret, operational configuration GET
// /api/v1/config surfaces to authenticated users — the raw material for the
// Settings page's "Instance" section (docs/features surfaced read-only,
// since vnprox.toml is a per-node, restart-time file, not runtime-editable).
//
// It deliberately excludes every secret or secret-bearing value: the PVE
// token/token file, the session encryption key, the peer cluster secret,
// TLS private key material, and the dev-only ticket username/password. Only
// values that are safe to show an operator who is already authenticated —
// timers, retention policy, mode flags, and non-secret paths/URLs — appear
// here.
type InstanceInfo struct {
	LLDPInterval  string `json:"lldpInterval"`
	PVEAPIURL     string `json:"pveApiUrl"`
	ProtectedPath string `json:"protectedPath"`
	PVEInterval   string `json:"pveInterval"`
	HostInterval  string `json:"hostInterval"`
	Version       string `json:"version"`
	Listen        string `json:"listen"`
	// HostSampler is T-1004's addition: which host-local flow sampler (if
	// any) is active on this node — "" (neither configured, or eBPF
	// configured but its kernel-feature probe failed at startup with no
	// conntrack fallback also enabled), "conntrack", or "ebpf". Mirrors
	// MetricsEnabled's "safe mode flag, not a secret" bar; captured once at
	// daemon start like every other field here (cmd/vnproxd/hostsample.go's
	// setupHostSample runs the eBPF probe exactly once at startup).
	HostSampler              string `json:"hostSampler,omitempty"`
	ConfirmTimeoutDefaultSec int    `json:"confirmTimeoutDefaultSec"`
	SnapshotKeepDays         int    `json:"snapshotKeepDays"`
	SnapshotPinDays          int    `json:"snapshotPinDays"`
	// MetricsEnabled is T-1001's addition: whether `GET /metrics` (the
	// Prometheus exporter) is mounted on this node — [metrics] enabled from
	// vnprox.toml, surfaced read-only like every other field here. Not a
	// secret itself (unlike the scrape token it gates), just a mode flag,
	// matching this struct's existing "safe to show an already-
	// authenticated operator" bar.
	MetricsEnabled    bool `json:"metricsEnabled"`
	AllowDangerousOps bool `json:"allowDangerousOps"`
	ReadOnly          bool `json:"readOnly"`
}

// configHandler serves GET /api/v1/config -> InstanceInfo. It is a pure
// snapshot of the daemon's own loaded config (captured once at router
// construction), so it needs no service dependency and never touches a
// secret.
func configHandler(info InstanceInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
	}
}
