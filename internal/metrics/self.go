package metrics

// self.go implements T-1903's self-observability registry: RED-shaped
// metrics about vnproxd itself (HTTP, the collector, the change engine, the
// store, peer RPCs, WebSocket connections), as opposed to the rest of this
// package (Sampler et al.), which reports on the *cluster* vnprox observes.
// docs/features/monitoring.md §9 documents every series this file can
// produce, including each one's label set and cardinality bound.
//
// Design note: this is a small, hand-rolled counter/histogram
// implementation rather than a dependency on prometheus/client_golang.
// docs/development.md's dependency table doesn't list a Prometheus client
// library, and internal/api/metrics_exporter.go already hand-renders the
// exposition text format for the cluster-derived families (T-1001) — this
// file follows that precedent rather than introducing a new dependency for
// what is, structurally, the same rendering job. See this task's report for
// the explicit "no new dependency" note per CLAUDE.md's ground rules.
//
// Cardinality safety (AC1) is enforced two ways, deliberately redundant:
//  1. every label value this file's callers pass is drawn from a small,
//     compile-time-fixed vocabulary (an HTTP route *pattern*, never a raw
//     path; a status *class*, never a raw status code; a fixed outcome
//     enum) — never anything an end user or a cluster object's name/id
//     controls;
//  2. belt-and-suspenders, every counterVec/histogramVec below still caps
//     the number of distinct label combinations it will track (seriesCap)
//     and logs once, at WARN, if a caller's label source somehow turns out
//     to be unbounded after all — so a future regression degrades into a
//     loud log line and a capped "cardinality_overflow" bucket, never an
//     unbounded memory/series leak.

