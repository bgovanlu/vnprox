package pvemock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

// recordingFailer stands in for *testing.T so a test can assert that an
// unmatched request fails the test that made it — without failing this
// test.
type recordingFailer struct {
	msgs   []string
	helped int
}

func (f *recordingFailer) Helper() { f.helped++ }
func (f *recordingFailer) Errorf(format string, args ...any) {
	f.msgs = append(f.msgs, fmt.Sprintf(format, args...))
}

// replayFixture is the cassette set every case below runs against: one
// endpoint with no query, and the same path with a query, so "no cassette"
// and "the wrong cassette" are both reachable.
func replayFixture() map[string]pvecassette.Cassette {
	zones := pvecassette.Cassette{
		PVEVersion: "8.3.5", Method: "GET", Path: "/api2/json/cluster/sdn/zones", Status: 200,
		Body: `{"data":[{"zone":"vlanz","type":"vlan","bridge":"vmbr0"}]}` + "\n",
	}
	running := pvecassette.Cassette{
		PVEVersion: "8.3.5", Method: "GET", Path: "/api2/json/cluster/sdn/zones", Status: 200,
		Query: map[string][]string{"running": {"1"}},
		Body:  `{"data":[{"zone":"vlanz","type":"vlan","bridge":"vmbr0","state":"available"}]}` + "\n",
	}
	denied := pvecassette.Cassette{
		PVEVersion: "8.3.5", Method: "PUT", Path: "/api2/json/nodes/pve1/network", Status: 403,
		Body: `{"data":null,"message":"permission check failed (Sys.Modify)"}` + "\n",
	}
	return map[string]pvecassette.Cassette{
		zones.Key(): zones, running.Key(): running, denied.Key(): denied,
	}
}

func newReplay(t *testing.T, f Failer) *ReplayServer {
	t.Helper()
	opts := []ReplayOption{}
	if f != nil {
		opts = append(opts, WithUnmatchedFailer(f))
	}
	srv, err := NewReplayServerFromSet(replayFixture(), opts...)
	if err != nil {
		t.Fatalf("NewReplayServerFromSet: %v", err)
	}
	return srv
}

// do drives the handler with no sockets at all — httptest.NewRecorder,
// not httptest.NewServer. That is T-2502 AC6 in its strongest form:
// replay needs no network, not even a loopback listener, so the suite
// runs unchanged with outbound networking disabled.
func do(t *testing.T, srv *ReplayServer, method, target string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Result()
}

