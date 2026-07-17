// Package api implements the HTTP router, handlers, and middleware stack
// for vnproxd: request id / structured logging / panic recovery / security
// headers, the /api/v1/health endpoint, and embedded-SPA serving with
// SPA-fallback routing. The WS hub and the rest of docs/api.md's routes
// land in later tasks (auth, changesets, topology, ...); this package only
// implements what T-002 requires.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// AuthService is the subset of *auth.Service the router needs: route
// registration for docs/api.md's Auth endpoints, plus the session/
// capability middleware later capability-gated route registrations (T-106
// topology, and eventually the change engine) wrap themselves in. Declared
// as an interface here (rather than importing internal/auth's concrete
// type) so this package's dependency on T-105's auth package stays a small
// seam — internal/api does not otherwise know or care how login/session/
// CSRF/capability-derivation works.
//
// RequireCap takes the capability's plain string name (its JSON field name,
// e.g. "netRead") rather than internal/auth's own Cap type, so this
// interface doesn't need to import that package's types either;
// cmd/vnproxd's wiring (see its authServiceAdapter) is what bridges the
// concrete *auth.Service (whose RequireCap takes auth.Cap) to this shape.
type AuthService interface {
	MountRoutes(r chi.Router)
	SessionMiddleware(next http.Handler) http.Handler
	RequireCap(cap string) func(http.Handler) http.Handler
}

// PeerServer is the subset of T-301's *peer.Server the router needs: a
// single call that registers the entire documented /api/peer/* subtree
// (docs/api.md's "Peer API" section), including that package's own HMAC
// auth middleware — unlike every other MountRoutes-shaped seam in this
// file, PeerServer's routes are deliberately *not* wrapped in
// AuthService.SessionMiddleware/RequireCap: docs/security.md's peer auth
// section is explicit that SPA session cookies grant nothing on peer
// routes, so the only gate is internal/peer's own cluster-secret HMAC
// check. Declared as an interface (the same pattern as AuthService/
// TopologyService above) purely to keep this package's dependency on
// internal/peer's concrete type to a one-method seam.
type PeerServer interface {
	MountRoutes(r chi.Router)
}

