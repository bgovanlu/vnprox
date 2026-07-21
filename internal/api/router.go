// Package api implements the HTTP router, handlers, and middleware stack
// for vnproxd: request id / structured logging / panic recovery / security
// headers, the /api/v1/health endpoint, and embedded-SPA serving with
// SPA-fallback routing. The WS hub and the rest of docs/api.md's routes
// land in later tasks (auth, changesets, topology, ...); this package only
// implements what T-002 requires.
package api

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/migration"
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
	SimDivergence         simDivergenceRecorder
	IngressTargets        IngressTargetStore
	Collectors            CollectorHealth
	Topology              TopologyService
	LLDP                  LLDPService
	Drift                 DriftService
	Findings              FindingsService
	FDB                   FDBService
	Layouts               LayoutStore
	Annotations           AnnotationStore
	AlertRules            AlertRuleStore
	AlertDeliveries       AlertDeliveryStore
	AlertSecretCipher     SecretCipher
	Federation            FederationService
	FederationAudit       federationAuditWriter
	FederationAgg         FederationAggregator
	Changesets            ChangesetService
	Snapshots             SnapshotService
	Audit                 AuditService
	HA                    HAStatusService
	DistFS                fs.FS
	HistoryFindingEvents  HistoryFindingEventsSource
	SDN                   SDNService
	SDNDNS                SDNDNSService
	IPAM                  IPAMService
	IPAMExternal          IPAMExternalService
	IPAMExternalAudit     ipamExternalAuditWriter
	FederationIPAM        FederationIPAMSource
	EdgeInterfaces        EdgeInterfacesSource
	EdgeGraph             EdgeGraph
	EdgeIPAM              EdgeIPAMSource
	ProbeAudit            simulateVerifyAuditor
	IngressSecretCipher   SecretCipher
	IngressDiscoverers    ingress.IngressDiscoverer
	EVPN                  EVPNService
	DHCP                  DHCPService
	IPv6                  IPv6Service
	Metrics               MetricsService
	MetricsCounters       MetricsCounterService
	MCP                   http.Handler
	PVEGateways           PVEGatewayProvider
	Protected             ProtectedService
	PBS                   PBSService
	Firewall              FirewallGraph
	ProbeClients          ProbeClientProvider
	TenantNotifier        ApprovalNotifier
	BlueprintTrust        BlueprintTrustStore
	BlueprintSignersAudit blueprintBundleAuditor
	Spec                  SpecInventory
	SpecPin               PinnedSpecStore
	SpecPinAudit          specPinAuditor
	Simulator             SimulatorGraph
	Blueprints            BlueprintService
	Auth                  AuthService
	History               HistoryAuditSource
	QosShapes             QosShapeSource
	Qos                   QosShapeListService
	GuestInteriorToggles  GuestInteriorToggleStore
	GuestInteriorGraph    GuestInteriorGraph
	GuestInteriorHost     GuestInteriorHostReader
	GuestInteriorPeers    PeerContainerSource
	GuestInteriorIPAM     GuestInteriorIPAMSource
	FwLog                 FwLogService
	Peer                  PeerServer
	PeerAudit             PeerAuditSource
	PeerSnapshots         PeerSnapshotSource
	Flows                 FlowLocalSource
	PeerFlows             PeerFlowSource
	LatMesh               LatMeshService
	MTUProbe              MTUProbeService
	Ceph                  CephService
	Failsim               FailsimService
	Microseg              MicrosegService
	WireGuard             WireGuardService
	WgCarriers            change.WgCarrierSource
	Wan                   WanService
	WanAudit              wanAuditor
	Captures              CaptureService
	Conntrack             ConntrackLocalSource
	PeerConntrack         PeerConntrackSource
	ConntrackGuests       ConntrackGuestResolver
	TenantStore           TenantAdminStore
	DocExport             DocExportService
	Capacity              CapacityService
	Posture               PostureService
	Plugins               PluginService
	// HubClient/HubVetting/PluginInstaller back T-1705's Blueprint & plugin
	// hub (GET /hub/index, POST /hub/install). HubClient nil skips mounting the
	// whole family; a blueprint install additionally needs Blueprints +
	// BlueprintTrust (reused above) and a plugin install needs PluginInstaller +
	// BlueprintTrust — a type whose backing dependency is absent returns 501.
	// HubVetting (the informational vetted-badge allowlist) is optional. Hub
	// installs reuse BlueprintSignersAudit for their audit trail.
	HubClient           HubClient
	HubVetting          HubVetting
	PluginInstaller     PluginInstaller
	LLDPInstaller       LocalLLDPInstaller
	LLDPPeerInstaller   PeerLLDPInstaller
	LLDPAudit           lldpInstallAuditor
	Tenant              TenantScoper
	Tokens              APITokenStore
	TokenAudit          tokenAuditor
	Webhooks            WebhookStore
	WebhookSecretCipher SecretCipher
	K8sClusters         K8sClusterStore
	K8sSecretCipher     SecretCipher
	K8sPoller           K8sPoller
	K8sGraph            K8sGraph
	K8sIPAM             K8sIPAMSource
	K8sAudit            k8sAuditWriter
	Migration           *migration.Planner
	LocalNode           func() string
	FlowClassifier      *flow.Classifier
	Logger              *slog.Logger
	Version             string
	MetricsExporter     MetricsExporterConfig
	BlueprintSigningKey ed25519.PrivateKey
	Instance            InstanceInfo
}