// TestReplay_UnmatchedRequestFailsLoudly is T-2502 AC3.
//
// Every row is a *near miss* — a request that a mock built to be helpful
// would have been tempted to answer: the right path with the wrong query,
// the right query on the wrong path, the right shape on a node that was
// never recorded. Each one must produce the distinctive failure instead,
// because "a fixture that works only because of a fallback default is
// impossible to write" is the criterion, and a fallback that fires only on
// exotic requests is still a fallback.
func TestReplay_UnmatchedRequestFailsLoudly(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
	}{
		{"a path nobody recorded", "GET", "/api2/json/nodes/pve1/network"},
		{"a recorded path with an unrecorded query value", "GET", "/api2/json/cluster/sdn/zones?running=0"},
		{"a recorded path with an extra query parameter", "GET", "/api2/json/cluster/sdn/zones?running=1&pending=1"},
		{"a recorded path with the query dropped where one was recorded", "GET", "/api2/json/cluster/sdn/vnets"},
		{"the right path with the wrong method", "POST", "/api2/json/cluster/sdn/zones"},
		{"a node that was never recorded", "PUT", "/api2/json/nodes/pve9/network"},
		// The mock Server answers both of these from its fixture. A
		// ReplayServer must not: it is not a Server with cassettes bolted
		// on, and inheriting even one of Server's ~80 handlers would
		// reintroduce exactly the synthetic default this card removes.
		{"the mock's own control plane", "GET", "/mock/mess"},
		{"a ticket login", "POST", "/api2/json/access/ticket"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failer := &recordingFailer{}
			srv := newReplay(t, failer)
			resp := do(t, srv, tc.method, tc.target)
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != ReplayUnmatchedStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, ReplayUnmatchedStatus)
			}
			if got := resp.Header.Get(ReplayUnmatchedHeader); got != "unmatched" {
				t.Errorf("%s = %q, want \"unmatched\"", ReplayUnmatchedHeader, got)
			}

			var env struct {
				Data    any    `json:"data"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				t.Fatalf("decoding error body: %v", err)
			}
			if env.Data != nil {
				t.Errorf("an unmatched request returned data (%v) — that is the fallback this test exists to forbid", env.Data)
			}
			if !strings.Contains(env.Message, ErrNoCassette.Error()) {
				t.Errorf("message does not carry the distinctive error:\n  %s", env.Message)
			}
			if !strings.Contains(env.Message, tc.method) || !strings.Contains(env.Message, strings.Split(tc.target, "?")[0]) {
				t.Errorf("message does not name the request that failed:\n  %s", env.Message)
			}

			if len(failer.msgs) != 1 {
				t.Fatalf("the failer was called %d times, want 1 — an unmatched request must fail the test that made it", len(failer.msgs))
			}
			if failer.helped == 0 {
				t.Error("the failer's Helper() was not called, so the failure will point at the wrong line")
			}
			if got := srv.Unmatched(); len(got) != 1 {
				t.Errorf("Unmatched() = %v, want one entry", got)
			}
			if srv.Served() != 0 {
				t.Errorf("Served() = %d, want 0", srv.Served())
			}
		})
	}
}

// TestReplay_ServesRecordedBodiesByteForByte is T-2502 AC1 at the handler
// level, including the trailing newline PVE's own encoder emits and the
// non-200 status a recorded 403 carries.
func TestReplay_ServesRecordedBodiesByteForByte(t *testing.T) {
	fixture := replayFixture()
	srv := newReplay(t, t)

	for _, key := range pvecassette.Keys(fixture) {
		want := fixture[key]
		target := want.Path
		if q := strings.SplitN(key, "?", 2); len(q) == 2 {
			target += "?" + q[1]
		}
		t.Run(key, func(t *testing.T) {
			resp := do(t, srv, want.Method, target)
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != want.Status {
				t.Errorf("status = %d, want %d", resp.StatusCode, want.Status)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}
			if string(got) != want.Body {
				t.Errorf("body is not byte-identical:\n  got:  %q\n  want: %q", got, want.Body)
			}
			if h := resp.Header.Get(ReplayMatchedKeyHeader); h != key {
				t.Errorf("%s = %q, want %q", ReplayMatchedKeyHeader, h, key)
			}
		})
	}
	if srv.Served() != len(fixture) {
		t.Errorf("Served() = %d, want %d", srv.Served(), len(fixture))
	}
}

// TestReplay_MatchesQueryRegardlessOfOrder is AC4 at the handler level:
// the client builds a query string with url.Values.Encode, but nothing
// promises a *recorded* request was serialised the same way.
func TestReplay_MatchesQueryRegardlessOfOrder(t *testing.T) {
	multi := pvecassette.Cassette{
		PVEVersion: "8.3.5", Method: "GET", Path: "/api2/json/nodes/pve1/network", Status: 200,
		Query: map[string][]string{"type": {"bridge"}, "node": {"pve1"}},
		Body:  `{"data":[{"iface":"vmbr0"}]}`,
	}
	srv, err := NewReplayServerFromSet(map[string]pvecassette.Cassette{multi.Key(): multi}, WithUnmatchedFailer(t))
	if err != nil {
		t.Fatalf("NewReplayServerFromSet: %v", err)
	}

	for _, target := range []string{
		"/api2/json/nodes/pve1/network?type=bridge&node=pve1",
		"/api2/json/nodes/pve1/network?node=pve1&type=bridge",
	} {
		resp := do(t, srv, "GET", target)
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d, want 200", target, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	// ...and value-sensitively: a single changed value is a different
	// request, not a near-enough one.
	failer := &recordingFailer{}
	valueSensitive, err := NewReplayServerFromSet(map[string]pvecassette.Cassette{multi.Key(): multi}, WithUnmatchedFailer(failer))
	if err != nil {
		t.Fatalf("NewReplayServerFromSet: %v", err)
	}
	resp := do(t, valueSensitive, "GET", "/api2/json/nodes/pve1/network?node=pve2&type=bridge")
	_ = resp.Body.Close()
	if resp.StatusCode != ReplayUnmatchedStatus {
		t.Errorf("a changed query value matched a cassette: status = %d", resp.StatusCode)
	}
}

// TestNewReplayServer_RefusesAnEmptyDirectory: a server that can answer
// nothing and a typo in a path are indistinguishable at request time, so
// the distinction is made at construction time.
func TestNewReplayServer_RefusesAnEmptyDirectory(t *testing.T) {
	if _, err := NewReplayServer(t.TempDir()); err == nil {
		t.Error("NewReplayServer accepted a directory with no cassettes")
	}
	if _, err := NewReplayServerFromSet(nil); err == nil {
		t.Error("NewReplayServerFromSet accepted an empty set")
	}
}
