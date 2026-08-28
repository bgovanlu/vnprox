// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRegistry_HTTPRequest_RendersCounterAndHistogram(t *testing.T) {
	r := NewRegistry(nil)
	r.ObserveHTTPRequest("/api/v1/changesets/{id}", "GET", 200, 12*time.Millisecond)
	r.ObserveHTTPRequest("/api/v1/changesets/{id}", "GET", 500, 40*time.Millisecond)

	var buf bytes.Buffer
	r.WriteTo(&buf)
	body := buf.String()

	for _, want := range []string{
		`vnprox_http_requests_total{route="/api/v1/changesets/{id}",method="GET",status_class="2xx"} 1`,
		`vnprox_http_requests_total{route="/api/v1/changesets/{id}",method="GET",status_class="5xx"} 1`,
		`vnprox_http_request_duration_seconds_count{route="/api/v1/changesets/{id}",method="GET"} 2`,
		"# TYPE vnprox_http_requests_total counter",
		"# TYPE vnprox_http_request_duration_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull body:\n%s", want, body)
		}
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		100: "1xx", 200: "2xx", 201: "2xx", 301: "3xx", 404: "4xx", 500: "5xx", 599: "5xx", 999: "other", 0: "other",
	}
	for status, want := range cases {
		if got := StatusClass(status); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestHistogramVec_CumulativeBuckets(t *testing.T) {
	h := newHistogramVec("test_hist", "help", nil, []float64{1, 2, 5}, nil)
	h.observe(0.5)
	h.observe(1.5)
	h.observe(10)

	var buf bytes.Buffer
	h.writeTo(&buf)
	body := buf.String()

	// le="1" should count only the 0.5s observation; le="2" should count
	// 0.5s and 1.5s (cumulative); le="+Inf" should count all three.
	for _, want := range []string{
		`test_hist_bucket{le="1"} 1`,
		`test_hist_bucket{le="2"} 2`,
		`test_hist_bucket{le="5"} 2`,
		`test_hist_bucket{le="+Inf"} 3`,
		`test_hist_count 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull body:\n%s", want, body)
		}
	}
}

func TestCounterVec_CardinalityCapCollapsesToOverflowBucket(t *testing.T) {
	c := newCounterVec("test_counter", "help", []string{"label"}, nil)
	c.cap = 3 // small cap so the test doesn't need 4000 distinct values

	c.inc("a")
	c.inc("b")
	c.inc("c")
	// A 4th distinct label value exceeds the cap — it must fold into the
	// overflow bucket rather than growing the series count unboundedly.
	c.inc("d")
	c.inc("e")

	c.mu.Lock()
	n := len(c.values)
	overflowCount := *c.values[overflowLabel]
	c.mu.Unlock()

	if n != 4 { // a, b, c, and the one overflow bucket
		t.Fatalf("series count = %d, want 4 (3 real + 1 overflow bucket)", n)
	}
	if overflowCount != 2 {
		t.Fatalf("overflow bucket count = %d, want 2", overflowCount)
	}
}

func TestHistogramVec_CardinalityCapCollapsesToOverflowBucket(t *testing.T) {
	h := newHistogramVec("test_hist_cap", "help", []string{"label"}, []float64{1}, nil)
	h.cap = 2

	h.observe(0.5, "a")
	h.observe(0.5, "b")
	h.observe(0.5, "c") // over cap

	h.mu.Lock()
	n := len(h.data)
	h.mu.Unlock()

	if n != 3 { // a, b, and the overflow bucket
		t.Fatalf("series count = %d, want 3", n)
	}
}

func TestRegistry_ChangeAndCollectorAndPeerAndStoreObservations(t *testing.T) {
	r := NewRegistry(nil)
	r.ObserveCollectorPoll("pve", "", 20*time.Millisecond, nil)
	r.ObserveCollectorPoll("host", "pve1", 5*time.Millisecond, errFake)
	r.ObserveChangeOutcome(ChangeOpApply, true)
	r.ObserveChangeOutcome(ChangeOpUnattendedRevert, false)
	r.ObserveAwaitingConfirmDuration("rolled_back", 90*time.Second)
	r.ObserveStoreQuery("select", time.Millisecond)
	r.ObservePeerCall("pve2", "/api/peer/host/stats", "ok", 8*time.Millisecond)
	r.ObservePeerCall("pve2", "/api/peer/host/stats", "unreachable", 8*time.Millisecond)

	var buf bytes.Buffer
	r.WriteTo(&buf)
	body := buf.String()

	for _, want := range []string{
		`vnprox_collector_polls_total{source="pve",node="",outcome="success"} 1`,
		`vnprox_collector_polls_total{source="host",node="pve1",outcome="failure"} 1`,
		`vnprox_change_outcomes_total{op="apply",outcome="success"} 1`,
		`vnprox_change_outcomes_total{op="unattended_revert",outcome="failure"} 1`,
		`vnprox_change_awaiting_confirm_seconds_count{outcome="rolled_back"} 1`,
		`vnprox_store_query_duration_seconds_count{op="select"} 1`,
		`vnprox_peer_calls_total{node="pve2",endpoint="/api/peer/host/stats",outcome="ok"} 1`,
		`vnprox_peer_calls_total{node="pve2",endpoint="/api/peer/host/stats",outcome="unreachable"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull body:\n%s", want, body)
		}
	}
}

var errFake = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "fake" }
