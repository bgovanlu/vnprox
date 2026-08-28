// SPDX-License-Identifier: Apache-2.0

// demo_test.go covers T-2801's demo world: the embedded dataset loads and
// validates, the in-process transport reaches the fixture without a socket,
// and — the one that matters — a chi-routed caller's request context cannot
// break it.
package demo

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func TestLoadDataset(t *testing.T) {
	ds, err := LoadDataset()
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if got := len(ds.Fixture.Nodes); got < 3 {
		t.Errorf("demo cluster has %d nodes; a demo of a cluster-aware product needs at least 3", got)
	}
	if len(ds.Fixture.SDN.Zones) == 0 || len(ds.Fixture.SDN.Vnets) == 0 {
		t.Error("demo cluster declares no SDN zones/vnets; the card names SDN zones as demo content")
	}
	if len(ds.Fixture.Mess) == 0 {
		t.Error("demo cluster declares no `mess:` entries; the deliberate imperfections must be enumerated for a reviewer")
	}
	if len(ds.Flows.Flows) == 0 {
		t.Error("demo flow corpus is empty; the Flow Explorer would render nothing")
	}

	// Guests, on more than one node: a demo whose every guest sits on one
	// node cannot show anything cluster-shaped.
	nodesWithGuests := 0
	for _, n := range ds.Fixture.Nodes {
		if len(n.Qemu)+len(n.Lxc) > 0 {
			nodesWithGuests++
		}
	}
	if nodesWithGuests < 2 {
		t.Errorf("demo cluster has guests on %d node(s); want at least 2", nodesWithGuests)
	}
}

// The drift the demo relies on is expressed in the fixture, not asserted by
// the drift engine here (that is internal/drift's own test surface). This
// checks the fixture still SAYS what the mess list claims: a staged
// interfaces.new somewhere, and a same-named bridge whose MTU diverges
// across nodes. Both are one careless edit away from silently vanishing,
// taking the demo's Findings and Drift screens with them.
func TestDatasetKeepsItsDeliberateDrift(t *testing.T) {
	ds, err := LoadDataset()
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	pending := 0
	bridgeMTU := map[string]map[int]bool{}
	for _, n := range ds.Fixture.Nodes {
		if len(n.NetworkPending) > 0 {
			pending++
		}
		for _, iface := range n.Network {
			if iface.Type != "bridge" {
				continue
			}
			if bridgeMTU[iface.Iface] == nil {
				bridgeMTU[iface.Iface] = map[int]bool{}
			}
			bridgeMTU[iface.Iface][iface.MTU] = true
		}
	}
	if pending == 0 {
		t.Error("no node declares network_pending: the demo's pending_interfaces drift is gone")
	}
	diverged := false
	for name, mtus := range bridgeMTU {
		if len(mtus) > 1 {
			diverged = true
			t.Logf("bridge %s has diverging MTUs across nodes: %v (this is the deliberate one)", name, mtus)
		}
	}
	if !diverged {
		t.Error("no same-named bridge has a diverging MTU: the demo's mtu_consistency drift is gone")
	}
}

func TestFlowCorpusRejectsFutureOffsets(t *testing.T) {
	_, err := parseFlowCorpus([]byte("flows:\n  - {node: pve1, src_ip: 10.0.0.1, dst_ip: 10.0.0.2, proto: 6, at_offset_sec: 30, source: conntrack}\n"))
	if err == nil {
		t.Fatal("parseFlowCorpus accepted a positive at_offset_sec; a fixture shipping flows from the future is not believable demo data")
	}
	if !strings.Contains(err.Error(), "at_offset_sec") {
		t.Errorf("error does not name the offending field: %v", err)
	}
}

func TestFlowCorpusRejectsUnknownSource(t *testing.T) {
	_, err := parseFlowCorpus([]byte("flows:\n  - {node: pve1, src_ip: 10.0.0.1, dst_ip: 10.0.0.2, proto: 6, at_offset_sec: -1, source: netflow99}\n"))
	if err == nil {
		t.Fatal("parseFlowCorpus accepted an unknown source; the Flow Explorer's source filter would silently never match it")
	}
}

func TestFlowCorpusRecordsAreRelativeToNow(t *testing.T) {
	ds, err := LoadDataset()
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	records := ds.Flows.Records(now)
	if len(records) != len(ds.Flows.Flows) {
		t.Fatalf("Records returned %d records for %d specs", len(records), len(ds.Flows.Flows))
	}
	for i, r := range records {
		if r.At > now.Unix() {
			t.Errorf("record %d observed in the future (at=%d, now=%d)", i, r.At, now.Unix())
		}
		if r.Source == "" {
			t.Errorf("record %d has no source", i)
		}
	}
}