// Options configures the router built by NewRouter.
type Options struct {
	DistFS     fs.FS
	Auth       AuthService
	Collectors CollectorHealth
	Topology   TopologyService
	LLDP       LLDPService
	Drift      DriftService
	// Findings is T-602's unified findings-stream seam (drift+lldp+ipam+
	// health composed by *findings.Engine): backs `GET /findings`,
	// `POST /findings/{id}/fix`, and (superseding Drift for this purpose
	// when set) the `GET /topology` finding-badge overlay. Nil simply
	// omits the /findings routes and falls back to Drift-only badge
	// painting — see handleTopology's doc comment.
	Findings FindingsService
	FDB      FDBService
	Layouts  LayoutStore
	// Annotations backs T-907's GET/POST /annotations and
	// DELETE /annotations/{id} (entity-pinned sticky notes); nil-safe like
	// Layouts above (routes simply aren't mounted).
	Annotations AnnotationStore
	// AlertRules/AlertDeliveries/AlertSecretCipher back T-1005's alert
	// routing CRUD + delivery log (GET/POST /alert-rules,
	// GET/PUT/DELETE /alert-rules/{id}, POST /alert-rules/{id}/test,
	// GET /alert-deliveries); all three (plus Auth) are required together —
	// any one nil skips mounting every route in this family, matching
	// Layouts/Annotations' own degraded-mode convention.
	AlertRules        AlertRuleStore
	AlertDeliveries   AlertDeliveryStore
	AlertSecretCipher SecretCipher
	Changesets        ChangesetService
	Snapshots         SnapshotService
	Audit             AuditService
	// SDN is T-401's read view seam (docs/api.md's `GET /sdn`); nil (no
	// PVE client — see cmd/vnproxd/collect.go's setupCollect doc comment)
	// simply skips mounting the route, the same degraded-mode treatment
	// every other optional Options field gets.
	SDN SDNService
	// IPAM is T-405's read view seam (docs/api.md's `GET /ipam/subnets` and
	// `GET /ipam/subnets/{cidr}/allocations`); nil-safe like SDN above.
	IPAM IPAMService
	// EVPN is T-404's read view seam (docs/api.md's `GET /sdn/evpn/status`);
	// nil (no PVE/peer clients wired) simply skips mounting the route,
	// same degraded-mode treatment as SDN above.
	EVPN EVPNService
	// DHCP is T-406's read view seam (docs/api.md's `GET /sdn/dhcp`:
	// static reservations + live leases); nil-safe like SDN/EVPN above.
	DHCP DHCPService
	// Metrics is T-601's *metrics.Sampler seam for GET /metrics/live and
	// GET /metrics/history; nil (no daemon-side sampler wired, e.g. tests)
	// simply omits both routes.
	Metrics MetricsService
	// MetricsCounters is T-1001's exporter seam over the same underlying
	// *metrics.Sampler as Metrics above (the concrete type satisfies both
	// interfaces) — kept as its own Options field rather than a type
	// assertion on Metrics so a test can wire a MetricsCounterService
	// double without also having to implement MetricsService's Live/
	// History methods. Nil (together with a zero MetricsExporter.Token)
	// skips mounting GET /metrics entirely.
	MetricsCounters MetricsCounterService
	// MetricsExporter carries GET /metrics' own auth config (scrape token +
	// optional CIDR allowlist, docs/security.md's Authentication section) —
	// deliberately not routed through AuthService, since this route is the
	// one documented exception to the session-cookie/CSRF convention.
	MetricsExporter MetricsExporterConfig
	PVEGateways     PVEGatewayProvider
	// Protected backs GET/PUT /protected-interfaces + /suggest (T-203) and
	// GET /protected-interfaces/status (T-702). Also passed into
	// mountTopologyRoutes as the mgmt/corosync/mgmt-path badge-painting
	// input on `GET /topology` (docs/features/topology.md §3) — the same
	// "internal/api decorates the pure projection" seam Findings/Drift
	// above already use for the finding-badge overlay; nil skips both.
	Protected ProtectedService
	// Firewall backs T-501's read routes (GET /firewall/rulesets,
	// GET /firewall/objects) — typically the daemon's live *inventory.Graph
	// (which satisfies FirewallGraph's one-method seam directly).
	Firewall   FirewallGraph
	Blueprints BlueprintService
	// Simulator backs T-503's `POST /simulate/path` — the same live
	// *inventory.Graph (satisfies SimulatorGraph's one-method seam directly).
	Simulator SimulatorGraph
	// ProbeClients backs T-802's `POST /simulate/verify` live-probe route
	// and T-806's `GET /simulate/verify/eligibility` (nil skips mounting
	// both, same degraded-mode treatment as every other optional Options
	// field). ProbeAudit is /simulate/verify's probe.verify audit-log
	// seam, nil-safe on its own (see auditSimulateVerify's doc comment).
	// SimDivergence is T-806's persisted sim_divergence finding seam
	// (nil-safe — see recordDivergence's doc comment); typically the
	// daemon's own *store.SimDivergenceRepo.
	ProbeClients  ProbeClientProvider
	ProbeAudit    simulateVerifyAuditor
	SimDivergence simDivergenceRecorder
	// FwLog backs T-505's GET /firewall/log (docs/features/firewall.md
	// §4) — typically the daemon's *fwlog.Service, which also owns the
	// `firewall.log.batch` WS push (fed directly from its own Run loop
	// over the shared hub, not through this router — the same
	// "producer pushes over the shared hub directly" pattern
	// internal/topology.Service.Broadcast's other callers use).
	FwLog FwLogService
	Peer  PeerServer
	// PeerAudit and PeerSnapshots are T-303's cluster fan-out dependencies
	// for GET /audit and GET /snapshots (docs/architecture.md §7: "Audit/
	// snapshot queries in the UI fan out to peers and merge"). Nil (every
	// pre-T-303 caller) preserves the original node-local-only behavior of
	// both routes exactly.
	PeerAudit     PeerAuditSource
	PeerSnapshots PeerSnapshotSource
	// DocExport backs T-605's GET /export/doc (config documentation
	// export); nil skips mounting the route, matching every other optional
	// Options field.
	DocExport DocExportService
	// LLDPInstaller/LLDPPeerInstaller/LLDPAudit/LocalNode back T-605's
	// POST /lldp/install (the onboarding walkthrough's "LLDP offer" step,
	// docs/user-guide.md §1.3); LLDPInstaller nil skips mounting the route.
	LLDPInstaller     LocalLLDPInstaller
	LLDPPeerInstaller PeerLLDPInstaller
	LLDPAudit         lldpInstallAuditor
	LocalNode         func() string
	Logger            *slog.Logger
	Version           string
	// Instance is the non-secret operational config surfaced by GET
	// /config (the Settings page's "Instance" section). Zero value is fine
	// — the route still mounts and reports whatever's set (Version at
	// minimum). Never populate it with a secret; see InstanceInfo.
	Instance InstanceInfo
}