import (
	"bytes"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// defaultSeriesCap is the per-vec safety ceiling described in this file's
// package-level doc comment. Every family registered below stays orders of
// magnitude under this in practice (see docs/features/monitoring.md §9's
// per-series cardinality bound); it exists purely as a backstop.
const defaultSeriesCap = 4000

// overflowLabel is the label-tuple every over-cap observation collapses
// into, keeping the series count bounded even once a vec's cap is reached.
const overflowLabel = "cardinality_overflow"

func labelKey(values []string) string {
	// \x1f (unit separator) never appears in any label value this package
	// emits (route patterns, method names, outcome enums, ...), so this is
	// injective without needing to escape anything.
	return strings.Join(values, "\x1f")
}

// counterVec is a Prometheus-style counter keyed by an ordered tuple of
// label values.
type counterVec struct {
	logger      *slog.Logger
	values      map[string]*uint64
	labelValues map[string][]string
	name        string
	help        string
	labelNames  []string
	cap         int
	mu          sync.Mutex
	overflowed  bool
}

func newCounterVec(name, help string, labelNames []string, logger *slog.Logger) *counterVec {
	return &counterVec{
		name: name, help: help, labelNames: labelNames, cap: defaultSeriesCap,
		logger:      logger,
		values:      map[string]*uint64{},
		labelValues: map[string][]string{},
	}
}

func (c *counterVec) inc(values ...string) { c.add(1, values...) }

func (c *counterVec) add(delta uint64, values ...string) {
	if len(values) != len(c.labelNames) {
		panic(fmt.Sprintf("metrics: %s: expected %d label values, got %d", c.name, len(c.labelNames), len(values)))
	}
	key := labelKey(values)
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.values[key]
	if !ok {
		if len(c.values) >= c.cap {
			c.logOverflowLocked(values)
			key = overflowLabel
			values = overflowValues(c.labelNames)
			p, ok = c.values[key]
			if !ok {
				var z uint64
				p = &z
				c.values[key] = p
				c.labelValues[key] = values
			}
		} else {
			var z uint64
			p = &z
			c.values[key] = p
			c.labelValues[key] = append([]string(nil), values...)
		}
	}
	*p += delta
}

func (c *counterVec) logOverflowLocked(values []string) {
	if c.overflowed {
		return
	}
	c.overflowed = true
	logger := c.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("metrics: series cardinality cap reached; further distinct label combinations are folded into one overflow series",
		"metric", c.name, "cap", c.cap, "labels", values)
}

func overflowValues(labelNames []string) []string {
	out := make([]string, len(labelNames))
	for i := range out {
		out[i] = overflowLabel
	}
	return out
}

func (c *counterVec) writeTo(buf *bytes.Buffer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.values) == 0 {
		return
	}
	fmt.Fprintf(buf, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	for _, key := range sortedKeys(c.values) {
		writeSample(buf, c.name, "", c.labelNames, c.labelValues[key], float64(*c.values[key]))
	}
}

// histogramVec is a Prometheus-style cumulative histogram keyed by an
// ordered tuple of label values, with a fixed set of bucket upper bounds
// (seconds) shared by every label combination in the vec.
type histogramVec struct {
	logger      *slog.Logger
	data        map[string]*histogramData
	labelValues map[string][]string
	name        string
	help        string
	labelNames  []string
	buckets     []float64
	cap         int
	mu          sync.Mutex
	overflowed  bool
}

type histogramData struct {
	bucketCounts []uint64 // len(buckets); exact (non-cumulative) per-bucket counts
	sum          float64
	count        uint64
}

func newHistogramVec(name, help string, labelNames []string, buckets []float64, logger *slog.Logger) *histogramVec {
	return &histogramVec{
		name: name, help: help, labelNames: labelNames, buckets: buckets, cap: defaultSeriesCap,
		logger:      logger,
		data:        map[string]*histogramData{},
		labelValues: map[string][]string{},
	}
}

func (h *histogramVec) observe(seconds float64, values ...string) {
	if len(values) != len(h.labelNames) {
		panic(fmt.Sprintf("metrics: %s: expected %d label values, got %d", h.name, len(h.labelNames), len(values)))
	}
	if seconds < 0 {
		seconds = 0
	}
	key := labelKey(values)
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.data[key]
	if !ok {
		if len(h.data) >= h.cap {
			h.logOverflowLocked(values)
			key = overflowLabel
			values = overflowValues(h.labelNames)
			d, ok = h.data[key]
			if !ok {
				d = &histogramData{bucketCounts: make([]uint64, len(h.buckets))}
				h.data[key] = d
				h.labelValues[key] = values
			}
		} else {
			d = &histogramData{bucketCounts: make([]uint64, len(h.buckets))}
			h.data[key] = d
			h.labelValues[key] = append([]string(nil), values...)
		}
	}
	idx := sort.SearchFloat64s(h.buckets, seconds)
	if idx < len(h.buckets) {
		d.bucketCounts[idx]++
	}
	d.sum += seconds
	d.count++
}

func (h *histogramVec) logOverflowLocked(values []string) {
	if h.overflowed {
		return
	}
	h.overflowed = true
	logger := h.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("metrics: series cardinality cap reached; further distinct label combinations are folded into one overflow series",
		"metric", h.name, "cap", h.cap, "labels", values)
}

func (h *histogramVec) writeTo(buf *bytes.Buffer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.data) == 0 {
		return
	}
	fmt.Fprintf(buf, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	for _, key := range sortedKeys(h.data) {
		d := h.data[key]
		labels := h.labelValues[key]
		var cumulative uint64
		for i, ub := range h.buckets {
			cumulative += d.bucketCounts[i]
			writeSample(buf, h.name, "_bucket", h.labelNames, labels, float64(cumulative), leLabel(ub))
		}
		writeSample(buf, h.name, "_bucket", h.labelNames, labels, float64(d.count), infBound())
		writeSample(buf, h.name, "_sum", h.labelNames, labels, d.sum)
		writeSample(buf, h.name, "_count", h.labelNames, labels, float64(d.count))
	}
}

// leBound is a small helper type so writeSample's variadic extra-label
// mechanism can carry histogram's "le" label alongside the vec's own
// labelNames without a bespoke code path for that one family.
type leBound struct{ value string }

func leLabel(ub float64) leBound { return leBound{value: formatFloat(ub)} }
func infBound() leBound          { return leBound{value: "+Inf"} }

func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

// writeSample renders one exposition-format sample line:
// `name<suffix>{l1="v1",...} value`, optionally with a trailing "le" label
// (histogram bucket bound) appended after the vec's own labels.
func writeSample(buf *bytes.Buffer, name, suffix string, labelNames, labelValues []string, value float64, le ...leBound) {
	buf.WriteString(name)
	buf.WriteString(suffix)
	if len(labelNames) > 0 || len(le) > 0 {
		buf.WriteByte('{')
		for i, n := range labelNames {
			if i > 0 {
				buf.WriteByte(',')
			}
			fmt.Fprintf(buf, "%s=%q", n, labelValues[i])
		}
		if len(le) > 0 {
			if len(labelNames) > 0 {
				buf.WriteByte(',')
			}
			fmt.Fprintf(buf, "le=%q", le[0].value)
		}
		buf.WriteByte('}')
	}
	fmt.Fprintf(buf, " %s\n", formatFloat(value))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- bucket sets (seconds) ---

// httpDurationBuckets matches the Prometheus client library's own default
// HTTP histogram buckets — a well-understood shape for request latency.
var httpDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// pollDurationBuckets are wider: a collector poll round-trips a PVE API
// call or a full-cluster host fan-out, not a single in-process handler.
var pollDurationBuckets = []float64{.01, .025, .05, .1, .25, .5, 1, 2, 5, 10, 30}

// storeQueryDurationBuckets are tight: SQLite on local disk, not a network
// round trip.
var storeQueryDurationBuckets = []float64{.0001, .0005, .001, .0025, .005, .01, .025, .05, .1, .5, 1}

// peerCallDurationBuckets mirror httpDurationBuckets — a peer RPC is
// another daemon's own HTTP handler, one hop away.
var peerCallDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// awaitingConfirmDurationBuckets span the commit-confirm window's real
// range (docs/features/change-management.md §4: a handful of minutes up to
// the configured max), not sub-second request latency.
var awaitingConfirmDurationBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600}

// Registry holds every push-model self-observability series T-1903 adds.
// Pull-model series (store size on disk, schema version, WS connection
// count, collector consecutive-failure gauges) are rendered directly from
// their existing source of truth at scrape time — see
// internal/api/metrics_exporter.go's writeSelfMetrics/writePullMetrics —
// rather than duplicated here, per this task's card: "mirror what /health
// already tracks rather than inventing a second notion."
//
// Every method is nil-receiver-safe is NOT true here (Registry itself must
// be non-nil), but every *caller* of a Registry method in this codebase
// treats a nil *Registry as "metrics disabled" and skips the call — the
// same nil-safe-optional-dependency convention every other cross-package
// hook in this codebase (OnStats, OnDelta, Broadcaster, ...) already uses.
type Registry struct {
	httpRequests *counterVec
	httpDuration *histogramVec

	collectorPolls        *counterVec
	collectorPollDuration *histogramVec

	changeOutcomes          *counterVec
	awaitingConfirmDuration *histogramVec

	storeQueryDuration *histogramVec

	peerCalls        *counterVec
	peerCallDuration *histogramVec
}

// NewRegistry constructs an empty Registry. logger is used only for the
// (expected-never-to-fire) cardinality-overflow warning every vec above
// carries; nil defaults to slog.Default().
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		httpRequests: newCounterVec("vnprox_http_requests_total",
			"Total HTTP requests handled, by route pattern, method, and status class.",
			[]string{"route", "method", "status_class"}, logger),
		httpDuration: newHistogramVec("vnprox_http_request_duration_seconds",
			"HTTP request duration in seconds, by route pattern and method.",
			[]string{"route", "method"}, httpDurationBuckets, logger),

		collectorPolls: newCounterVec("vnprox_collector_polls_total",
			"Total collector poll attempts, by source, node, and outcome.",
			[]string{"source", "node", "outcome"}, logger),
		collectorPollDuration: newHistogramVec("vnprox_collector_poll_duration_seconds",
			"Collector poll duration in seconds, by source and node.",
			[]string{"source", "node"}, pollDurationBuckets, logger),

		changeOutcomes: newCounterVec("vnprox_change_outcomes_total",
			"Total change-engine operations, by op and outcome.",
			[]string{"op", "outcome"}, logger),
		awaitingConfirmDuration: newHistogramVec("vnprox_change_awaiting_confirm_seconds",
			"Time a changeset spent in awaiting_confirm before leaving it, by the status it left to.",
			[]string{"outcome"}, awaitingConfirmDurationBuckets, logger),

		storeQueryDuration: newHistogramVec("vnprox_store_query_duration_seconds",
			"SQLite store query/exec duration in seconds, by statement kind.",
			[]string{"op"}, storeQueryDurationBuckets, logger),

		peerCalls: newCounterVec("vnprox_peer_calls_total",
			"Total peer-API RPCs issued, by node, endpoint, and outcome.",
			[]string{"node", "endpoint", "outcome"}, logger),
		peerCallDuration: newHistogramVec("vnprox_peer_call_duration_seconds",
			"Peer-API RPC duration in seconds, by node and endpoint.",
			[]string{"node", "endpoint"}, peerCallDurationBuckets, logger),
	}
}

