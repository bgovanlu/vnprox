package api

// metrics_exporter.go implements T-1001's `GET /metrics` Prometheus/
// OpenMetrics text exporter (docs/api.md's Metrics-exporter subsection,
// docs/features/monitoring.md §4): an export surface only, over data
// internal/metrics.Sampler and internal/findings.Engine already compute —
// no new collection logic here.
//
// Auth is deliberately not the session-cookie/CSRF convention every other
// /api/v1 route in this package uses: docs/security.md documents this
// route as the one exception, since a Prometheus scraper cannot carry a
// browser session cookie or a CSRF header. Instead: an optional source-CIDR
// allowlist ([metrics] allow_from), checked first, then a bearer scrape
// token (a random 256-bit value at /etc/vnprox/keys/metrics.key, generated
// alongside the session key) checked with crypto/subtle.ConstantTimeCompare.
// mountMetricsExporterRoutes is therefore called directly from NewRouter,
// outside AuthService.SessionMiddleware/RequireCap entirely — the same
// "this route has its own auth scheme" treatment PeerServer's routes get,
// for the same reason (docs/api.md's Peer API section).

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/metrics"
)

// MetricsCounterService is the exporter's dependency on *metrics.Sampler:
// raw, not-yet-rated per-ref counters (Prometheus does its own
// rate()/increase() over successive scrapes) — unlike MetricsService.Live
// above, which returns pre-computed Rates for docs/api.md's `GET
// /metrics/live`. Declared as its own small interface (rather than folded
// into MetricsService) so every existing MetricsService test double in this
// package keeps compiling unchanged; the real *metrics.Sampler satisfies
// both.
type MetricsCounterService interface {
	AllCounters() []metrics.CounterSnapshot
}

// MetricsExporterConfig configures GET /metrics. A nil/empty Token skips
// mounting the route entirely (the same nil-safe convention every other
// Options field gets) — e.g. when `[metrics] enabled = false` in
// vnprox.toml, cmd/vnproxd never loads/generates a token to begin with.
type MetricsExporterConfig struct {
	BuildVersion string
	Token        []byte
	AllowFrom    []*net.IPNet
}

// changesetExportStatuses is the exact, closed set of change.Status values
// docs/api.md's Metrics-exporter table documents for vnprox_changesets'
// "status" label. Every one of these is always emitted (even at zero) so a
// scraper's label set is stable across scrapes; change.StatusValidated and
// change.StatusDiscarded are deliberately not counted here — they're not in
// the card's documented label vocabulary (validated is a transient
// pre-apply state folded conceptually into "draft" from an operator's
// point of view, and discarded changesets are gone, not a status an
// operator watches a dashboard for).
var changesetExportStatuses = []change.Status{
	change.StatusDraft,
	change.StatusApplying,
	change.StatusAwaitingConfirm,
	change.StatusCommitted,
	change.StatusRolledBack,
	change.StatusFailed,
}

// findingsExportSeverities is the closed severity set vnprox_findings_open's
// "severity" label documents — always emitted (even at zero), same
// stable-label-set reasoning as changesetExportStatuses above.
var findingsExportSeverities = []string{findings.SeverityError, findings.SeverityWarning, findings.SeverityInfo}

// ifaceCounterFamily is one of the eight vnprox_iface_* counter families
// (docs/api.md's Metrics-exporter table): a name/help pair plus the
// Counters field it reads.
type ifaceCounterFamily struct {
	get  func(metrics.Counters) uint64
	name string
	help string
}

var ifaceCounterFamilies = []ifaceCounterFamily{
	{name: "vnprox_iface_rx_bytes_total", help: "Cumulative bytes received, per interface.", get: func(c metrics.Counters) uint64 { return c.RxBytes }},
	{name: "vnprox_iface_tx_bytes_total", help: "Cumulative bytes transmitted, per interface.", get: func(c metrics.Counters) uint64 { return c.TxBytes }},
	{name: "vnprox_iface_rx_packets_total", help: "Cumulative packets received, per interface.", get: func(c metrics.Counters) uint64 { return c.RxPkts }},
	{name: "vnprox_iface_tx_packets_total", help: "Cumulative packets transmitted, per interface.", get: func(c metrics.Counters) uint64 { return c.TxPkts }},
	{name: "vnprox_iface_rx_errors_total", help: "Cumulative receive errors, per interface.", get: func(c metrics.Counters) uint64 { return c.RxErrs }},
	{name: "vnprox_iface_tx_errors_total", help: "Cumulative transmit errors, per interface.", get: func(c metrics.Counters) uint64 { return c.TxErrs }},
	{name: "vnprox_iface_rx_dropped_total", help: "Cumulative receive drops, per interface.", get: func(c metrics.Counters) uint64 { return c.RxDrop }},
	{name: "vnprox_iface_tx_dropped_total", help: "Cumulative transmit drops, per interface.", get: func(c metrics.Counters) uint64 { return c.TxDrop }},
}

// mountMetricsExporterRoutes registers GET /metrics (docs/api.md's
// Metrics-exporter subsection). It mounts nothing when cfg.Token is empty
// (metrics disabled, or the daemon hasn't wired a token — the same
// nil/empty-skips-mounting convention every other optional Options
// dependency in this package follows) or when counters is nil. findingsSvc/
// driftSvc/changesets are each independently nil-safe inside the handler
// (that data family is simply omitted/zeroed, never a 500) so a
// partially-wired daemon (or a router test) can still exercise the iface
// counter families without needing every dependency present.
func mountMetricsExporterRoutes(r chi.Router, counters MetricsCounterService, findingsSvc FindingsService, driftSvc DriftService, changesets ChangesetService, cfg MetricsExporterConfig) {
	if len(cfg.Token) == 0 || counters == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(metricsExporterAuth(cfg))
		r.Get("/metrics", handleMetricsExport(counters, findingsSvc, driftSvc, changesets, cfg.BuildVersion))
	})
}

