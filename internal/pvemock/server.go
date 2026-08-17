// Package pvemock implements a mock Proxmox VE API server and the YAML
// cluster fixtures that drive it. It exists so every later vnprox task
// (the PVE client, host readers, the change engine, SDN/firewall UI, ...)
// can develop and test against a faithful imitation of the PVE API surface
// without needing real Proxmox hardware. See README.md for a curl
// walkthrough and testdata/clusters/*.yaml for the fixtures.
package pvemock

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Privilege names the mock understands. These mirror the subset of real PVE
// privileges vnprox itself maps to UI capability flags (see
// docs/architecture.md §6).
const (
	PrivSysAudit    = "Sys.Audit"
	PrivSysModify   = "Sys.Modify"
	PrivVMAudit     = "VM.Audit"
	PrivVMConfigNet = "VM.Config.Network"
	PrivSDNAudit    = "SDN.Audit"
	PrivSDNAllocate = "SDN.Allocate"
)

// Server is a mock PVE API HTTP server backed by fixture-derived State.
type Server struct {
	state  *State
	router chi.Router
	log    *slog.Logger
}

// NewServer builds a Server from an already-loaded, validated Fixture.
func NewServer(f *Fixture, opts ...Option) *Server {
	srv := &Server{
		state: NewState(f),
		log:   slog.Default(),
	}
	for _, o := range opts {
		o(srv)
	}
	srv.router = srv.buildRouter()
	return srv
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithLogger overrides the server's slog.Logger (default: slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.log = l }
}

// WithTicketTTL makes tickets issued by POST /access/ticket expire after
// ttl, overriding the fixture's mock.ticket_ttl_ms (if any). Zero or
// negative restores the default "never expires" behavior. Expired tickets
// are rejected with 401 exactly like unknown ones, mirroring real PVE's 2h
// ticket lifetime on a test-friendly timescale.
func WithTicketTTL(ttl time.Duration) Option {
	return func(s *Server) {
		if ttl < 0 {
			ttl = 0
		}
		s.state.sessions.setTTL(ttl)
	}
}

// MockIdentityHeader is set on every response this package's servers send,
// naming which of them answered ("server" here, "replay" in replay.go).
//
// Added for T-2501. A real pveproxy cannot set it, so it is an unambiguous
// answer to "am I talking to a mock?" — which `vnproxctl verify` asks before
// it will produce a hardware-validation report, because a green run against
// this server is indistinguishable from a green run against real Proxmox and
// would raise the validated count in docs/status-matrix.md without validating
// anything. Identifying itself is the mock's own responsibility; leaving the
// caller to infer it from behaviour is how the inference eventually goes
// wrong in the direction nobody notices.
const MockIdentityHeader = "X-Pvemock"

// ServeHTTP implements http.Handler.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(MockIdentityHeader, "server")
	srv.router.ServeHTTP(w, r)
}

// State exposes the server's runtime state, primarily so tests (and the
// fixture-backed host.Reader) can inspect/mutate it directly without a
// network round trip.
func (srv *Server) State() *State { return srv.state }

