package collect_test

// F-01 regression test: a node that leaves the cluster (disappears from
// GET /cluster/status) must have all of its entities retired from the
// inventory graph within a poll cycle — not linger as a stale ghost until
// daemon restart — while every other node's entities stay untouched.
//
// pvemock has no runtime cluster-membership mutation API, so membership
// change is simulated one layer up: membershipFilter interposes on the
// mock's HTTP responses and hides a node from /cluster/status (and its
// rows from /cluster/resources, which is what real PVE would report once
// the node is gone). Everything below the collector — client, auth,
// routing — is exercised for real.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// membershipFilter proxies to the wrapped pvemock handler, dropping
// removed nodes' rows from cluster-status and cluster-resources responses.
type membershipFilter struct {
	inner   http.Handler
	removed map[string]bool
	mu      sync.Mutex
}

func newMembershipFilter(inner http.Handler) *membershipFilter {
	return &membershipFilter{inner: inner, removed: map[string]bool{}}
}

// Remove hides node from cluster membership for all subsequent responses.
func (f *membershipFilter) Remove(node string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed[node] = true
}

func (f *membershipFilter) isRemoved(node string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.removed[node]
}

func (f *membershipFilter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isStatus := r.URL.Path == "/api2/json/cluster/status"
	isResources := r.URL.Path == "/api2/json/cluster/resources"
	if r.Method != http.MethodGet || (!isStatus && !isResources) {
		f.inner.ServeHTTP(w, r)
		return
	}

	rec := httptest.NewRecorder()
	f.inner.ServeHTTP(rec, r)

	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &payload) != nil {
		copyRecorded(w, rec)
		return
	}

	kept := payload.Data[:0]
	for _, row := range payload.Data {
		rowType, _ := row["type"].(string)
		name, _ := row["name"].(string)
		node, _ := row["node"].(string)
		if isStatus && rowType == "node" && f.isRemoved(name) {
			continue
		}
		if isResources && f.isRemoved(node) {
			continue
		}
		kept = append(kept, row)
	}
	payload.Data = kept

	body, err := json.Marshal(payload)
	if err != nil {
		copyRecorded(w, rec)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func copyRecorded(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, vs := range rec.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// TestDepartedNodeRetired converges on the three-node fixture, removes pve2
// (the node that also hosts guest 201, so guest + guest-nic + guest
// firewall retirement is exercised alongside node, pve-network, and node
// firewall entities), and asserts the departed node's entities vanish
// while the surviving nodes' ref set is exactly what it was before.
func TestDepartedNodeRetired(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	filter := newMembershipFilter(srv)
	c, graph, _ := newTestCollectorHandler(t, srv, filter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	go func() { _ = c.RunHostLoop(ctx) }()
	go func() { _ = c.RunLLDPLoop(ctx) }()

	all := threeNodeVlanRefs()
	waitFor(t, 3*time.Second, "graph to converge before the membership change", func() bool {
		return graph.Snapshot().Len() == len(all)
	})
	if got := snapshotRefs(graph.Snapshot()); !reflect.DeepEqual(got, all) {
		t.Fatalf("pre-departure ref set mismatch:\n got %v\nwant %v", got, all)
	}

	// Expected survivors: everything whose ref does not live on pve2.
	var want []string
	for _, ref := range all {
		if !strings.Contains(ref, ":pve2:") {
			want = append(want, ref)
		}
	}
	if len(want) != len(all)-10 {
		// pve2 owns: node, eno1, eno2, bond0, vmbr0, vmbr0.20, node
		// fw-ruleset, guest 201, its nic, its fw-ruleset = 10 refs.
		t.Fatalf("test setup: expected exactly 10 pve2-scoped refs, got %d", len(all)-len(want))
	}

	filter.Remove("pve2")

	// The retirement must land within a poll cycle of the collector seeing
	// the new membership (50ms poll intervals; the timeout is CI headroom,
	// not the bound under test — the assertion below is exact).
	waitFor(t, 3*time.Second, "departed node's entities to be retired", func() bool {
		return graph.Snapshot().Len() == len(want)
	})

	got := snapshotRefs(graph.Snapshot())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-departure ref set mismatch:\n got %v\nwant %v", got, want)
	}
	for _, ref := range got {
		if strings.Contains(ref, ":pve2:") {
			t.Errorf("stale ghost entity survived departure: %s", ref)
		}
	}

	// Survivors must be genuinely untouched, not merely present: pve1's
	// bridge still carries its merged three-source view.
	snap := graph.Snapshot()
	brRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	br, ok := snap.Get(brRef)
	if !ok {
		t.Fatalf("surviving bridge %s missing", brRef)
	}
	if b := br.(*inventory.Bridge); !b.VlanAware || b.Gateway != "10.10.0.1" {
		t.Errorf("surviving bridge lost merged state: %+v", b)
	}
}