// metricsExporterAuth implements docs/security.md's two-gate scrape auth:
// the CIDR allowlist first, then the bearer token — both documented there
// as "checked before"/"in addition to" the other, so the allowlist check
// runs first (AC2: a valid token from a disallowed source is still a 403,
// not a 401 — an operator misreading a 401 as "bad token" when the real
// problem is "wrong network" would be actively misleading).
func metricsExporterAuth(cfg MetricsExporterConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !metricsClientAllowed(r, cfg.AllowFrom) {
				writeJSONError(w, http.StatusForbidden, "forbidden", "source address is not permitted to scrape metrics")
				return
			}
			if !metricsTokenValid(r, cfg.Token) {
				writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "missing or invalid scrape token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// metricsClientAllowed reports whether r's source address is permitted by
// allow (nil/empty means "allow any source" — docs/security.md's documented
// default).
func metricsClientAllowed(r *http.Request, allow []*net.IPNet) bool {
	if len(allow) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// r.RemoteAddr had no port (e.g. a bare IP, as some test harnesses
		// set it) — fall back to treating it as the host directly rather
		// than failing closed on a harmless format difference.
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range allow {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// metricsTokenValid checks the request's `Authorization: Bearer <token>`
// header against token using crypto/subtle.ConstantTimeCompare
// (docs/security.md: "checked ... with crypto/subtle.ConstantTimeCompare"),
// exactly as the task requires — not internal/peer's hmac.Equal (which is
// also constant-time, but this route's own doc contract names
// crypto/subtle specifically, and a plain bearer-token compare has no
// message/signature structure to HMAC over in the first place).
func metricsTokenValid(r *http.Request, token []byte) bool {
	if len(token) == 0 {
		return false
	}
	const prefix = "Bearer "
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, prefix) {
		return false
	}
	supplied := strings.TrimPrefix(authz, prefix)
	return subtle.ConstantTimeCompare([]byte(supplied), token) == 1
}

// handleMetricsExport renders the full scrape body: the eight per-ref
// interface counter families, findings-open-by-severity, drift-open,
// changesets-by-status, and vnprox_build_info — in that order, matching
// docs/api.md's Metrics-exporter table.
func handleMetricsExport(counters MetricsCounterService, findingsSvc FindingsService, driftSvc DriftService, changesets ChangesetService, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		writeIfaceCounterFamilies(&buf, counters)
		writeFindingsOpenFamily(&buf, findingsSvc)
		writeDriftOpenFamily(&buf, driftSvc)
		writeChangesetsFamily(&buf, r.Context(), changesets)
		writeBuildInfoFamily(&buf, version)

		// text/plain with the Prometheus exposition-format version
		// parameter (the same content type the reference Go client library
		// and every real Prometheus exporter serve) — AC1's "returns 200
		// text/plain" plus AC3's expfmt-parseable-in-tests requirement both
		// hold for this content type.
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	}
}

func writeIfaceCounterFamilies(buf *bytes.Buffer, counters MetricsCounterService) {
	if counters == nil {
		return
	}
	snaps := counters.AllCounters()
	for _, fam := range ifaceCounterFamilies {
		fmt.Fprintf(buf, "# HELP %s %s\n# TYPE %s counter\n", fam.name, fam.help, fam.name)
		for _, s := range snaps {
			fmt.Fprintf(buf, "%s{ref=%q,node=%q,kind=%q} %d\n",
				fam.name, s.Ref.String(), s.Ref.Node, string(s.Ref.Kind), fam.get(s.Counters))
		}
	}
}

func writeFindingsOpenFamily(buf *bytes.Buffer, svc FindingsService) {
	buf.WriteString("# HELP vnprox_findings_open Current open finding count from the unified findings stream, by severity.\n")
	buf.WriteString("# TYPE vnprox_findings_open gauge\n")
	counts := make(map[string]int, len(findingsExportSeverities))
	if svc != nil {
		for _, f := range svc.Findings() {
			counts[f.Severity]++
		}
	}
	for _, sev := range findingsExportSeverities {
		fmt.Fprintf(buf, "vnprox_findings_open{severity=%q} %d\n", sev, counts[sev])
	}
}

func writeDriftOpenFamily(buf *bytes.Buffer, svc DriftService) {
	buf.WriteString("# HELP vnprox_drift_open Current open cross-node drift finding count.\n")
	buf.WriteString("# TYPE vnprox_drift_open gauge\n")
	n := 0
	if svc != nil {
		n = len(svc.Findings())
	}
	fmt.Fprintf(buf, "vnprox_drift_open %d\n", n)
}

func writeChangesetsFamily(buf *bytes.Buffer, ctx context.Context, svc ChangesetService) {
	buf.WriteString("# HELP vnprox_changesets Current changeset count by status.\n")
	buf.WriteString("# TYPE vnprox_changesets gauge\n")
	counts := make(map[change.Status]int, len(changesetExportStatuses))
	if svc != nil {
		if all, err := svc.List(ctx, ""); err == nil {
			for _, c := range all {
				counts[c.Status]++
			}
		}
	}
	for _, st := range changesetExportStatuses {
		fmt.Fprintf(buf, "vnprox_changesets{status=%q} %d\n", string(st), counts[st])
	}
}

func writeBuildInfoFamily(buf *bytes.Buffer, version string) {
	buf.WriteString("# HELP vnprox_build_info vnproxd build info; the sample value is always 1.\n")
	buf.WriteString("# TYPE vnprox_build_info gauge\n")
	fmt.Fprintf(buf, "vnprox_build_info{version=%q} 1\n", version)
}