func (srv *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(requestLogger(srv.log))

	r.Route("/api2/json", func(api chi.Router) {
		api.Post("/access/ticket", srv.handleTicket)
		// Any authenticated identity may read its own effective
		// permission set (no specific privilege required), like real PVE.
		api.Get("/access/permissions", srv.handleAccessPermissions)

		api.Get("/cluster/status", srv.requirePrivilege(PrivSysAudit, srv.handleClusterStatus))
		api.Get("/cluster/resources", srv.requirePrivilege(PrivSysAudit, srv.handleClusterResources))
		// T-1503: Ceph read-only awareness — cluster-wide public/cluster
		// network config plus per-node OSD placement, gated on the same
		// PrivSysAudit read privilege every other read-only cluster/node
		// route above uses (no Ceph-specific privilege exists in real PVE
		// either; Ceph's own admin surface uses Sys.Modify, which this mock
		// deliberately never grants a route for — see ceph.go's doc
		// comment: PVE's own Ceph tooling keeps ownership).
		api.Get("/cluster/ceph/config", srv.requirePrivilege(PrivSysAudit, srv.handleCephConfig))
		api.Get("/nodes/{node}/ceph/osd", srv.requirePrivilege(PrivSysAudit, srv.handleCephOSDs))
		// T-1206: PBS network awareness — cluster-wide storage.cfg entries
		// and vzdump backup jobs, gated on the same PrivSysAudit read
		// privilege every other read-only cluster route uses. Both are
		// read-only (internal/pbs never writes storage or backup config —
		// PVE's own tooling keeps ownership), so no POST/PUT/DELETE
		// counterpart is registered here.
		api.Get("/storage", srv.requirePrivilege(PrivSysAudit, srv.handleStorageList))
		api.Get("/cluster/backup", srv.requirePrivilege(PrivSysAudit, srv.handleBackupJobs))

		api.Get("/nodes/{node}/network", srv.requirePrivilege(PrivSysAudit, srv.handleNetworkList))
		api.Post("/nodes/{node}/network", srv.requirePrivilege(PrivSysModify, srv.handleNetworkCreate))
		api.Put("/nodes/{node}/network", srv.requirePrivilege(PrivSysModify, srv.handleNetworkReload))
		api.Delete("/nodes/{node}/network", srv.requirePrivilege(PrivSysModify, srv.handleNetworkRevert))
		api.Get("/nodes/{node}/network/{iface}", srv.requirePrivilege(PrivSysAudit, srv.handleNetworkGet))
		api.Put("/nodes/{node}/network/{iface}", srv.requirePrivilege(PrivSysModify, srv.handleNetworkUpdate))
		api.Delete("/nodes/{node}/network/{iface}", srv.requirePrivilege(PrivSysModify, srv.handleNetworkDelete))

		api.Get("/nodes/{node}/qemu/{vmid}/config", srv.requirePrivilege(PrivVMAudit, srv.handleGuestConfigGet("qemu")))
		api.Put("/nodes/{node}/qemu/{vmid}/config", srv.requirePrivilege(PrivVMConfigNet, srv.handleGuestConfigPut("qemu")))
		api.Get("/nodes/{node}/lxc/{vmid}/config", srv.requirePrivilege(PrivVMAudit, srv.handleGuestConfigGet("lxc")))
		api.Put("/nodes/{node}/lxc/{vmid}/config", srv.requirePrivilege(PrivVMConfigNet, srv.handleGuestConfigPut("lxc")))
		api.Get("/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces", srv.requirePrivilege(PrivVMAudit, srv.handleGuestAgentInterfaces))
		// T-802: guest-agent exec + poll, backing the live path-probe
		// engine (internal/probe). Gated on the same PrivVMAudit privilege
		// handleGuestAgentInterfaces above uses — this mock does not model
		// real PVE's separate VM.Monitor privilege for guest-agent exec,
		// following that route's own existing precedent rather than
		// inventing a new privilege name unused anywhere else in this
		// package.
		api.Post("/nodes/{node}/qemu/{vmid}/agent/exec", srv.requirePrivilege(PrivVMAudit, srv.handleGuestAgentExec))
		api.Get("/nodes/{node}/qemu/{vmid}/agent/exec-status", srv.requirePrivilege(PrivVMAudit, srv.handleGuestAgentExecStatus))
		// T-806: the guest-agent transport liveness check backing the
		// "Verify live" button's eligibility gate.
		api.Post("/nodes/{node}/qemu/{vmid}/agent/ping", srv.requirePrivilege(PrivVMAudit, srv.handleGuestAgentPing))

		api.Get("/nodes/{node}/tasks/{upid}/status", srv.requirePrivilege(PrivSysAudit, srv.handleTaskStatus))
		api.Get("/nodes/{node}/tasks/{upid}/log", srv.requirePrivilege(PrivSysAudit, srv.handleTaskLog))

		srv.mountSDN(api)
		srv.mountSDNDNS(api)
		srv.mountSDNFabric(api)
		srv.mountSDNController(api)
		srv.mountIPAM(api)
		srv.mountFirewall(api)
	})

	// Test/dev control-plane, never part of the real PVE API: introspect
	// the fixture's documented "mess", and force latency/failure
	// injection without editing YAML. Intentionally unauthenticated so
	// test harnesses can flip it before logging in as a given user.
	r.Route("/mock", func(m chi.Router) {
		m.Get("/mess", srv.handleMockMess)
		m.Post("/nodes/{node}/network-reload-fail", srv.handleMockSetNetworkReloadFail)
		m.Post("/nodes/{node}/sdn-status-fail", srv.handleMockSetSDNZoneStatusFail)
		m.Post("/nodes/{node}/firewall-compile-fail", srv.handleMockSetFirewallCompileFail)
	})

	return r
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Debug("pvemock request", "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}
