// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

// ErrNoCassette is the distinctive failure a replay server produces when a
// request matches no recorded cassette (T-2502 AC3).
//
// It is distinctive on purpose and in three places at once — this sentinel,
// the ReplayUnmatched header, and the status code — because the thing it
// has to be told apart from is a *plausible* answer. A replay server that
// answered an unmatched request with an empty list, a synthetic default, or
// the closest cassette it could find would hand every test above it a green
// tick earned by the mock's imagination rather than by PVE's behaviour.
// That is the exact defect class T-2108 found four instances of and the
// reason this whole card exists, so there is no fallback here to configure,
// disable, or forget to disable.
var ErrNoCassette = errors.New("pvemock: replay: no cassette matches request")

const (
	// ReplayUnmatchedStatus is the HTTP status an unmatched request gets.
	// 599 is outside every status range PVE itself uses, so a test that
	// somehow ignores the body still cannot mistake it for a real answer.
	ReplayUnmatchedStatus = 599

	// ReplayUnmatchedHeader is set to "unmatched" on that response.
	ReplayUnmatchedHeader = "X-Pvemock-Replay"

	// ReplayMatchedKeyHeader carries the cassette key a matched response
	// was served from, so a failing assertion can say which recording it
	// was reading.
	ReplayMatchedKeyHeader = "X-Pvemock-Replay-Key"
)

// Failer is the subset of *testing.T a replay server needs in order to
// fail the test that made an unmatched request, without this package
// importing "testing" into a non-test build.
type Failer interface {
	Helper()
	Errorf(format string, args ...any)
}

// ReplayServer serves recorded cassettes and nothing else.
//
// It is a sibling of Server, not a mode of it: Server answers from a YAML
// fixture through ~80 hand-written handlers, and every one of those
// handlers is a statement about what PVE does. A ReplayServer makes no
// such statements. It can answer exactly the requests somebody observed,
// and it fails loudly on everything else.
type ReplayServer struct {
	log       *slog.Logger
	cassettes map[string]pvecassette.Cassette
	failer    Failer
	dir       string
	unmatched []string

	mu     sync.Mutex
	served int
}

// ReplayOption configures a ReplayServer at construction time.
type ReplayOption func(*ReplayServer)

// WithReplayLogger overrides the server's slog.Logger (default:
// slog.Default()).
func WithReplayLogger(l *slog.Logger) ReplayOption {
	return func(s *ReplayServer) { s.log = l }
}

// WithUnmatchedFailer makes an unmatched request fail a test immediately,
// at the moment it happens, in addition to the error response. Pass a
// *testing.T.
//
// Without it an unmatched request still produces ReplayUnmatchedStatus and
// still shows up in Unmatched(), so a caller cannot get a green run out of
// one either way; the failer only makes the failure land on the line that
// caused it instead of at the end of the test.
func WithUnmatchedFailer(f Failer) ReplayOption {
	return func(s *ReplayServer) { s.failer = f }
}

// NewReplayServer loads every cassette under dir (recursively) and serves
// them. It fails if the directory is missing, if any cassette is invalid
// or carries a credential, or if two cassettes claim the same request.
func NewReplayServer(dir string, opts ...ReplayOption) (*ReplayServer, error) {
	set, err := pvecassette.LoadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("pvemock: building replay server from %s: %w", dir, err)
	}
	if len(set) == 0 {
		// An empty cassette directory would answer nothing and fail every
		// request — technically correct, and indistinguishable from a
		// typo in a path. Say so at construction instead.
		return nil, fmt.Errorf("pvemock: building replay server from %s: no cassettes found", dir)
	}
	srv := &ReplayServer{
		log:       slog.Default(),
		cassettes: set,
		dir:       dir,
	}
	for _, o := range opts {
		o(srv)
	}
	return srv, nil
}

// NewReplayServerFromSet builds a ReplayServer from cassettes already in
// memory — used by the recorder's own round-trip tests, which have no
// directory to point at.
func NewReplayServerFromSet(set map[string]pvecassette.Cassette, opts ...ReplayOption) (*ReplayServer, error) {
	if len(set) == 0 {
		return nil, fmt.Errorf("pvemock: building replay server: cassette set is empty")
	}
	srv := &ReplayServer{
		log:       slog.Default(),
		cassettes: set,
		dir:       "(in-memory)",
	}
	for _, o := range opts {
		o(srv)
	}
	return srv, nil
}

// ServeHTTP implements http.Handler.
func (s *ReplayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// T-2501: identify as a mock on every response, matched or not. A
	// cassette recorded from real hardware is still not real hardware being
	// exercised now, so `vnproxctl verify` has to be able to tell the
	// difference at the door — see MockIdentityHeader's comment in server.go.
	w.Header().Set(MockIdentityHeader, "replay")
	key := pvecassette.RequestKey(r.Method, r.URL.Path, r.URL.Query())
	c, ok := s.cassettes[key]
	if !ok {
		s.serveUnmatched(w, key)
		return
	}

	s.mu.Lock()
	s.served++
	s.mu.Unlock()

	s.log.Debug("pvemock replay", "method", r.Method, "path", r.URL.Path, "status", c.Status, "key", key)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(ReplayMatchedKeyHeader, key)
	w.WriteHeader(c.Status)
	// Written verbatim: byte-identical replay is the property the cassette
	// format exists to provide (T-2502 AC1).
	_, _ = w.Write([]byte(c.Body))
}

func (s *ReplayServer) serveUnmatched(w http.ResponseWriter, key string) {
	s.mu.Lock()
	s.unmatched = append(s.unmatched, key)
	s.mu.Unlock()

	msg := fmt.Sprintf("%v: %s (cassette dir %s holds %d recordings: %s)",
		ErrNoCassette, key, s.dir, len(s.cassettes), strings.Join(s.knownKeys(), ", "))

	s.log.Error("pvemock replay: unmatched request", "key", key, "dir", s.dir, "cassettes", len(s.cassettes))
	if s.failer != nil {
		s.failer.Helper()
		s.failer.Errorf("%s", msg)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(ReplayUnmatchedHeader, "unmatched")
	w.WriteHeader(ReplayUnmatchedStatus)
	_ = json.NewEncoder(w).Encode(pveEnvelope{Data: nil, Message: msg})
}

// knownKeys is the cassette inventory, capped so one unmatched request in
// a 500-cassette directory does not produce an unreadable error.
func (s *ReplayServer) knownKeys() []string {
	keys := pvecassette.Keys(s.cassettes)
	const maxListed = 20
	if len(keys) > maxListed {
		return append(keys[:maxListed:maxListed], fmt.Sprintf("... and %d more", len(keys)-maxListed))
	}
	return keys
}

// Unmatched returns the request keys that found no cassette, in the order
// they arrived. A test that cannot use WithUnmatchedFailer (because the
// request happens on a background goroutine, say) asserts on this instead.
func (s *ReplayServer) Unmatched() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.unmatched...)
}

// Served is how many requests were answered from a cassette.
func (s *ReplayServer) Served() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served
}

// Keys returns the sorted request keys this server can answer.
func (s *ReplayServer) Keys() []string {
	keys := pvecassette.Keys(s.cassettes)
	sort.Strings(keys)
	return keys
}
