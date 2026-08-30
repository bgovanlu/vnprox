// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// This is the test the SDN DNS poll never had, and its absence is most of why
// T-4112's bug survived: `internal/collect` called
// GET /cluster/sdn/dns/{zone}/records once per zone on every cycle against a
// route no PVE has, and the only thing that ever answered it was
// internal/pvemock — which served the invented route because it and the
// client were written from the same wrong source.
//
// The stub below deliberately does NOT serve any record route. If the poll
// ever asks PVE for records again, it gets a 404 and contributes nothing, and
// the assertions fail.

// dnsPollStubs wraps the mock PVE server, overriding just enough for one SDN
// zone (`zone1`) to register the domain `lab.example` through a PowerDNS
// connection (`pdns1`) that a second stub actually serves.
func dnsPollStubs(t *testing.T, mock *pvemock.Server, rrsets []powerdns.RRSet) http.Handler {
	t.Helper()

	pdns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/zones/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(powerdns.Zone{
			ID: "lab.example.", Name: "lab.example.", RRSets: rrsets,
		})
	}))
	t.Cleanup(pdns.Close)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/cluster/sdn/dns"):
			write([]pve.SDNDnsPlugin{{ID: "pdns1", Type: "powerdns"}})
		case strings.Contains(r.URL.Path, "/cluster/sdn/dns/pdns1"):
			write(pve.SDNDnsPlugin{
				ID: "pdns1", Type: "powerdns",
				URL: pdns.URL + "/api/v1/servers/localhost", Key: "k", TTL: 600,
			})
		case strings.HasSuffix(r.URL.Path, "/cluster/sdn/zones"):
			write([]pve.SDNZone{{ID: "zone1", Type: "simple", DnsZone: "lab.example", DNS: "pdns1"}})
		default:
			mock.ServeHTTP(w, r)
		}
	})
}

func TestSDNPoll_DNSRecordsComeFromPowerDNS(t *testing.T) {
	mock := loadFixtureServer(t, fixtureSingleNode)
	handler := dnsPollStubs(t, mock, []powerdns.RRSet{
		{Name: "lab.example.", Type: "SOA", Records: []powerdns.Record{{Content: "ns1. hm. 1 2 3 4 5"}}},
		{Name: "web.lab.example.", Type: "A", TTL: 300, Records: []powerdns.Record{{Content: "10.0.0.5"}, {Content: "10.0.0.6"}}},
	})
	c, graph, _ := newTestCollectorHandler(t, mock, handler)

	if _, err := c.RefreshNow(context.Background(), inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}

	snap := graph.Snapshot()
	var zone *inventory.SdnDnsZone
	var record *inventory.SdnDnsRecord
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.SdnDnsZone:
			zone = v
		case *inventory.SdnDnsRecord:
			record = v
		}
	}

	if zone == nil {
		t.Fatal("the poll produced no SdnDnsZone — the domain the SDN zone registers was not derived")
	}
	if zone.ID != "lab.example." || zone.DNS != "pdns1" {
		t.Errorf("zone = %+v, want the canonical domain served by pdns1", zone)
	}

	if record == nil {
		t.Fatal("the poll produced no SdnDnsRecord — this is exactly the state that made the PTR audit report every zone unreadable")
	}
	if record.Name != "web" || record.Type != "A" {
		t.Fatalf("record = %+v, want the A record with a zone-relative name", record)
	}
	// Both addresses, not just the first: an rrset is one entity carrying the
	// whole value set.
	if len(record.Values) != 2 {
		t.Errorf("values = %v, want both addresses of the rrset", record.Values)
	}
	// SOA is PowerDNS's own bookkeeping and must not appear as a record an
	// operator manages.
	for _, e := range snap.All() {
		if r, ok := e.(*inventory.SdnDnsRecord); ok && r.Type == "SOA" {
			t.Errorf("the poll surfaced the zone's SOA as a managed record: %+v", r)
		}
	}
}

// A cluster with no PowerDNS connection configured contributes nothing and
// does not fail the SDN poll — the ordinary state of a cluster that does not
// use SDN DNS, and it must not look like an error.
func TestSDNPoll_NoDNSPluginContributesNothing(t *testing.T) {
	mock := loadFixtureServer(t, fixtureSingleNode)
	c, graph, _ := newTestCollector(t, mock)

	if _, err := c.RefreshNow(context.Background(), inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	for _, e := range graph.Snapshot().All() {
		switch e.(type) {
		case *inventory.SdnDnsZone, *inventory.SdnDnsRecord:
			t.Errorf("an unconfigured cluster produced a DNS entity: %+v", e)
		}
	}
}
