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

// ServeHTTP implements http.Handler.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

		api.Get("/cluster/status", srv.requirePrivilege(PrivSysAudit, srv.handleClusterStatus))
		api.Get("/cluster/resources", srv.requirePrivilege(PrivSysAudit, srv.handleClusterResources))

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

		api.Get("/nodes/{node}/tasks/{upid}/status", srv.requirePrivilege(PrivSysAudit, srv.handleTaskStatus))
		api.Get("/nodes/{node}/tasks/{upid}/log", srv.requirePrivilege(PrivSysAudit, srv.handleTaskLog))

		srv.mountSDN(api)
		srv.mountFirewall(api)
	})

	// Test/dev control-plane, never part of the real PVE API: introspect
	// the fixture's documented "mess", and force latency/failure
	// injection without editing YAML. Intentionally unauthenticated so
	// test harnesses can flip it before logging in as a given user.
	r.Route("/mock", func(m chi.Router) {
		m.Get("/mess", srv.handleMockMess)
		m.Post("/nodes/{node}/network-reload-fail", srv.handleMockSetNetworkReloadFail)
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
