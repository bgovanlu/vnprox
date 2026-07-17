package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/dhcp"
	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/evpn"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/neighbor"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/sdn"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
	webui "github.com/bgovanlu/vnprox/web"
)

// certPollInterval is the mtime-polling fallback interval for TLS
// cert/key hot-reload (see config.CertProvider.Watch's doc comment for why
// polling instead of fsnotify).
const certPollInterval = 30 * time.Second

// peerSecretPollInterval is the mtime-polling interval for the cluster
// secret (peer.SecretStore.Watch's doc comment: same "changes on the order
// of a cluster's lifetime" rationale as certPollInterval).
const peerSecretPollInterval = 30 * time.Second

// metricPruneInterval is how often the metric_samples prune loop enforces
// store.MetricRetention (24h, docs/data-model.md §2). Hourly keeps the
// table within ~4% of the retention window at negligible cost.
const metricPruneInterval = time.Hour

// snapshotRetentionInterval is how often the snapshot retention job
// (T-206) enforces cfg.Retention's keep/pin-days policy. Snapshots accrue
// far more slowly than metric samples (one pre/post pair per apply, not a
// per-poll-interval row), so a coarser cadence than metricPruneInterval is
// plenty — this only needs to run at least once within any given day to
// keep the "keep N days" window accurate to within a day.
const snapshotRetentionInterval = 6 * time.Hour

// shutdownGrace bounds how long the HTTP server's graceful Shutdown may
// take; it is a safety net, not the expected duration — acceptance
// criterion 3 requires the whole process to exit within 3s of SIGTERM even
// with an in-flight slow request, so any real handler must finish well
// inside this.
const shutdownGrace = 10 * time.Second

// distRootFS returns the embedded frontend build rooted at the site root
// (stripping the "dist" prefix embed.FS retains). See web/assets.go for why
// the embed lives in its own tiny package.
func distRootFS() (fs.FS, error) {
	sub, err := fs.Sub(webui.DistFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("preparing embedded web/dist filesystem: %w", err)
	}
	return sub, nil
}