// --- HTTP RED (internal/api's redMetricsMiddleware) ---

// StatusClass reduces an HTTP status code to Prometheus's conventional
// "NxX" class label ("2xx".."5xx", "other" for anything outside 1xx-5xx),
// bounding this label to six values regardless of which of the ~60 distinct
// status codes this codebase's handlers actually return.
func StatusClass(status int) string {
	switch {
	case status >= 100 && status < 200:
		return "1xx"
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "other"
	}
}

// ObserveHTTPRequest records one completed HTTP request. route must be a
// route *pattern* (e.g. "/api/v1/changesets/{id}"), never a raw request
// path — see this file's package doc comment and
// internal/api/redmetrics.go's routeLabel, the only caller.
func (r *Registry) ObserveHTTPRequest(route, method string, status int, dur time.Duration) {
	class := StatusClass(status)
	r.httpRequests.inc(route, method, class)
	r.httpDuration.observe(dur.Seconds(), route, method)
}

// --- Collector (internal/collect.Collector's OnPoll hook) ---

// ObserveCollectorPoll records one collector poll attempt. source is
// "pve"|"host"|"lldp" (collect.Collector's own closed source-name
// vocabulary); node is "" for a cluster-wide source ("pve") or the polled
// node's name for a per-node source ("host", "lldp") — the same scoping
// collect.SourceStatus already uses.
func (r *Registry) ObserveCollectorPoll(source, node string, dur time.Duration, err error) {
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	r.collectorPolls.inc(source, node, outcome)
	r.collectorPollDuration.observe(dur.Seconds(), source, node)
}