// DefaultMCPPath is the fixed mount path (under /api/v1) for the MCP transport
// (T-1701), kept here so the router and cmd/vnproxd agree without importing
// internal/config. Pinned equal to config.DefaultMCPPath by a config test.
const DefaultMCPPath = "/api/v1/mcp"

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

	// T-1703: the server-side tenant-scoping middleware, built once and shared
	// by every tenant-scoped read route. nil when multi-tenancy is disabled
	// (opts.Tenant nil) or when the auth backend can't resolve a username —
	// the read routes then mount unscoped, exactly as before.
	var scopeMW func(http.Handler) http.Handler
	// wsGuard fail-closes the /api/ws upgrade for tenant-scoped principals
	// (T-1703): the delta feed is cluster-wide and not yet per-subscriber
	// filtered, so a tenant member is refused rather than handed unscoped data.
	var wsGuard func(http.Handler) http.Handler
	if opts.Tenant != nil && opts.Auth != nil {
		if lookup, ok := opts.Auth.(UsernameLookup); ok {
			scopeMW = tenantScopeMiddleware(opts.Tenant, lookup)
			wsGuard = tenantWSGuard(opts.Tenant, lookup)
		}
	}

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
		mountTopologyRoutes(r, opts.Topology, opts.Auth, opts.Collectors, opts.Drift, opts.Findings, opts.Protected, opts.QosShapes, opts.PBS, scopeMW)
		mountPBSRoutes(r, opts.PBS, opts.Auth)
		mountLLDPRoutes(r, opts.LLDP, opts.Auth)
		mountDriftRoutes(r, opts.Drift, opts.Changesets, opts.Auth)
		mountFindingsRoutes(r, opts.Findings, opts.Changesets, opts.Auth, scopeMW)
		mountFDBRoutes(r, opts.FDB, opts.Auth)
		mountMetricsRoutes(r, opts.Metrics, opts.Auth)
		mountMetricsExporterRoutes(r, opts.MetricsCounters, opts.Findings, opts.Drift, opts.Changesets, opts.MetricsExporter)
		mountLayoutsRoutes(r, opts.Layouts, opts.Auth)
		mountAnnotationsRoutes(r, opts.Annotations, opts.Auth)
		mountAlertRulesRoutes(r, opts.AlertRules, opts.AlertDeliveries, opts.AlertSecretCipher, opts.Auth)
		mountFederationRoutes(r, opts.Federation, opts.FederationAudit, opts.Auth)
		mountFederationTopologyRoutes(r, opts.FederationAgg, opts.Auth)
		mountFederationIPAMRoutes(r, opts.FederationIPAM, opts.Auth)
		mountChangesetsRoutes(r, opts.Changesets, opts.Auth, opts.PVEGateways, opts.Protected, opts.WgCarriers, opts.Tenant, opts.TenantNotifier, opts.TenantStore)
		mountSnapshotsRoutes(r, opts.Snapshots, opts.Auth, opts.PeerSnapshots)
		mountAuditRoutes(r, opts.Audit, opts.Auth, opts.PeerAudit)
		mountHistoryRoutes(r, opts.History, opts.HistoryFindingEvents, opts.Auth)
		mountHARoutes(r, opts.HA, opts.Auth)
		mountProtectedRoutes(r, opts.Protected, opts.Auth)
		mountSDNRoutes(r, opts.SDN, opts.Auth)
		mountSDNDNSRoutes(r, opts.SDNDNS, opts.Auth)
		mountIPAMRoutes(r, opts.IPAM, opts.Auth, scopeMW)
		mountIPAMExternalRoutes(r, opts.IPAMExternal, opts.IPAMExternalAudit, opts.Auth)
		mountEdgeRoutes(r, opts.EdgeInterfaces, opts.SDN, opts.EdgeGraph, opts.EdgeIPAM, opts.Auth)
		mountIngressRoutes(r, opts.IngressTargets, opts.IngressSecretCipher, opts.IngressDiscoverers, opts.EdgeInterfaces, opts.EdgeGraph, opts.EdgeIPAM, opts.TokenAudit, opts.Auth)
		mountEVPNRoutes(r, opts.EVPN, opts.Auth)
		mountIPv6Routes(r, opts.IPv6, opts.Auth)
		mountDHCPRoutes(r, opts.DHCP, opts.Auth)
		mountFirewallRoutes(r, opts.Firewall, opts.Auth)
		mountBlueprintsRoutes(r, opts.Blueprints, opts.Changesets, opts.Auth)
		mountBlueprintBundleRoutes(r, opts.Blueprints, opts.BlueprintSigningKey, opts.BlueprintTrust, opts.BlueprintSignersAudit, opts.Auth)
		mountSpecRoutes(r, opts.Spec, opts.Changesets, opts.Auth)
		mountSpecPinRoutes(r, opts.SpecPin, opts.SpecPinAudit, opts.Auth)
		mountSimulateRoutes(r, opts.Simulator, opts.GuestInteriorIPAM, opts.ProbeClients, opts.ProbeAudit, opts.SimDivergence, opts.QosShapes, opts.Auth)
		mountQosRoutes(r, opts.Qos, opts.Auth)
		mountGuestInteriorRoutes(r, opts.GuestInteriorToggles, opts.GuestInteriorGraph, opts.ProbeClients, opts.GuestInteriorHost, opts.GuestInteriorPeers, opts.GuestInteriorIPAM, opts.LocalNode, opts.ProbeAudit, opts.Auth)
		mountFwLogRoutes(r, opts.FwLog, opts.Auth)
		mountLatMeshRoutes(r, opts.LatMesh, opts.Auth)
		mountMTUProbeRoutes(r, opts.MTUProbe, opts.Auth)
		mountCephRoutes(r, opts.Ceph, opts.Auth)
		mountFailsimRoutes(r, opts.Failsim, opts.Auth)
		mountMicrosegRoutes(r, opts.Microseg, opts.Auth)
		mountWireGuardRoutes(r, opts.WireGuard, opts.Auth)
		mountWanRoutes(r, opts.Wan, opts.Findings, opts.LocalNode, opts.WanAudit, opts.Auth)
		mountCaptureRoutes(r, opts.Captures, opts.Auth)
		mountConntrackRoutes(r, opts.Conntrack, opts.PeerConntrack, opts.ConntrackGuests, opts.LocalNode, opts.Auth)
		mountDiagnoseRoutes(r, opts, opts.Auth)
		mountFlowRoutes(r, opts.Flows, opts.Auth, opts.PeerFlows, opts.FlowClassifier, scopeMW)
		mountTenantRoutes(r, opts.TenantStore, opts.Tenant, opts.Changesets, opts.TenantNotifier, opts.Auth)
		mountDocExportRoutes(r, opts.DocExport, opts.Auth)
		mountCapacityRoutes(r, opts.Capacity, opts.Auth)
		mountPostureRoutes(r, opts.Posture, opts.Auth)
		mountPluginRoutes(r, opts.Plugins, opts.Auth)
		mountHubRoutes(r, opts.HubClient, opts.HubVetting, opts.Blueprints, opts.BlueprintTrust, opts.PluginInstaller, opts.BlueprintSignersAudit, opts.Auth)
		mountLLDPInstallRoutes(r, opts.LLDPInstaller, opts.LLDPPeerInstaller, opts.LLDPAudit, opts.LocalNode, opts.Auth)
		mountTokenRoutes(r, opts.Tokens, opts.TokenAudit, opts.Topology, opts.Auth)
		mountEmbedTokenRoute(r, opts.Tokens, opts.TokenAudit, opts.Auth)
		mountWebhookRoutes(r, opts.Webhooks, opts.WebhookSecretCipher, opts.TokenAudit, opts.Auth)
		mountK8sRoutes(r, opts.K8sClusters, opts.K8sSecretCipher, opts.K8sPoller, opts.K8sGraph, opts.K8sIPAM, opts.K8sAudit, opts.Auth)
		mountMigrationRoutes(r, opts.Migration, opts.Auth)
		// T-1701 MCP server: mounted raw (its own bearer auth, no session/CSRF)
		// at DefaultMCPPath — "/mcp" relative to this /api/v1 subrouter. Handles
		// POST (JSON-RPC) and GET (SSE). Nil unless [mcp] enabled=true.
		if opts.MCP != nil {
			r.Handle("/mcp", opts.MCP)
		}
	})

	// /api/ws is intentionally not under /api/v1 (docs/api.md's WebSocket
	// section documents it at the bare /api/ws path).
	mountWSRoute(r, opts.Topology, opts.Auth, wsGuard)

	// /api/peer/* is likewise outside /api/v1 (docs/api.md's Peer API
	// section: "internal only", its own auth scheme) — mounted at the top
	// level, same as /api/ws, so it shares the request-id/logging/
	// recovery/security-headers middleware every route gets but none of
	// /api/v1's session-cookie machinery.
	if opts.Peer != nil {
		opts.Peer.MountRoutes(r)
	}

	// /embed/* (T-1706): top-level, HTML-serving, read-only embed view
	// shells for wikis/NOC screens/status pages. Like /api/ws and /api/peer
	// above, these are outside /api/v1's session-cookie machinery — their
	// own embedViewAuth authenticates the token from the query string only,
	// never a session cookie (docs/security.md's embed-token model). Mounted
	// before the SPA NotFound fallback so a valid-token request serves the
	// shell while an invalid one gets a clean 401 rather than the SPA.
	mountEmbedViewRoutes(r, opts.Tokens, opts.DistFS, opts.Posture != nil)

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
