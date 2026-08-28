// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// This file is T-1002's HTTP-level coverage for GET /flows' cluster fan-out
// and filter wiring, mirroring cluster_http_test.go's audit/snapshot
// pattern exactly: a fake local FlowLocalSource plus a fake PeerFlowSource
// standing in for real peers (internal/peer's own client/server wire-level
// correctness is covered by that package's tests; the underlying filter
// narrowing logic — guest/vlan/subnet/port/proto/fromTs/toTs — is covered by
// internal/store/flows_test.go against a real SQLite-backed
// FlowSampleRepo). This file's job is proving the two are wired together
// correctly: query params parse into the right store.FlowFilter, and the
// cluster merge produces the documented partial/failedNodes envelope.

type fakeFlowLocalSource struct {
	samples    []store.FlowSample
	lastFilter store.FlowFilter
}

func (f *fakeFlowLocalSource) Query(_ context.Context, filter store.FlowFilter, _ string, limit int) ([]store.FlowSample, string, error) {
	f.lastFilter = filter
	end := limit
	if end > len(f.samples) {
		end = len(f.samples)
	}
	return f.samples[:end], "", nil
}

type fakePeerFlowSource struct {
	perPeer    map[string][]peer.FlowRecord
	failNodes  map[string]bool
	peers      []peer.Peer
	lastFilter peer.FlowFilter
}

func (f *fakePeerFlowSource) Peers(context.Context) ([]peer.Peer, error) { return f.peers, nil }

func (f *fakePeerFlowSource) Flows(_ context.Context, p peer.Peer, filter peer.FlowFilter, _ string, limit int) ([]peer.FlowRecord, string, error) {
	f.lastFilter = filter
	if f.failNodes[p.Node] {
		return nil, "", errors.New("fake: peer unreachable")
	}
	items := f.perPeer[p.Node]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, "", nil
}

func flowsTestRouter(svc FlowLocalSource, peers PeerFlowSource) http.Handler {
	return NewRouter(Options{
		Version:   "test",
		DistFS:    testDistFS(),
		Logger:    testLogger(),
		Auth:      fakeAuth{authenticated: true},
		Flows:     svc,
		PeerFlows: peers,
	})
}

func flowsTestRouterWithClassifier(svc FlowLocalSource, peers PeerFlowSource, classifier *flow.Classifier) http.Handler {
	return NewRouter(Options{
		Version:        "test",
		DistFS:         testDistFS(),
		Logger:         testLogger(),
		Auth:           fakeAuth{authenticated: true},
		Flows:          svc,
		PeerFlows:      peers,
		FlowClassifier: classifier,
	})
}

func TestFlowsRoute_LocalOnly_UnchangedWhenNoPeerSource(t *testing.T) {
	svc := &fakeFlowLocalSource{samples: []store.FlowSample{
		{ID: 1, At: 100, Node: "pve1", SrcIP: "10.0.0.1", DstIP: "10.0.0.2", Proto: 6, Source: "netflow5"},
	}}
	r := flowsTestRouter(svc, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/flows", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body flowListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].SrcIP != "10.0.0.1" {
		t.Fatalf("items = %+v", body.Items)
	}
	if body.Partial {
		t.Error("partial = true, want false (no peer source configured)")
	}
}

func TestFlowsRoute_ClusterMerge(t *testing.T) {
	svc := &fakeFlowLocalSource{samples: []store.FlowSample{
		{ID: 1, At: 300, Node: "pve1", SrcIP: "10.0.0.1", DstIP: "10.0.0.2", Proto: 6, Source: "netflow5"},
	}}
	peers := &fakePeerFlowSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}, {Node: "pve3", Addr: "10.0.0.3:8007"}},
		perPeer: map[string][]peer.FlowRecord{
			"pve2": {{ID: 1, At: 200, Node: "pve2", SrcIP: "10.0.1.1", DstIP: "10.0.1.2", Proto: 17, Source: "sflow"}},
		},
		failNodes: map[string]bool{"pve3": true},
	}
	r := flowsTestRouter(svc, peers)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/flows?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body flowListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2 (local + pve2; pve3 failed)", body.Items)
	}
	if body.Items[0].SrcIP != "10.0.0.1" || body.Items[1].SrcIP != "10.0.1.1" {
		t.Errorf("items order = %+v, want [local (at=300), pve2 (at=200)]", body.Items)
	}
	if !body.Partial {
		t.Error("partial = false, want true (pve3 failed)")
	}
	if len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve3" {
		t.Errorf("failedNodes = %v, want [pve3]", body.FailedNodes)
	}
}

