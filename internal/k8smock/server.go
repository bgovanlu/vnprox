package k8smock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/bgovanlu/vnprox/internal/k8s"
)

// Recorder tracks every request a k8smock server received, safe for
// concurrent use — the same shape internal/ingress/ingressmock.Recorder
// uses.
type Recorder struct {
	requests []RecordedRequest
	mu       sync.Mutex
}

// RecordedRequest is one request a k8smock server observed.
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

// NewServer builds an httptest.Server serving f's four fixed k8s API
// endpoints (see this package's doc comment). Any other path 404s, same
// as a real apiserver would for a path this package's Client never
// requests.
func NewServer(f Fixture) (*httptest.Server, *Recorder) {
	rec := &Recorder{}
	nodes, pods, services, daemonsets := f.ToK8s()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes", writeJSON(k8s.NodeList{Items: nodes}))
	mux.HandleFunc("/api/v1/pods", writeJSON(k8s.PodList{Items: pods}))
	mux.HandleFunc("/api/v1/services", writeJSON(k8s.ServiceList{Items: services}))
	mux.HandleFunc("/apis/apps/v1/namespaces/kube-system/daemonsets", writeJSON(k8s.DaemonSetList{Items: daemonsets}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		mux.ServeHTTP(w, r)
	}))
	return srv, rec
}

func writeJSON(body any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}
