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
	// History/HistoryFindingEvents back T-1007's `GET /history/events`
	// (web/src/topology/history/HistoryTimeline.tsx's event-marker feed):
	// History is the same *store.AuditRepo Audit above wires in, narrowed
	// to the T-205 changeset-lifecycle action set; HistoryFindingEvents is
	// *store.FindingEventRepo (the finding_events table this task added).
	// Either alone is enough to mount the route; both nil skips it, same
	// degraded-mode treatment as every other optional Options field.
	History              HistoryAuditSource
	HistoryFindingEvents HistoryFindingEventsSource
	// SDN is T-401's read view seam (docs/api.md's `GET /sdn`); nil (no
	// PVE client — see cmd/vnproxd/collect.go's setupCollect doc comment)
	// simply skips mounting the route, the same degraded-mode treatment
	// every other optional Options field gets.
	SDN SDNService
	// IPAM is T-405's read view seam (docs/api.md's `GET /ipam/subnets` and
	// `GET /ipam/subnets/{cidr}/allocations`); nil-safe like SDN above.
	IPAM IPAMService
	// EdgeInterfaces/EdgeGraph/EdgeIPAM back T-1403's Edge & NAT cockpit
	// (docs/api.md's `GET /edge/routes`/`GET /edge/nat`). EdgeInterfaces is
	// typically the same *change.Service changeSvc wires in as Changesets
	// above — its ReadRawInterfaces is the identical per-node interfaces-
	// file read T-208's raw editor uses (docs/api.md's `GET /nodes/{node}/
	// interfaces/raw`), reused rather than duplicated; a dedicated field
	// (rather than reusing Changesets directly) keeps this route's
	// dependency to the one method it needs, testable without a full
	// ChangesetService fake. EdgeGraph is typically the same live
	// *inventory.Graph GuestInteriorGraph above already wires in (node
	// enumeration + guest status); EdgeIPAM is typically the same
	// *ipam.Service ipamSvc wires in elsewhere (port-forward -> guest IP
	// correlation), nil-safe — it only narrows the response, see
	// mountEdgeRoutes' doc comment.
	EdgeInterfaces EdgeInterfacesSource
	EdgeGraph      EdgeGraph
	EdgeIPAM       EdgeIPAMSource
	// EVPN is T-404's read view seam (docs/api.md's `GET /sdn/evpn/status`);
	// nil (no PVE/peer clients wired) simply skips mounting the route,
	// same degraded-mode treatment as SDN above.
	EVPN EVPNService
	// DHCP is T-406's read view seam (docs/api.md's `GET /sdn/dhcp`:
	// static reservations + live leases); nil-safe like SDN/EVPN above.
	DHCP DHCPService
	// IPv6 is T-1404's read view seam (docs/api.md's `GET /ipv6/segments`:
	// cluster-wide RA/SLAAC/DHCPv6 visibility); nil-safe like EVPN above.
	IPv6 IPv6Service
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
	// BlueprintSigningKey/BlueprintTrust/BlueprintSignersAudit back T-1107's
	// signed blueprint sharing bundles (docs/features/blueprints.md §5):
	// GET /blueprints/{id}/bundle, GET /blueprints/signing-key,
	// POST /blueprints/import, and GET/POST/DELETE /blueprint-signers.
	// BlueprintSigningKey is this daemon's own Ed25519 identity (generated
	// at first use, cmd/vnproxd); a zero-length key skips mounting
	// GET /blueprints/signing-key and produces only unsigned bundles from
	// GET /blueprints/{id}/bundle. BlueprintTrust is the admin-managed
	// trust store (nil skips mounting every /blueprint-signers route and
	// POST /blueprints/import — there would be nothing to check trust
	// against).
	BlueprintSigningKey   ed25519.PrivateKey
	BlueprintTrust        BlueprintTrustStore
	BlueprintSignersAudit blueprintBundleAuditor
	// Spec backs T-1101's `GET /spec` + `POST /spec/import` (the declarative
	// cluster network spec, internal/spec): the same live *inventory.Graph,
	// which satisfies SpecInventory's one-method Snapshot seam directly. Nil
	// simply omits the spec routes.
	Spec SpecInventory
	// SpecPin/SpecPinAudit back T-1102's `GET/POST/DELETE /spec/pin` (the
	// GitOps reconciler's pinned desired state) — typically the daemon's own
	// *store.PinnedSpecRepo (satisfies PinnedSpecStore directly) and
	// *store.AuditRepo, the same repo LLDPAudit/ProbeAudit below reuse for
	// their own one-method audit seam. Nil-safe like every other optional
	// Options field (SpecPin nil skips mounting the route family).
	SpecPin      PinnedSpecStore
	SpecPinAudit specPinAuditor
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
	// GuestInteriorToggles/GuestInteriorGraph/GuestInteriorHost/
	// GuestInteriorPeers/GuestInteriorIPAM back T-1304's guest network
	// interior inspector: GET/PUT /guests/{ref}/interior-toggle and
	// GET /guests/{ref}/interior. GuestInteriorToggles nil skips mounting
	// the whole family (matching Layouts/Annotations' degraded-mode
	// convention); GuestInteriorGraph is typically the same live
	// *inventory.Graph Simulator/Firewall above already wire in.
	// GuestInteriorHost (typically realHost, host.NewReal()) backs the lxc
	// host-side read path for a guest on this daemon's own node;
	// GuestInteriorPeers (typically *peer.Client) backs it for a guest on
	// a peer node — the qemu path needs neither (it reuses ProbeClients
	// above, since PVE's own REST API is already cluster-transparent).
	// GuestInteriorIPAM (typically the same *ipam.Service ipamSvc wires in
	// elsewhere) backs the IPAM cross-check annotation; nil simply omits
	// it. The interior read route reuses ProbeAudit above for its own
	// guest.interior_read audit row (same shared-seam pattern TokenAudit
	// already establishes across the Tokens/Webhooks route families).
	GuestInteriorToggles GuestInteriorToggleStore
	GuestInteriorGraph   GuestInteriorGraph
	GuestInteriorHost    GuestInteriorHostReader
	GuestInteriorPeers   PeerContainerSource
	GuestInteriorIPAM    GuestInteriorIPAMSource
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
	// Flows is T-1002's local-node read seam for GET /flows (nil skips
	// mounting the route, same degraded-mode treatment as every other
	// optional Options field); PeerFlows is its cluster fan-out dependency,
	// nil-safe like PeerAudit/PeerSnapshots above (falls back to
	// node-local-only).
	Flows     FlowLocalSource
	PeerFlows PeerFlowSource
	// LatMesh backs T-1303's GET /latmesh/heatmap and GET /latmesh/history
	// (docs/api.md's Latency mesh section); nil skips mounting both
	// routes, matching every other optional Options field. Typically the
	// daemon's own *latmesh.Service.
	LatMesh LatMeshService
	// MTUProbe backs T-1306's GET /mtuprobe/results (docs/api.md's Path MTU
	// prober section); nil skips mounting the route, matching every other
	// optional Options field. Typically the daemon's own *mtuprobe.Service.
	MTUProbe MTUProbeService
	// WireGuard backs T-1401's GET /wireguard/tunnels + /{id}/pubkey +
	// /{id}/peer-config (docs/api.md's WireGuard section); nil skips mounting
	// every /wireguard route. Read-only — WireGuard is mutated only through
	// the wg.* changeset op family, never a route here.
	WireGuard WireGuardService
	// WgCarriers supplies the changesets routes' touchesMgmtPath computation
	// with each existing WireGuard tunnel's stored carrier, so a wg op that
	// references a management-path tunnel WITHOUT the carrier in its params
	// (wg.peer.*, wg.tunnel.delete, carrier-less wg.tunnel.update) is still
	// flagged (Finding 2 / the mgmt-path interlock). Typically the same
	// WireGuard read service WireGuard is wired from. Nil degrades those ops
	// to params-only coverage (a create still names its own carrier).
	WgCarriers change.WgCarrierSource
	// Captures is T-1301's packet-capture coordinator seam (POST /captures,
	// POST /captures/{id}/stop, GET /captures/{id}, GET /captures). Nil skips
	// mounting every /captures route, same degraded-mode treatment as every
	// other optional Options field.
	Captures CaptureService
	// Conntrack is T-1305's local-node read seam for GET /conntrack (nil
	// skips mounting the route, same degraded-mode treatment as every other
	// optional Options field); PeerConntrack is its cluster fan-out
	// dependency (nil-safe, falls back to node-local-only); ConntrackGuests
	// resolves the `guest=` filter (nil-safe, that filter then matches
	// nothing rather than 500ing). Reuses LocalNode (T-605's own field,
	// below) to tag local entries with this node's own name.
	Conntrack       ConntrackLocalSource
	PeerConntrack   PeerConntrackSource
	ConntrackGuests ConntrackGuestResolver
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
	// Tokens/TokenAudit back T-1104's POST/GET/DELETE /tokens (automation
	// bearer tokens); Tokens nil skips mounting the whole family. Auth must
	// additionally implement TokenMinter (cmd/vnproxd's authServiceAdapter
	// does) or the routes still aren't mounted, matching UsernameLookup's
	// own precedent for layouts.
	Tokens     APITokenStore
	TokenAudit tokenAuditor
	// Webhooks/WebhookSecretCipher back T-1104's POST/GET/DELETE /webhooks
	// (automation event delivery targets); Webhooks nil skips mounting the
	// whole family. Reuses TokenAudit for its own audit entries (webhook.
	// create/webhook.delete) rather than a second audit-seam field.
	Webhooks            WebhookStore
	WebhookSecretCipher SecretCipher
	Logger              *slog.Logger
	Version             string
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
		mountChangesetsRoutes(r, opts.Changesets, opts.Auth, opts.PVEGateways, opts.Protected, opts.WgCarriers)
		mountSnapshotsRoutes(r, opts.Snapshots, opts.Auth, opts.PeerSnapshots)
		mountAuditRoutes(r, opts.Audit, opts.Auth, opts.PeerAudit)
		mountHistoryRoutes(r, opts.History, opts.HistoryFindingEvents, opts.Auth)
		mountProtectedRoutes(r, opts.Protected, opts.Auth)
		mountSDNRoutes(r, opts.SDN, opts.Auth)
		mountIPAMRoutes(r, opts.IPAM, opts.Auth)
		mountEdgeRoutes(r, opts.EdgeInterfaces, opts.SDN, opts.EdgeGraph, opts.EdgeIPAM, opts.Auth)
		mountEVPNRoutes(r, opts.EVPN, opts.Auth)
		mountIPv6Routes(r, opts.IPv6, opts.Auth)
		mountDHCPRoutes(r, opts.DHCP, opts.Auth)
		mountFirewallRoutes(r, opts.Firewall, opts.Auth)
		mountBlueprintsRoutes(r, opts.Blueprints, opts.Changesets, opts.Auth)
		mountBlueprintBundleRoutes(r, opts.Blueprints, opts.BlueprintSigningKey, opts.BlueprintTrust, opts.BlueprintSignersAudit, opts.Auth)
		mountSpecRoutes(r, opts.Spec, opts.Changesets, opts.Auth)
		mountSpecPinRoutes(r, opts.SpecPin, opts.SpecPinAudit, opts.Auth)
		mountSimulateRoutes(r, opts.Simulator, opts.GuestInteriorIPAM, opts.ProbeClients, opts.ProbeAudit, opts.SimDivergence, opts.Auth)
		mountGuestInteriorRoutes(r, opts.GuestInteriorToggles, opts.GuestInteriorGraph, opts.ProbeClients, opts.GuestInteriorHost, opts.GuestInteriorPeers, opts.GuestInteriorIPAM, opts.LocalNode, opts.ProbeAudit, opts.Auth)
		mountFwLogRoutes(r, opts.FwLog, opts.Auth)
		mountFlowRoutes(r, opts.Flows, opts.Auth, opts.PeerFlows)
		mountLatMeshRoutes(r, opts.LatMesh, opts.Auth)
		mountMTUProbeRoutes(r, opts.MTUProbe, opts.Auth)
		mountWireGuardRoutes(r, opts.WireGuard, opts.Auth)
		mountCaptureRoutes(r, opts.Captures, opts.Auth)
		mountConntrackRoutes(r, opts.Conntrack, opts.PeerConntrack, opts.ConntrackGuests, opts.LocalNode, opts.Auth)
		mountDiagnoseRoutes(r, opts, opts.Auth)
		mountDocExportRoutes(r, opts.DocExport, opts.Auth)
		mountLLDPInstallRoutes(r, opts.LLDPInstaller, opts.LLDPPeerInstaller, opts.LLDPAudit, opts.LocalNode, opts.Auth)
		mountTokenRoutes(r, opts.Tokens, opts.TokenAudit, opts.Topology, opts.Auth)
		mountWebhookRoutes(r, opts.Webhooks, opts.WebhookSecretCipher, opts.TokenAudit, opts.Auth)
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
