// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// These tests stand up BOTH servers a DNS record apply talks to: a PVE stub
// for the configuration (which SDN zone registers which domain, through which
// PowerDNS connection) and a PowerDNS stub for the records themselves.
//
// That pairing is the point. Before T-4112 an applied sdn.dns.record.* op
// POSTed to /cluster/sdn/dns/{zone}/records — a route no PVE has — and every
// test passed because internal/pvemock served it. A test that stands up the
// real second server cannot make that mistake quietly: if the op talks to the
// wrong host or the wrong path, nothing answers.

// dnsStubs returns a gateway wired to a PVE stub describing one SDN zone
// (`zone1`, domain `lab.example`, plugin `pdns1`) and a PowerDNS stub serving
// that domain with the given rrsets. Every PATCH the gateway sends is
// appended to the returned slice.
func dnsStubs(t *testing.T, rrsets []powerdns.RRSet) (*pveGateway, *[]powerdns.RRSet) {
	t.Helper()

	var patched []powerdns.RRSet
	pdns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/zones/") {
			t.Errorf("PowerDNS stub got an unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(powerdns.Zone{
				ID: "lab.example.", Name: "lab.example.", RRSets: rrsets,
			})
		case http.MethodPatch:
			var body struct {
				RRSets []powerdns.RRSet `json:"rrsets"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			patched = append(patched, body.RRSets...)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(pdns.Close)

	pveStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) {
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
		case strings.HasSuffix(r.URL.Path, "/cluster/sdn/vnets"):
			write([]pve.SDNVnet{})
		default:
			// Anything else means the apply is asking PVE a question it has
			// no business asking — most importantly, a record read or write.
			t.Errorf("PVE stub got an unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(pveStub.Close)

	client, err := pve.New(pve.Config{
		APIURL: pveStub.URL, Auth: pve.AuthAPIToken,
		TokenValue: "vnprox@pve!daemon=00000000-0000-0000-0000-000000000000",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return &pveGateway{client: client}, &patched
}

func dnsRecordOp(t *testing.T, id string, params change.Params) change.Op {
	t.Helper()
	return change.Op{
		Target: inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: id},
		Params: params,
	}
}

// The create reaches PowerDNS as a REPLACE carrying the new value — not a PVE
// route, and not a PowerDNS "create" (there is no such changetype).
func TestSDNStageOp_DNSRecordCreateWritesToPowerDNS(t *testing.T) {
	gw, patched := dnsStubs(t, nil)

	err := gw.SDNStageOp(context.Background(),
		dnsRecordOp(t, "lab.example/web/A", &change.SdnDnsRecordCreateParams{
			Zone: "lab.example", Name: "web", Type: "A", Value: "10.0.0.5",
		}), "")
	if err != nil {
		t.Fatalf("SDNStageOp: %v", err)
	}

	if len(*patched) != 1 {
		t.Fatalf("PowerDNS received %d rrset changes, want 1: %+v", len(*patched), *patched)
	}
	got := (*patched)[0]
	if got.ChangeType != powerdns.ChangeReplace {
		t.Errorf("changetype = %q, want REPLACE", got.ChangeType)
	}
	if got.Name != "web.lab.example." {
		t.Errorf("name = %q, want the fully-qualified rrset name", got.Name)
	}
	if len(got.Records) != 1 || got.Records[0].Content != "10.0.0.5" {
		t.Errorf("records = %+v, want the one address", got.Records)
	}
	// The plugin's ttl applies when the op carries none — matching
	// PowerdnsPlugin.pm, so a record vnprox writes and one PVE writes to the
	// same zone do not differ in TTL for no reason.
	if got.TTL != 600 {
		t.Errorf("ttl = %d, want the plugin's 600", got.TTL)
	}
}

// Creating a value that is already there issues no request at all, which is
// PowerdnsPlugin.pm's own early return. Re-applying a realized changeset must
// touch nothing.
func TestSDNStageOp_DNSRecordCreateThatIsAlreadyRealizedWritesNothing(t *testing.T) {
	gw, patched := dnsStubs(t, []powerdns.RRSet{{
		Name: "web.lab.example.", Type: "A", TTL: 600,
		Records: []powerdns.Record{{Content: "10.0.0.5"}},
	}})

	err := gw.SDNStageOp(context.Background(),
		dnsRecordOp(t, "lab.example/web/A", &change.SdnDnsRecordCreateParams{
			Zone: "lab.example", Name: "web", Type: "A", Value: "10.0.0.5",
		}), "")
	if err != nil {
		t.Fatalf("SDNStageOp: %v", err)
	}
	if len(*patched) != 0 {
		t.Errorf("an already-realized create wrote %+v, want no request", *patched)
	}
}

// Deleting the only record must DELETE the rrset. A REPLACE with an empty
// record list is not how PowerDNS removes one, so getting this wrong leaves
// the record in place and reports success.
func TestSDNStageOp_DNSRecordDeleteRemovesTheRRSet(t *testing.T) {
	gw, patched := dnsStubs(t, []powerdns.RRSet{{
		Name: "web.lab.example.", Type: "A", TTL: 600,
		Records: []powerdns.Record{{Content: "10.0.0.5"}},
	}})

	err := gw.SDNStageOp(context.Background(),
		dnsRecordOp(t, "lab.example/web/A", &change.SdnDnsRecordDeleteParams{}), "")
	if err != nil {
		t.Fatalf("SDNStageOp: %v", err)
	}
	if len(*patched) != 1 || (*patched)[0].ChangeType != powerdns.ChangeDelete {
		t.Fatalf("patched = %+v, want one DELETE", *patched)
	}
}

// An update against a multi-valued rrset REFUSES rather than replacing the
// set with one value. The op names one value and PowerDNS's write unit is the
// whole rrset; honouring it would delete two addresses and report success.
func TestSDNStageOp_DNSRecordUpdateRefusesAMultiValuedRRSet(t *testing.T) {
	gw, patched := dnsStubs(t, []powerdns.RRSet{{
		Name: "web.lab.example.", Type: "A", TTL: 600,
		Records: []powerdns.Record{{Content: "10.0.0.5"}, {Content: "10.0.0.6"}},
	}})

	newValue := "10.0.0.9"
	err := gw.SDNStageOp(context.Background(),
		dnsRecordOp(t, "lab.example/web/A", &change.SdnDnsRecordUpdateParams{Value: &newValue}), "")
	if err == nil {
		t.Fatal("want a refusal, not a silent two-record deletion")
	}
	for _, addr := range []string{"10.0.0.5", "10.0.0.6"} {
		if !strings.Contains(err.Error(), addr) {
			t.Errorf("error %q does not name %s, which would have been destroyed", err, addr)
		}
	}
	if len(*patched) != 0 {
		t.Errorf("a refused update still wrote %+v", *patched)
	}
}

// A record naming a domain no SDN zone registers is refused, and the error
// says what vnprox does know. Writing to it would put records where PVE will
// never look — the same class of mistake as the invented routes.
func TestSDNStageOp_DNSRecordInAnUnknownDomainIsRefused(t *testing.T) {
	gw, patched := dnsStubs(t, nil)

	err := gw.SDNStageOp(context.Background(),
		dnsRecordOp(t, "other.example/web/A", &change.SdnDnsRecordCreateParams{
			Zone: "other.example", Name: "web", Type: "A", Value: "10.0.0.5",
		}), "")
	if err == nil {
		t.Fatal("want a refusal for a domain no SDN zone registers")
	}
	if !strings.Contains(err.Error(), "lab.example.") {
		t.Errorf("error %q does not say which domains vnprox knows", err)
	}
	if len(*patched) != 0 {
		t.Errorf("a refused op still wrote %+v", *patched)
	}
}