// --- Change engine (internal/change.Service) ---

// Change-engine op labels (docs/features/monitoring.md §9's closed
// vocabulary for vnprox_change_outcomes_total's "op" label).
const (
	ChangeOpApply            = "apply"
	ChangeOpConfirm          = "confirm"
	ChangeOpRollback         = "rollback"
	ChangeOpUnattendedRevert = "unattended_revert"
)

// ObserveChangeOutcome records one change-engine operation's terminal
// outcome. success=false covers every failure shape (validation-blocked,
// apply error, rollback-incomplete, ...) — docs/features/monitoring.md §9
// argues this coarse a label is the right bound; a finer-grained reason
// belongs in the audit trail (already structured, already queryable), not
// a metric label.
func (r *Registry) ObserveChangeOutcome(op string, success bool) {
	outcome := "success"
	if !success {
		outcome = "failure"
	}
	r.changeOutcomes.inc(op, outcome)
}

// ObserveAwaitingConfirmDuration records how long a changeset spent in
// awaiting_confirm before leaving it. outcome is "committed"|"rolled_back"|
// "failed" — the three ways a changeset can leave that status.
func (r *Registry) ObserveAwaitingConfirmDuration(outcome string, dur time.Duration) {
	r.awaitingConfirmDuration.observe(dur.Seconds(), outcome)
}

// --- Store (internal/store.DB's query observer) ---

// ObserveStoreQuery records one store query/exec's duration. op is a SQL
// statement kind ("select"|"insert"|"update"|"delete"|"tx"|"other" — see
// internal/store's queryOp), never a raw query string.
func (r *Registry) ObserveStoreQuery(op string, dur time.Duration) {
	r.storeQueryDuration.observe(dur.Seconds(), op)
}

// --- Peer RPC (internal/peer.Client) ---

// ObservePeerCall records one peer-API RPC attempt. endpoint is the
// request path with its query string stripped (every peer.Client call site
// builds its path from a literal template, e.g. "/api/peer/host/stats", so
// this is already a closed, compile-time-bounded vocabulary — see
// internal/peer/client.go's do). outcome is peer.PeerTrustState's own
// closed vocabulary ("ok"|"unreachable"|"untrusted").
func (r *Registry) ObservePeerCall(node, endpoint, outcome string, dur time.Duration) {
	r.peerCalls.inc(node, endpoint, outcome)
	r.peerCallDuration.observe(dur.Seconds(), node, endpoint)
}

// WriteTo renders every push-model series this Registry holds, in
// Prometheus/OpenMetrics text exposition format, appending to buf. A
// family with zero observations so far is omitted entirely (unlike the
// cluster-derived families in internal/api/metrics_exporter.go, these
// label sets are not a small closed enumeration known up front — there is
// no fixed "every route" or "every peer node" list to zero-fill against
// before the router/cluster exists).
func (r *Registry) WriteTo(buf *bytes.Buffer) {
	r.httpRequests.writeTo(buf)
	r.httpDuration.writeTo(buf)
	r.collectorPolls.writeTo(buf)
	r.collectorPollDuration.writeTo(buf)
	r.changeOutcomes.writeTo(buf)
	r.awaitingConfirmDuration.writeTo(buf)
	r.storeQueryDuration.writeTo(buf)
	r.peerCalls.writeTo(buf)
	r.peerCallDuration.writeTo(buf)
}