// --- the transport ---------------------------------------------------------

// The transport must answer from the fixture. The control leg is the
// address it is nominally pointed at: APIURL is unresolvable, so a
// successful call proves the request never left the process.
func TestTransportServesTheFixtureWithoutDialing(t *testing.T) {
	if _, err := net.LookupHost("demo-cluster.invalid"); err == nil {
		t.Skip("demo-cluster.invalid resolves on this host (a wildcard resolver?); the no-dial control leg is meaningless here")
	}

	m, err := New(nil)
	if err != nil {
		t.Fatalf("demo.New: %v", err)
	}
	client, err := pve.New(pve.Config{
		APIURL: APIURL, Auth: pve.AuthTicket,
		Username: TicketUsername, Password: TicketPassword,
		HTTPClient: m.HTTPClient(),
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	entries, err := client.ClusterStatus(t.Context())
	if err != nil {
		t.Fatalf("ClusterStatus over the in-process transport: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("ClusterStatus returned nothing; the fixture was not served")
	}
}

// REGRESSION, and the reason withoutValues exists.
//
// A PVE call made while serving an API request carries that request's
// context — which, under vnprox's own chi router, holds a *chi.Context.
// chi's Mux.ServeHTTP reuses an existing one instead of allocating a fresh
// one, so pvemock's router would route against the OUTER router's
// already-consumed path and answer 404. The symptom was a login that failed
// with "404 page not found" from a route that plainly exists, while every
// collector call (background context, no chi value) worked.
func TestTransportIgnoresTheCallersRouteContext(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatalf("demo.New: %v", err)
	}

	// An outer chi router shaped like vnproxd's own: a MOUNTED SUBROUTER
	// (r.Route("/api/v1", ...)), not a flat pattern. That distinction is
	// load-bearing — a flat chi route leaves RoutePath empty, so an inner
	// chi mux reusing the context still routes correctly and the bug does
	// not reproduce. A subrouter sets RoutePath to the remainder
	// ("/auth/login"), which is exactly what pvemock's router would then try
	// to route.
	var loginErr error
	outer := chi.NewRouter()
	outer.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", loginHandler(m, &loginErr))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	outer.ServeHTTP(rec, req)
	if loginErr != nil {
		t.Fatalf("PVE login made from inside a chi-routed handler failed: %v", loginErr)
	}
}

// loginHandler is the production call shape: build a PVE client on the demo
// transport and log in using the INBOUND REQUEST'S OWN CONTEXT.
func loginHandler(m *Mode, out *error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, newErr := pve.New(pve.Config{
			APIURL: APIURL, Auth: pve.AuthTicket,
			Username: "root", Realm: "pam", Password: TicketPassword,
			HTTPClient: m.HTTPClient(),
		})
		if newErr != nil {
			*out = newErr
			return
		}
		_, _, *out = client.Login(r.Context())
		w.WriteHeader(http.StatusOK)
	}
}

// Cancellation must survive the value stripping: a demo daemon should still
// abandon a PVE call when the caller hangs up.
func TestTransportPreservesCancellation(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatalf("demo.New: %v", err)
	}
	client, err := pve.New(pve.Config{
		APIURL: APIURL, Auth: pve.AuthTicket,
		Username: TicketUsername, Password: TicketPassword,
		HTTPClient: m.HTTPClient(),
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.ClusterStatus(ctx); err == nil {
		t.Fatal("a cancelled context did not abort the in-process call; cancellation was stripped along with the values")
	}
}

// --- the flow seeder -------------------------------------------------------

type recordingIngester struct{ batches [][]flow.Record }

func (r *recordingIngester) Ingest(_ context.Context, records []flow.Record) {
	r.batches = append(r.batches, records)
}

func TestRunFlowSeederSeedsImmediatelyAndStops(t *testing.T) {
	ds, err := LoadDataset()
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	ing := &recordingIngester{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- RunFlowSeeder(ctx, ds.Flows, ing, time.Hour, nil) }()

	// The first seed is synchronous, before the ticker — cancel and the
	// actor must still have delivered it.
	cancel()
	select {
	case seedErr := <-done:
		if seedErr != nil {
			t.Fatalf("RunFlowSeeder returned %v; a cancelled actor returns nil", seedErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunFlowSeeder did not return after its context was cancelled")
	}
	if len(ing.batches) == 0 {
		t.Fatal("RunFlowSeeder ingested nothing before returning; the Flow Explorer would be empty until the first tick")
	}
	if got := len(ing.batches[0]); got != len(ds.Flows.Flows) {
		t.Errorf("first seed ingested %d records, want %d", got, len(ds.Flows.Flows))
	}
}