func TestFlowsRoute_QueryParamsBuildFilter(t *testing.T) {
	svc := &fakeFlowLocalSource{}
	r := flowsTestRouter(svc, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/flows?guest=bridge:pve1:vmbr0&vlan=100&subnet=10.0.0.0/24&port=443&protocol=tcp&fromTs=1&toTs=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := store.FlowFilter{Guest: "bridge:pve1:vmbr0", VLAN: 100, Subnet: "10.0.0.0/24", Port: 443, Proto: 6, FromTs: 1, ToTs: 2}
	if svc.lastFilter != want {
		t.Errorf("filter = %+v, want %+v", svc.lastFilter, want)
	}
}

func TestFlowsRoute_ProtocolFilter_NumericAndName(t *testing.T) {
	tests := []struct {
		query     string
		wantProto int
	}{
		{"protocol=tcp", 6},
		{"protocol=17", 17},
		{"protocol=udp", 17},
		{"protocol=not-a-protocol", -1}, // unrecognized: matches nothing, never a 400
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			svc := &fakeFlowLocalSource{}
			r := flowsTestRouter(svc, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/flows?"+tt.query, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if svc.lastFilter.Proto != tt.wantProto {
				t.Errorf("Proto = %d, want %d", svc.lastFilter.Proto, tt.wantProto)
			}
		})
	}
}

func TestFlowsRoute_InvalidVLAN_400(t *testing.T) {
	svc := &fakeFlowLocalSource{}
	r := flowsTestRouter(svc, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/flows?vlan=notanumber", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestFlowsRoute_ServiceClass covers T-1504 AC2's GET /flows half: when a
// FlowClassifier is wired, every item (local and peer-fanned-out alike)
// carries the additive serviceClass field; when no classifier is wired
// (the pre-T-1504 default), the field is omitted entirely rather than
// emitted as a misleading "unclassified".
func TestFlowsRoute_ServiceClass(t *testing.T) {
	svc := &fakeFlowLocalSource{samples: []store.FlowSample{
		{ID: 1, At: 100, Node: "pve1", SrcIP: "10.10.10.1", DstIP: "10.10.10.2", Proto: 17, Source: "netflow5"},
		{ID: 2, At: 200, Node: "pve1", SrcIP: "203.0.113.1", DstIP: "203.0.113.2", Proto: 6, Source: "netflow5"},
	}}

	classifier := flow.NewClassifier()
	classifier.RegisterNetworkSource(flow.NetworkSourceKindCorosync, flow.NewCorosyncSource([]string{"10.10.10.1"}, nil))

	r := flowsTestRouterWithClassifier(svc, nil, classifier)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/flows", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body flowListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2", body.Items)
	}
	if body.Items[0].ServiceClass != "corosync" {
		t.Errorf("items[0].ServiceClass = %q, want corosync", body.Items[0].ServiceClass)
	}
	if body.Items[1].ServiceClass != "unclassified" {
		t.Errorf("items[1].ServiceClass = %q, want unclassified", body.Items[1].ServiceClass)
	}

	// No classifier wired: field is omitted from the raw JSON, not present
	// as an empty/zero value.
	r2 := flowsTestRouter(svc, nil)
	rec2 := httptest.NewRecorder()
	r2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/v1/flows", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec2.Code, rec2.Body.String())
	}
	if bodyStr := rec2.Body.String(); strings.Contains(bodyStr, "serviceClass") {
		t.Errorf("body = %s, want no serviceClass field when no classifier is wired", bodyStr)
	}
}

func TestFlowsRoute_NotMountedWhenNil(t *testing.T) {
	r := flowsTestRouter(nil, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/flows", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("expected GET /flows to not be mounted when Flows is nil, got 200")
	}
}