// NewRouter builds the vnproxd HTTP handler: the full middleware stack,
// /api/v1/* routes, and SPA-fallback static serving for everything else.
func NewRouter(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()

	r.Use(requestIDMiddleware)
	r.Use(requestLoggerMiddleware(logger))
	r.Use(recovererMiddleware(logger))
	r.Use(securityHeadersMiddleware)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler(opts.Version, opts.Collectors))
		if opts.Auth != nil {
			opts.Auth.MountRoutes(r)
			// GET /config: non-secret instance config for the Settings page,
			// gated behind a session + the same read capability every other
			// read route uses.
			r.Group(func(r chi.Router) {
				r.Use(opts.Auth.SessionMiddleware)
				r.Use(opts.Auth.RequireCap(capNetRead))
				r.Get("/config", configHandler(opts.Instance))
			})
		}
		mountTopologyRoutes(r, opts.Topology, opts.Auth, opts.Collectors, opts.Drift, opts.Findings, opts.Protected)
		mountLLDPRoutes(r, opts.LLDP, opts.Auth)
		mountDriftRoutes(r, opts.Drift, opts.Changesets, opts.Auth)
		mountFindingsRoutes(r, opts.Findings, opts.Changesets, opts.Auth)
		mountFDBRoutes(r, opts.FDB, opts.Auth)
		mountMetricsRoutes(r, opts.Metrics, opts.Auth)
		mountMetricsExporterRoutes(r, opts.MetricsCounters, opts.Findings, opts.Drift, opts.Changesets, opts.MetricsExporter)
		mountLayoutsRoutes(r, opts.Layouts, opts.Auth)
		mountAnnotationsRoutes(r, opts.Annotations, opts.Auth)
		mountAlertRulesRoutes(r, opts.AlertRules, opts.AlertDeliveries, opts.AlertSecretCipher, opts.Auth)
		mountChangesetsRoutes(r, opts.Changesets, opts.Auth, opts.PVEGateways, opts.Protected)
		mountSnapshotsRoutes(r, opts.Snapshots, opts.Auth, opts.PeerSnapshots)
		mountAuditRoutes(r, opts.Audit, opts.Auth, opts.PeerAudit)
		mountProtectedRoutes(r, opts.Protected, opts.Auth)
		mountSDNRoutes(r, opts.SDN, opts.Auth)
		mountIPAMRoutes(r, opts.IPAM, opts.Auth)
		mountEVPNRoutes(r, opts.EVPN, opts.Auth)
		mountDHCPRoutes(r, opts.DHCP, opts.Auth)
		mountFirewallRoutes(r, opts.Firewall, opts.Auth)
		mountBlueprintsRoutes(r, opts.Blueprints, opts.Changesets, opts.Auth)
		mountSimulateRoutes(r, opts.Simulator, opts.ProbeClients, opts.ProbeAudit, opts.SimDivergence, opts.Auth)
		mountFwLogRoutes(r, opts.FwLog, opts.Auth)
		mountDocExportRoutes(r, opts.DocExport, opts.Auth)
		mountLLDPInstallRoutes(r, opts.LLDPInstaller, opts.LLDPPeerInstaller, opts.LLDPAudit, opts.LocalNode, opts.Auth)
	})

	// /api/ws is intentionally not under /api/v1 (docs/api.md's WebSocket
	// section documents it at the bare /api/ws path).
	mountWSRoute(r, opts.Topology, opts.Auth)

	// /api/peer/* is likewise outside /api/v1 (docs/api.md's Peer API
	// section: "internal only", its own auth scheme) — mounted at the top
	// level, same as /api/ws, so it shares the request-id/logging/
	// recovery/security-headers middleware every route gets but none of
	// /api/v1's session-cookie machinery.
	if opts.Peer != nil {
		opts.Peer.MountRoutes(r)
	}

	// Unmatched /api/* routes get a JSON 404 (per docs/api.md's error
	// envelope), not the SPA fallback; everything else falls back to the
	// embedded SPA's index.html so client-side routing works on refresh.
	spa := newSPAHandler(opts.DistFS)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such API route")
			return
		}
		spa.ServeHTTP(w, req)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed on this route")
			return
		}
		spa.ServeHTTP(w, req)
	})

	return r
}

func isAPIPath(p string) bool {
	return len(p) >= 5 && p[:5] == "/api/"
}

// requestIDMiddleware assigns a request id (reusing an inbound
// X-Request-Id if the caller/proxy supplied one), stores it under chi's
// middleware.RequestIDKey so downstream code can use middleware.GetReqID,
// and — unlike chi's own RequestID middleware, which only populates the
// context — echoes it back as a response header so operators can correlate
// a client-visible id with structured logs.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(middleware.RequestIDHeader)
		if id == "" {
			id = fmt.Sprintf("vnproxd-%d-%d", time.Now().UnixNano(), middleware.NextRequestID())
		}
		w.Header().Set(middleware.RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
