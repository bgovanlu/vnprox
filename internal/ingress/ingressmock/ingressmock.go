// Package ingressmock provides the four reverse-proxy status-endpoint
// doubles T-1406's card names — HAProxy, nginx, Caddy, Traefik — as small
// httptest.Server fixtures serving realistic canned responses in each
// vendor's own real wire format. internal/ingress's own vendor discoverer
// tests are built against these; they are exported (not test-only) so a
// future T-1702 plugin discoverer conformance suite can import and reuse
// them verbatim, the same way internal/pvemock is reused across many
// unrelated packages' tests.
//
// Every server here also records every request it received (Requests()),
// so a caller can assert on the exact HTTP methods a discoverer issued —
// the fixture half of T-1406 AC4's zero-write-surface regression test.
package ingressmock

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
)

// Recorder tracks every request an ingressmock server received, safe for
// concurrent use by the server's handler goroutine and a test's assertions.
type Recorder struct {
	requests []RecordedRequest
	mu       sync.Mutex
}

// RecordedRequest is one request an ingressmock server observed.
type RecordedRequest struct {
	Method string
	Path   string
}

func (r *Recorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, RecordedRequest{Method: req.Method, Path: req.URL.Path})
}

// Requests returns every request recorded so far, in arrival order.
func (r *Recorder) Requests() []RecordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedRequest, len(r.requests))
	copy(out, r.requests)
	return out
}

// HAProxyBackend describes one server row the HAProxy double's CSV stats
// page reports.
type HAProxyBackend struct {
	Pool string
	Name string
	Addr string // may be "" to simulate an HAProxy version with no addr column
	Up   bool
}

// NewHAProxyServer builds an httptest.Server serving HAProxy's classic
// `;csv` stats export (GET-only) for the given backends, plus the standard
// FRONTEND/BACKEND aggregate rows every real deployment also emits.
func NewHAProxyServer(backends []HAProxyBackend) (*httptest.Server, *Recorder) {
	rec := &Recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(renderHAProxyCSV(backends)))
	}))
	return srv, rec
}

func renderHAProxyCSV(backends []HAProxyBackend) string {
	out := "# pxname,svname,status,addr\n"
	pools := map[string]bool{}
	for _, b := range backends {
		pools[b.Pool] = true
	}
	for pool := range pools {
		out += pool + ",FRONTEND,OPEN,\n"
		out += pool + ",BACKEND,UP,\n"
	}
	for _, b := range backends {
		status := "DOWN"
		if b.Up {
			status = "UP"
		}
		out += b.Pool + "," + b.Name + "," + status + "," + b.Addr + "\n"
	}
	return out
}

// NginxMode selects which real nginx status format NewNginxServer serves.
type NginxMode int

const (
	// NginxStubStatus serves open-source nginx's plain-text stub_status
	// block (no per-backend data).
	NginxStubStatus NginxMode = iota
	// NginxPlusAPI serves nginx Plus's JSON upstreams API.
	NginxPlusAPI
)

// NginxPeer is one nginx Plus upstream peer.
type NginxPeer struct {
	Server string
	Up     bool
}

// NewNginxServer builds an httptest.Server serving either nginx format.
// upstream/peers are only used in NginxPlusAPI mode.
func NewNginxServer(mode NginxMode, upstream string, peers []NginxPeer) (*httptest.Server, *Recorder) {
	rec := &Recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch mode {
		case NginxPlusAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(renderNginxPlusJSON(upstream, peers)))
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Active connections: 3 \nserver accepts handled requests\n 5 5 10 \nReading: 0 Writing: 1 Waiting: 2 \n"))
		}
	}))
	return srv, rec
}

func renderNginxPlusJSON(upstream string, peers []NginxPeer) string {
	body := `{"` + upstream + `":{"peers":[`
	for i, p := range peers {
		if i > 0 {
			body += ","
		}
		state := "down"
		if p.Up {
			state = "up"
		}
		body += `{"server":"` + p.Server + `","state":"` + state + `"}`
	}
	body += `]}}`
	return body
}

// CaddyUpstream is one server the Caddy double's admin API reports.
type CaddyUpstream struct {
	Address string
	Fails   int
}

// NewCaddyServer builds an httptest.Server serving Caddy's admin API
// `GET /reverse_proxy/upstreams` shape.
func NewCaddyServer(upstreams []CaddyUpstream) (*httptest.Server, *Recorder) {
	rec := &Recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		body := "["
		for i, u := range upstreams {
			if i > 0 {
				body += ","
			}
			body += `{"address":"` + u.Address + `","num_requests":0,"fails":` + strconv.Itoa(u.Fails) + `}`
		}
		body += "]"
		_, _ = w.Write([]byte(body))
	}))
	return srv, rec
}

// TraefikServer is one service the Traefik double's API reports.
type TraefikServer struct {
	Name    string
	URLs    []string
	Enabled bool
}

// NewTraefikServer builds an httptest.Server serving Traefik's API
// `GET /api/http/services` shape.
func NewTraefikServer(services []TraefikServer) (*httptest.Server, *Recorder) {
	rec := &Recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		body := "["
		for i, s := range services {
			if i > 0 {
				body += ","
			}
			status := "disabled"
			if s.Enabled {
				status = "enabled"
			}
			body += `{"name":"` + s.Name + `","status":"` + status + `","loadBalancer":{"servers":[`
			for j, u := range s.URLs {
				if j > 0 {
					body += ","
				}
				body += `{"url":"` + u + `"}`
			}
			body += `]}}`
		}
		body += "]"
		_, _ = w.Write([]byte(body))
	}))
	return srv, rec
}