// runDaemon loads config, wires the HTTPS server + TLS cert watcher into a
// supervised run group, and blocks until ctx is cancelled (SIGINT/SIGTERM)
// or a subsystem fails.
func runDaemon(ctx context.Context, configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath, logger)
	if err != nil {
		return fmt.Errorf("loading config %s: %w", configPath, err)
	}

	certProvider, err := config.NewCertProvider(cfg.Server.TLSCertPath, cfg.Server.TLSKeyPath, logger)
	if err != nil {
		return fmt.Errorf("initializing TLS: %w", err)
	}

	// T-301: the cluster secret every /api/peer/* request is HMAC-verified
	// against (docs/architecture.md §5). Generated on first use if absent
	// (docs/deployment.md: "first node only; pmxcfs replicates it") —
	// fatal on any other load failure, the same treatment certProvider
	// above gets, since a daemon that can neither sign nor verify peer
	// requests cannot safely participate in cluster coordination.
	peerSecrets, err := peer.LoadOrGenerateSecret(cfg.Peer.SecretPath, logger)
	if err != nil {
		return fmt.Errorf("initializing peer cluster secret: %w", err)
	}

	distFS, err := distRootFS()
	if err != nil {
		return err
	}

	authSvc, db, sessionCipher, err := setupAuth(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing auth: %w", err)
	}
	defer func() { _ = db.Close() }()

	graph := inventory.NewGraph()
	topoSvc := topology.NewService(graph, logger)
	// T-305: the drift detector runs its own 30s cycle over the same live
	// graph the collectors populate, independent of any one poll loop
	// (docs/features/topology.md §6); its findings changing broadcasts
	// `drift.changed` over the same shared WS hub topoSvc's Broadcast
	// already backs for internal/change's `changeset.status` events.
	driftSvc := setupDrift(graph, topoSvc, logger)

	// T-601: the metrics sampler is constructed before setupCollect so its
	// Ingest method can be wired in as collect.Config.OnStats (the host
	// loop's per-tick counter hook) — it persists a 24h, 30s-downsampled
	// counter ring via the same metric_samples repo the hourly prune loop
	// below enforces retention on, and pushes docs/api.md's `metrics.sample`
	// WS event over topoSvc's shared hub, exactly like driftSvc's
	// `drift.changed` above and changeSvc's `changeset.status` below.
	metricSamples := store.NewMetricSampleRepo(db)
	metricsSampler := metrics.New(metrics.Config{
		Store:  metricSamples,
		WS:     topoSvc,
		Logger: logger,
	})

	// T-1001: the Prometheus scrape token GET /metrics is gated on
	// (docs/security.md's Authentication section) — loaded now, ahead of
	// findingsEngine/driftSvc/changeSvc below, purely so it can sit next to
	// metricsSampler's own construction; it isn't consumed until
	// api.NewRouter is called far below. A nil/empty token (cfg.Metrics.Enabled
	// == false) means api.Options.MetricsExporter simply never mounts
	// GET /metrics.
	metricsToken, err := setupMetricsExporterToken(cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing metrics exporter: %w", err)
	}

	// T-602: the unified findings engine's IngestServices is wired in as
	// collect.Config.OnServices below (the same "piggyback on the host
	// loop's existing per-tick hook" pattern OnStats already established
	// for metricsSampler) — but the engine itself needs graph/driftSvc/
	// topoSvc/metricsSampler/a notifier, none of which exist until after
	// setupCollect returns the PVE client the notifier reuses, so
	// findingsEngine is constructed just below setupCollect and its
	// IngestServices method is closed over here for setupCollect's benefit.
	var findingsEngine *findings.Engine
	onServices := func(node string, status map[string]bool) {
		if findingsEngine != nil {
			findingsEngine.IngestServices(node, status)
		}
	}

	// T-303: peerClient (built from the same PVE client the collectors use
	// for cluster-status-based discovery) is nil exactly when collectErr is
	// non-nil — see setupCollect's doc comment — so every peerClient use
	// below is already guarded by the same "collectors initialized OK"
	// nil-safety collector's own uses need.
	collector, peerClient, sdnPVEClient, collectErr := setupCollect(cfg, graph, logger, topoSvc.OnDelta, metricsSampler.Ingest, onServices, peerSecrets)
	if collectErr != nil {
		logger.Error("collect: failed to initialize PVE/host collectors; starting without live inventory polling or cluster fan-out", "error", collectErr)
	}
	// T-301/T-304/T-406: the peer server (below, peerSrv) and the local
	// node's own T-406 DHCP-lease read both need a host.Reader over real
	// netlink/interfaces(5)/lldpd/dnsmasq state — host.NewReal() has no
	// dependencies of its own, so it's built here (rather than down by
	// peerSrv, where it used to live) so dhcpSvc below can use the same
	// instance too.
	realHost := host.NewReal()
	// localNode is a closure over collector (T-303's own doc comment
	// explains why: collector may not have completed a poll cycle yet at
	// startup) — built here, ahead of dhcpSvc/ipamSvc below, so both can
	// close over the same func value; clusterNodes/clusterTimers further
	// down reuse this identical variable.
	localNode := func() string {
		if collector == nil {
			return ""
		}
		return collector.Status().LocalNode
	}
	// T-401: GET /sdn reads PVE directly and live (internal/sdn.Service's
	// doc comment) via the same read-only client the collectors use — nil
	// exactly when collectErr is non-nil, mirroring peerClient's own
	// nil-safety above.
	var sdnSvc *sdn.Service
	if sdnPVEClient != nil {
		sdnSvc = sdn.NewService(sdnPVEClient)
	}
	// T-406: internal/dhcp.Service fans DHCP-lease reads across the
	// cluster (local node via realHost, every peer via peerClient) into
	// ipam.Observation values — always constructed (mirrors evpnSvc's own
	// unconditional construction below: it tolerates a nil Peers/empty
	// LocalNode internally, so there's no typed-nil risk assigning it
	// directly into ipam.Config.Leases, an interface field).
	var dhcpPeers dhcp.PeerSource
	if peerClient != nil {
		dhcpPeers = peerClient
	}
	dhcpSvc := dhcp.NewService(dhcp.Config{Host: realHost, Peers: dhcpPeers, LocalNode: localNode, Logger: logger})

	// T-805: internal/neighbor.Service fans ARP/IPv6-neighbor reads across
	// the cluster (local node via realHost, every peer via peerClient) into
	// ipam.Observation{Source: "neighbor"} values — the same
	// always-construct, tolerate-nil-Peers shape dhcpSvc above uses, wired
	// into ipam.Config.Neighbors below (the interface point T-405 reserved
	// for it, docs/features/ipam.md §1's "known gap" this task closes).
	var neighborPeers neighbor.PeerSource
	if peerClient != nil {
		neighborPeers = peerClient
	}
	neighborSvc := neighbor.NewService(neighbor.Config{Host: realHost, Peers: neighborPeers, LocalNode: localNode, Logger: logger})

	// T-405/T-406: GET /ipam/subnets(/{cidr}/allocations) and GET /sdn/dhcp
	// read PVE's IPAM plugin(s) directly and live, for the same "never
	// stale relative to what a reserve/release apply just changed" reason
	// sdnSvc above does — plus the cluster's own inventory graph
	// (guest/bridge data for the guest-agent enrichment source and
	// detected non-SDN subnets) and dhcpSvc above (T-406's lease
	// enrichment source, wired into exactly the interface point T-405 left
	// open for it — ipam.Config.Leases).
	//
	// ipamConcrete is kept as a second, concrete-typed handle (alongside
	// the interface-typed ipamSvc below) purely so cmd/vnproxd's own
	// dhcpAllocationsAdapter (changeagent.go) can call its exported
	// AllAllocations method for T-406's DHCP-range-overlap advisory check
	// — ipamSvc itself (declared as the api.IPAMService interface, not a
	// concrete *ipam.Service) only ever assigned inside the
	// sdnPVEClient != nil branch, so an unset case is a true nil
	// interface — the safe pattern peerAudit/peerSnapshots below already
	// use, avoiding the "non-nil interface wrapping a typed nil pointer"
	// footgun a bare `var ipamSvc *ipam.Service` would risk at
	// Options{IPAM: ipamSvc} (mountIPAMRoutes' `svc == nil` degraded-mode
	// check needs a literal nil interface to work).
	var ipamConcrete *ipam.Service
	var ipamSvc api.IPAMService
	// dhcpAPISvc is the same concrete *ipam.Service value as ipamSvc, typed
	// as api.DHCPService (docs/api.md's GET /sdn/dhcp seam) — api.IPAMService
	// doesn't declare the DHCP method, so ipamSvc's own interface-typed
	// variable can't be assigned directly to a DHCPService-typed field; a
	// second interface-typed variable, assigned from the same nil check, is
	// the same "true nil interface until assigned" pattern as ipamSvc
	// itself.
	var dhcpAPISvc api.DHCPService
	if sdnPVEClient != nil {
		ipamConcrete = ipam.NewService(ipam.Config{PVE: sdnPVEClient, Inventory: graph, Leases: dhcpSvc, Neighbors: neighborSvc})
		ipamSvc = ipamConcrete
		dhcpAPISvc = ipamConcrete
	}
	// changeAllocations adapts ipamConcrete into change.AllocationsSource
	// for T-406's DHCP-range-overlap advisory check — see
	// dhcpAllocationsAdapter's doc comment in changeagent.go for why
	// internal/change never imports internal/ipam directly. Same
	// true-nil-interface-until-assigned pattern as ipamSvc above.
	var changeAllocations change.AllocationsSource
	if ipamConcrete != nil {
		changeAllocations = dhcpAllocationsAdapter{ipam: ipamConcrete}
	}

	// T-602: one findings stream unifying drift (T-305), the LLDP VLAN
	// cross-check (T-302), and this task's own health checks — composed
	// over the same live graph/metrics substrate every other read path
	// shares (docs/architecture.md §2/§3). The notifier reuses sdnPVEClient
	// (the collectors' read-only PVE identity) rather than building a
	// third client.
	//
	// T-702: mgmtStatusAdapter is wired in now (findings.Engine is
	// constructed before change.Service exists, below) and filled in with
	// its real target once changeSvc is built — see the adapter's own doc
	// comment (findings.go) for why.
	mgmtAdapter := &mgmtStatusAdapter{}
	// T-803: corosyncStatusAdapter reuses realHost/localNode (already built
	// above for dhcpSvc/neighborSvc) — local-node-only for now, see its own
	// doc comment (findings.go) for the documented cluster-fan-out gap.
	corosyncAdapter := corosyncStatusAdapter{host: realHost, localNode: localNode, logger: logger}
	// T-1006: fwAnalyticsAdapter is wired in now (findings.Engine is
	// constructed before *fwlog.Service exists, below) and filled in with
	// its real target once fwlogSvc is built — see the adapter's own doc
	// comment (findings.go) for why, mirroring mgmtAdapter above.
	fwAnalyticsAdapterVal := &fwAnalyticsAdapter{}
	// T-1005: alert_rules/alert_deliveries repos + the webhook Notifier,
	// composed alongside PVE's own notification-target hook via
	// multiNotifier — independent delivery paths, per that task's card, not
	// a replacement for pvenotify.go. AlertSecretCipher is the same
	// session-secret cipher sessions.pve_ticket_enc uses (setupAuth's doc
	// comment).
	alertRuleRepo := store.NewAlertRuleRepo(db)
	alertDeliveryRepo := store.NewAlertDeliveryRepo(db)
	webhookNotifier := setupAlertWebhookNotifier(alertRuleRepo, alertDeliveryRepo, sessionCipher, logger)
	findingsNotifier := newMultiNotifier(setupFindingsNotifier(sdnPVEClient, logger), webhookNotifier)
	// T-806: the persisted sim_divergence finding store, created here (db
	// is available; findingsEngine is built next) and reused verbatim as
	// the router's api.Options.SimDivergence write-side seam below.
	simDivergenceRepo := store.NewSimDivergenceRepo(db)
	findingsEngine = setupFindings(graph, driftSvc, topoSvc, metricsSampler, mgmtAdapter, corosyncAdapter, fwAnalyticsAdapterVal, findingsNotifier, topoSvc, ipamConcrete, simDivergenceRepo, logger)

	// T-605: the config documentation export (docs/features/blueprints.md
	// §4) reads the exact same live sources the rest of this file's read
	// routes already do — the shared inventory graph, sdnSvc (nil-safe,
	// mirroring GET /sdn's own degraded-mode treatment above), and topoSvc
	// for both the topology/SVG section and the LLDP ports table.
	var docExportSDN docexport.SDNSource
	if sdnSvc != nil {
		docExportSDN = sdnSvc
	}
	docExportSvc := &docexport.Service{
		Inventory: graph,
		SDN:       docExportSDN,
		Ports:     topoSvc,
		Topo:      topoSvc,
	}

	// changeSvc reuses topoSvc's WS hub for changeset.status broadcasts
	// (docs/api.md's WebSocket section documents one shared /api/ws
	// connection multiplexing "topology"/"changesets"/... topics alike —
	// see topology.Service.Broadcast and internal/change.Broadcaster), and
	// validates against the same live *inventory.Graph collect populates
	// (T-202: Service.Validate/Create/UpdateDraft snapshot it read-only —
	// this package never polls or mutates inventory itself).
	// The apply engine (T-205): the host writer for interfaces(5) files, the
	// pre/post snapshot store, and the collector as the post-terminal
	// inventory refresher. Refresher is nil-safe (collector may be nil).
	var refresher change.InventoryRefresher
	if collector != nil {
		refresher = collector
	}
	// The host writer: the real /etc/network/interfaces agent by default, or
	// — when [safety] dev_interfaces_dir is set (dev.toml does; production
	// configs never do) — a sandboxed agent that can only touch files under
	// that directory and never execs a real ifreload (audit-phase-2 F-22:
	// `make dev` used to wire the production agent, leaving the developer's
	// machine one authenticated POST away from a real ifreload).
	//
	// nodeAgent's static type is the concrete *hostNodeAgent (not the
	// change.NodeAgent interface) so the same instance also backs T-301's
	// peer.HostWriter seam below: they mutate the very same
	// /etc/network/interfaces(.new) files, so they must share its
	// instance-level mutex, not each hold their own — two separate
	// instances would let a peer-API-triggered write and a local changeset
	// apply race on disk with no mutual exclusion at all.
	nodeAgent := newHostNodeAgent(logger)
	if dir := cfg.Safety.DevInterfacesDir; dir != "" {
		devAgent, devErr := newDevNodeAgent(dir, logger)
		if devErr != nil {
			return fmt.Errorf("initializing dev interfaces sandbox: %w", devErr)
		}
		logger.Warn("change: DEV MODE host writer — interfaces file operations are sandboxed and ifreload is a no-op", "dir", dir)
		nodeAgent = devAgent
	}
	snapshotRepo := store.NewSnapshotRepo(db)
	blobRepo := store.NewBlobRepo(db)
	auditRepo := store.NewAuditRepo(db)

	// T-304: the local-timer protocol's node-side agent — every daemon runs
	// one, independent of whether it ends up coordinating anything, so it
	// can answer a coordinator's arm/cancel/status calls for its own node
	// (peer.ServerOptions.Timers below) as well as serve this node's own
	// Apply calls directly (ClusterTimerAgent's "local" branch, no HTTP
	// round trip to itself).
	nodeTimerRepo := store.NewNodeTimerRepo(db)
	localTimers := change.NewLocalTimerAgent(change.LocalTimerConfig{
		Nodes:  nodeAgent,
		Repo:   nodeTimerRepo,
		Logger: logger,
	})
	if armErr := localTimers.ArmPendingOnStartup(ctx); armErr != nil {
		logger.Error("change: re-arming pending local rollback timers on startup", "error", armErr)
	}
	defer localTimers.StopTimers()

	// The coordinator side of T-304: a dedicated peer client (cluster
	// secret + PVE-cluster-status discovery) and the local-vs-peer routing
	// agents that let internal/change treat every node — this one or a
	// peer's — uniformly. discoverPVEClient failing is non-fatal (mirrors
	// setupCollect's own tolerance below): a daemon that can't yet reach PVE
	// for cluster-status coordinates only its own node, exactly the
	// documented single-node "zero peers" case.
	// clusterStatusSource is left a nil interface (not a non-nil interface
	// wrapping a nil *pve.Client — a classic Go footgun) on discovery-client
	// construction failure: peer.Client.Peers documents a nil
	// ClusterStatusSource as "the documented single-node zero-peers case",
	// exactly the degraded-but-safe behavior wanted here.
	//
	// coordPeerClient is deliberately a second *peer.Client, independent of
	// T-303's peerClient above (also secret+cluster-status-driven, but tied
	// to the collectors' own PVE client and its failure mode): T-304's
	// coordination path must keep working (in degraded, local-node-only
	// form) even when the collectors' PVE client has failed to build,
	// whereas T-303's peerClient is intentionally nil in exactly that case.
	// Unifying these into one shared client is a reasonable future cleanup
	// (T-305/T-306 or a hardening pass) but not attempted here — see
	// planning/reports/T-303.md and T-304.md, developed concurrently and
	// integrated by hand.
	var clusterStatusSource peer.ClusterStatusSource
	if discoveryClient, discErr := buildCollectorPVEClient(cfg); discErr != nil {
		logger.Warn("change: building peer-discovery PVE client; multi-node coordination unavailable until this succeeds", "error", discErr)
	} else {
		clusterStatusSource = discoveryClient
	}
	coordPeerClient := peer.NewClient(peer.ClientOptions{
		ClusterStatus: clusterStatusSource,
		Secrets:       peerSecrets,
		Logger:        logger,
	})
	// localNode is the same closure already built up above (before
	// dhcpSvc/ipamSvc) — reused here, not redeclared, so every one of its
	// callers throughout this function shares one variable.
	peerLocator := change.NewDiscoveringPeerLocator(coordPeerClient)
	clusterNodes := change.NewClusterNodeAgent(localNode, nodeAgent, coordPeerClient, peerLocator)
	clusterTimers := change.NewClusterTimerAgent(localNode, localTimers, coordPeerClient, peerLocator)

	changeSvc, err := change.NewService(change.Config{
		Changesets:        store.NewChangesetRepo(db),
		Audit:             auditRepo,
		WS:                topoSvc,
		Inventory:         graph,
		Allocations:       changeAllocations,
		Logger:            logger,
		ProtectedPath:     cfg.Safety.ProtectedPath,
		AllowDangerousOps: cfg.Safety.AllowDangerousOps,
		Nodes:             clusterNodes,
		Timers:            clusterTimers,
		Snapshots:         snapshotRepo,
		Blobs:             blobRepo,
		Refresher:         refresher,
		ConfirmTimeout:    time.Duration(cfg.Server.ConfirmTimeoutDefault) * time.Second,
		// The manual-rollback window tracks the snapshot-retention pin so a
		// still-offered rollback always has its pre-apply snapshot (audit
		// phase-2 F-10).
		RollbackWindowDays: cfg.Retention.SnapshotPinDays,
	})
	if err != nil {
		return fmt.Errorf("initializing change engine: %w", err)
	}
	// T-702: point the findings engine's mgmt_single_path check at the now-
	// real change.Service (see mgmtAdapter's construction/doc comment above).
	mgmtAdapter.set(changeSvc)
	// Re-arm commit-confirm rollback timers persisted across a restart, and
	// recover any apply interrupted by a crash (docs/development.md: "Rollback
	// timers must survive daemon restart ... re-armed on startup").
	if armErr := changeSvc.ArmPendingRollbacks(ctx); armErr != nil {
		logger.Error("change: re-arming pending rollbacks on startup", "error", armErr)
	}
	defer changeSvc.StopTimers()

	// T-505: the firewall log viewer's cluster-wide tailer/correlator.
	// Built before peerSrv below so the same local log source
	// (fwlogSource) backs both this daemon's own polling (fwlogSvc) and
	// what it serves to peers over GET /api/peer/firewall/log
	// (fwLogPeerReaderAdapter) — one source of truth for "this node's own
	// log", not two independently-opened readers of the same file.
	// coordPeerClient (constructed above for T-304's coordinator) is
	// reused for fan-out rather than building a third peer.Client — it
	// already carries the same cluster-secret/discovery wiring T-303's
	// peerClient does.
	fwlogSvc, fwlogSource, fwlogErr := setupFwlog(cfg, graph, topoSvc, coordPeerClient, localNode, logger)
	if fwlogErr != nil {
		logger.Error("fwlog: failed to initialize the firewall log viewer's local source; the feature will report empty/unavailable", "error", fwlogErr)
	}
	// T-1006: point the findings engine's fw_rule_unused check at the now-
	// real *fwlog.Service (see fwAnalyticsAdapterVal's construction/doc
	// comment above). Safe even when fwlogSvc is nil (setupFwlog's
	// dev-fixture failure path) — fwAnalyticsAdapter.Analytics degrades to
	// an empty Analytics value exactly like a never-set adapter.
	fwAnalyticsAdapterVal.set(fwlogSvc)

	// T-301/T-304: the peer server backs the documented /api/peer/host/* and
	// /api/peer/timer/* routes with the real netlink/interfaces(5)/lldpd
	// reader (realHost, built earlier above) for reads and the same
	// nodeAgent/localTimers constructed above for writes.
	//
	// T-302: the same host.Real also backs the guided-install route
	// (POST /api/peer/host/lldp/install, docs/features/lldp-discovery.md
	// §1) via its InstallLLDPD method (host/lldp_install_linux.go).
	var fwLogReader peer.FirewallLogReader
	if fwlogSource != nil {
		fwLogReader = fwLogPeerReaderAdapter{src: fwlogSource}
	}
	peerSrv := peer.NewServer(peer.ServerOptions{
		Secrets:       peerSecrets,
		Reader:        realHost,
		Writer:        nodeAgent,
		Audit:         auditPeerAdapter{auditRepo},
		Snapshots:     snapshotPeerAdapter{changeSvc},
		Timers:        localTimers,
		LLDPInstaller: realHost,
		FirewallLog:   fwLogReader,
		Version:       version,
		Logger:        logger,
	})

	// T-303: peerClient also backs GET /audit and GET /snapshots' cluster
	// fan-out (docs/architecture.md §7) — nil-safe, same as Peer above:
	// when it's nil (no PVE client, or a genuinely peerless single-node
	// deployment reporting zero peers) both routes stay exactly as
	// node-local as they were before T-303.
	var peerAudit api.PeerAuditSource
	var peerSnapshots api.PeerSnapshotSource
	var lldpPeerInstaller api.PeerLLDPInstaller
	if peerClient != nil {
		peerAudit = peerClient
		peerSnapshots = peerClient
		lldpPeerInstaller = peerClient
	}

	// T-404: GET /sdn/evpn/status fans FRR/BGP state across the cluster
	// via the same realHost reader (local node) and peerClient (peers)
	// every other node-local observability route above already uses;
	// sdnSvc (may be nil) backs exit-node health. evpnSDN/evpnPeers are
	// built as typed-nil-safe interface values (not a bare `sdnSvc`/
	// `peerClient` assignment) for the same "non-nil interface wrapping a
	// nil concrete pointer" footgun clusterStatusSource's own comment
	// above already calls out — a nil *sdn.Service assigned directly to
	// an evpn.SDNZoneSource field would make evpn.Service's own nil check
	// pass and then panic calling Tree() on a nil receiver.
	var evpnPeers evpn.PeerSource
	if peerClient != nil {
		evpnPeers = peerClient
	}
	var evpnSDN evpn.SDNZoneSource
	if sdnSvc != nil {
		evpnSDN = sdnSvc
	}
	evpnSvc := evpn.NewService(evpn.Config{
		Host:      realHost,
		Peers:     evpnPeers,
		LocalNode: localNode,
		SDN:       evpnSDN,
	})

	// T-603: blueprints diff/instantiate against the same live inventory
	// graph every other read path (topology, drift, sim) shares — never a
	// separate copy (docs/architecture.md §2/§3).
	blueprintSvc := blueprint.New(blueprint.Config{
		Repo:      store.NewBlueprintRepo(db),
		Inventory: graph,
	})

	// fwlogSvc is a *fwlog.Service, possibly nil (setupFwlog's dev-fixture
	// load failure path) — assigned through an explicit nil check rather
	// than handed to api.Options.FwLog directly, so a nil *fwlog.Service
	// never becomes a non-nil FwLogService interface value wrapping a nil
	// pointer (the classic Go footgun peer.Client.Peers' own doc comment
	// calls out; mountFwLogRoutes' `if svc == nil` guard only works
	// against a truly nil interface).
	var fwLogAPI api.FwLogService
	if fwlogSvc != nil {
		fwLogAPI = fwlogSvc
	}

	handler := api.NewRouter(api.Options{
		Version: version,
		// Non-secret operational config for the Settings page's Instance
		// section (GET /config). Deliberately excludes every secret/token/
		// key/password — see api.InstanceInfo.
		Instance: api.InstanceInfo{
			Version:                  version,
			Listen:                   cfg.Server.Listen,
			PVEAPIURL:                cfg.PVE.APIURL,
			ProtectedPath:            cfg.Safety.ProtectedPath,
			PVEInterval:              cfg.Collect.PVEInterval.String(),
			HostInterval:             cfg.Collect.HostInterval.String(),
			LLDPInterval:             cfg.Collect.LLDPInterval.String(),
			ConfirmTimeoutDefaultSec: cfg.Server.ConfirmTimeoutDefault,
			SnapshotKeepDays:         cfg.Retention.SnapshotKeepDays,
			SnapshotPinDays:          cfg.Retention.SnapshotPinDays,
			ReadOnly:                 cfg.Server.ReadOnly,
			AllowDangerousOps:        cfg.Safety.AllowDangerousOps,
			MetricsEnabled:           cfg.Metrics.Enabled,
		},
		DistFS:     distFS,
		Logger:     logger,
		Auth:       authServiceAdapter{authSvc},
		Collectors: collectorHealthAdapter{collector},
		Topology:   topoSvc,
		LLDP:       topoSvc,
		Drift:      driftSvc,
		Findings:   findingsEngine,
		FDB:        topoSvc,
		Metrics:    metricsSampler,
		// T-1001: metricsSampler also satisfies MetricsCounterService
		// (AllCounters) — same underlying object as Metrics above, wired
		// through the dedicated exporter seam GET /metrics uses.
		MetricsCounters: metricsSampler,
		MetricsExporter: api.MetricsExporterConfig{
			Token:        metricsToken,
			AllowFrom:    cfg.Metrics.AllowFrom,
			BuildVersion: version,
		},
		Layouts:           store.NewLayoutRepo(db),
		Annotations:       store.NewAnnotationRepo(db),
		AlertRules:        alertRuleRepo,
		AlertDeliveries:   alertDeliveryRepo,
		AlertSecretCipher: sessionCipher,
		Changesets:        changeSvc,
		Snapshots:         changeSvc,
		Audit:             auditRepo,
		SDN:               sdnSvc,
		IPAM:              ipamSvc,
		EVPN:              evpnSvc,
		DHCP:              dhcpAPISvc,
		PVEGateways:       pveGatewayProvider{authSvc},
		Protected:         changeSvc,
		Firewall:          graph,
		Blueprints:        blueprintSvc,
		Simulator:         graph,
		ProbeClients:      probeClientProvider{authSvc},
		ProbeAudit:        auditRepo,
		SimDivergence:     simDivergenceRepo,
		FwLog:             fwLogAPI,
		Peer:              peerSrv,
		PeerAudit:         peerAudit,
		PeerSnapshots:     peerSnapshots,
		// T-605: config documentation export (Tools -> Export documentation)
		// and the onboarding walkthrough's "LLDP offer" step's guided
		// install, both additive to docs/api.md's original contract (see
		// docexport.go/lldpinstall.go's doc comments).
		DocExport:         docExportSvc,
		LLDPInstaller:     realHost,
		LLDPPeerInstaller: lldpPeerInstaller,
		LLDPAudit:         auditRepo,
		LocalNode:         localNode,
	})

	srv := &http.Server{
		Addr:    cfg.Server.Listen,
		Handler: handler,
		TLSConfig: &tls.Config{
			GetCertificate: certProvider.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	var g runGroup
	g.add(func(ctx context.Context) error {
		return certProvider.Watch(ctx, sighup, certPollInterval)
	})
	// Picks up a cluster secret rotated on disk — most notably the very
	// first node's generated secret propagating to every other node via
	// pmxcfs (docs/deployment.md: "first node only; pmxcfs replicates
	// it") — without requiring a daemon restart.
	g.add(func(ctx context.Context) error {
		return peerSecrets.Watch(ctx, peerSecretPollInterval)
	})
	g.add(func(ctx context.Context) error {
		return serveHTTPS(ctx, srv, nil, logger)
	})
	if pprofActor := maybeStartDebugPprof(logger); pprofActor != nil {
		g.add(pprofActor)
	}
	g.add(authSvc.RunRenewalLoop)
	// metric_samples retention (store.MetricRetention): RunPruneLoop's doc
	// comment assigns the wiring to the daemon, and without it the table
	// grows unboundedly once metrics flow (audit phase-0 F-01). Reuses the
	// same *store.MetricSampleRepo instance T-601's metricsSampler above
	// writes through, rather than a second repo over the same table.
	g.add(func(ctx context.Context) error {
		return metricSamples.RunPruneLoop(ctx, metricPruneInterval, func(err error) {
			logger.Error("store: metric_samples prune failed", "error", err)
		})
	})
	// Snapshot retention (T-206, docs/features/change-management.md §4):
	// keep cfg.Retention.SnapshotKeepDays of history, floored at
	// SnapshotPinDays for any snapshot linked to a committed changeset (the
	// manual-rollback window), then reclaim orphaned blob storage.
	g.add(func(ctx context.Context) error {
		return store.RunSnapshotRetentionLoop(ctx, snapshotRepo, blobRepo, snapshotRetentionInterval,
			cfg.Retention.SnapshotKeepDays, cfg.Retention.SnapshotPinDays, func(err error) {
				logger.Error("store: snapshot retention failed", "error", err)
			})
	})
	if collector != nil {
		g.add(collector.RunPVELoop)
		g.add(collector.RunHostLoop)
		g.add(collector.RunLLDPLoop)
	}
	g.add(driftSvc.RunLoop)
	if fwlogSvc != nil {
		// T-505: continuously merges the local + every peer's pve-firewall
		// log into the shared, rate-capped buffer GET /firewall/log and the
		// `firewall.log.batch` WS push both read from (see
		// internal/fwlog.Service.Run's doc comment). Runs unconditionally
		// once initialized, the same "always on, not gated behind a
		// subscriber" treatment driftSvc.RunLoop above gets.
		g.add(fwlogSvc.Run)
	}
	g.add(findingsEngine.RunLoop)

	logger.Info("vnproxd starting",
		"version", version,
		"listen", cfg.Server.Listen,
		"tls_cert", cfg.Server.TLSCertPath,
		"tls_key", cfg.Server.TLSKeyPath,
		"read_only", cfg.Server.ReadOnly,
	)

	err = g.run(ctx)
	logger.Info("vnproxd stopped")
	return err
}

// serveHTTPS runs srv until ctx is cancelled, at which point it drains
// in-flight requests via a bounded graceful Shutdown. It returns nil on a
// clean shutdown (including the expected http.ErrServerClosed from
// Shutdown) or a wrapped error otherwise.
//
// ln is normally nil, in which case srv.Addr is bound the usual way
// (ListenAndServeTLS); tests pass an already-bound listener (so they can
// learn the ephemeral port before serving starts) via srv.ServeTLS instead.
func serveHTTPS(ctx context.Context, srv *http.Server, ln net.Listener, logger *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() {
		// cert/key args are empty: TLSConfig.GetCertificate/Certificates
		// supplies the keypair, which is what makes hot-reload possible.
		if ln != nil {
			serveErr <- srv.ServeTLS(ln, "", "")
		} else {
			serveErr <- srv.ListenAndServeTLS("", "")
		}
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("https server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down: draining in-flight requests")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("https server shutdown: %w", err)
		}
		<-serveErr // let the ListenAndServeTLS goroutine finish, don't leak it
		return nil
	}
}
