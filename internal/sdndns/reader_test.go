// SPDX-License-Identifier: Apache-2.0

package sdndns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// These tests stand up a real HTTP server speaking PowerDNS's shapes, rather
// than a double of this package's own seam. That is the point: internal/pvemock
// served the routes vnprox invented, so mock and caller agreed with each other
// and with nothing real. A test that speaks the wire cannot make that mistake
// silently — if the path or the envelope is wrong, the server returns 404 or
// decodes nothing.

type fakePVE struct {
	listErr  error
	getErr   error
	plugins  []pve.SDNDnsPlugin
	getCalls int
}

func (f *fakePVE) ListSDNDnsPlugins(context.Context) ([]pve.SDNDnsPlugin, error) {
	return f.plugins, f.listErr
}

func (f *fakePVE) GetSDNDnsPlugin(_ context.Context, id string) (pve.SDNDnsPlugin, error) {
	f.getCalls++
	if f.getErr != nil {
		return pve.SDNDnsPlugin{}, f.getErr
	}
	for _, p := range f.plugins {
		if p.ID == id {
			return p, nil
		}
	}
	return pve.SDNDnsPlugin{}, errors.New("not found")
}

// pdnsServer serves one zone's rrsets and records any PATCH it receives.
func pdnsServer(t *testing.T, zone powerdns.Zone) (*httptest.Server, *[]powerdns.RRSet) {
	t.Helper()
	var patched []powerdns.RRSet
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/servers/localhost/zones/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(zone)
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
	t.Cleanup(srv.Close)
	return srv, &patched
}

func pluginAt(url string) pve.SDNDnsPlugin {
	return pve.SDNDnsPlugin{ID: "pdns", Type: "powerdns", URL: url + "/api/v1/servers/localhost", Key: "k", TTL: 3600}
}

func TestReader_ReadsRecordsFromPowerDNS(t *testing.T) {
	srv, _ := pdnsServer(t, powerdns.Zone{
		ID: "example.com.", Name: "example.com.",
		RRSets: []powerdns.RRSet{
			{Name: "example.com.", Type: "SOA", Records: []powerdns.Record{{Content: "ns1. hm. 1 2 3 4 5"}}},
			{Name: "web.example.com.", Type: "A", TTL: 300, Records: []powerdns.Record{{Content: "10.0.0.5"}}},
		},
	})
	plugin := pluginAt(srv.URL)
	r := NewReader(&fakePVE{plugins: []pve.SDNDnsPlugin{plugin}}, nil)

	recs, err := r.Records(context.Background(), Zone{Domain: "example.com.", Plugin: "pdns"}, plugin)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 1 || recs[0].Name != "web" || recs[0].Value != "10.0.0.5" {
		t.Fatalf("records = %+v, want the one A record with a zone-relative name", recs)
	}
}

func TestReader_PatchReachesTheServer(t *testing.T) {
	srv, patched := pdnsServer(t, powerdns.Zone{ID: "example.com."})
	plugin := pluginAt(srv.URL)
	r := NewReader(&fakePVE{plugins: []pve.SDNDnsPlugin{plugin}}, nil)

	err := r.Patch(context.Background(), "example.com.", plugin,
		[]powerdns.RRSet{powerdns.SetSingle("web.example.com.", "A", "10.0.0.9", 300)})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(*patched) != 1 || (*patched)[0].Records[0].Content != "10.0.0.9" {
		t.Fatalf("server received %+v, want the one REPLACE", *patched)
	}
}

// A 404 from PowerDNS means "this server does not serve that zone", which is
// a configuration mistake with an obvious fix. It has to stay distinguishable
// from an unreachable server all the way up to the caller, because collapsing
// them is what left the PTR audit unable to say anything useful.
func TestReader_ZoneNotFoundStaysRecognisable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Could not find domain"}`))
	}))
	t.Cleanup(srv.Close)
	plugin := pluginAt(srv.URL)
	r := NewReader(&fakePVE{plugins: []pve.SDNDnsPlugin{plugin}}, nil)

	_, err := r.Records(context.Background(), Zone{Domain: "nope.example.", Plugin: "pdns"}, plugin)
	if err == nil {
		t.Fatal("want an error")
	}
	if !powerdns.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false — the wrapping lost the status", err)
	}
}

// The client cache is keyed on the connection details, not the instance id.
// An operator who repoints a plugin at a new server, or rotates its key, must
// get a new client on the next poll rather than keep talking to the old one
// until vnproxd restarts.
func TestReader_ClientCacheIsKeyedOnTheConnection(t *testing.T) {
	var dialed []powerdns.Config
	dial := func(cfg powerdns.Config) (*powerdns.Client, error) {
		dialed = append(dialed, cfg)
		return powerdns.New(cfg)
	}
	r := NewReader(&fakePVE{}, dial)

	base := pve.SDNDnsPlugin{ID: "pdns", URL: "https://a.example:8081/api/v1/servers/localhost", Key: "k1"}
	if _, err := r.client(base); err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := r.client(base); err != nil {
		t.Fatalf("client: %v", err)
	}
	if len(dialed) != 1 {
		t.Fatalf("dialed %d times for an unchanged plugin, want 1", len(dialed))
	}

	rotated := base
	rotated.Key = "k2"
	if _, err := r.client(rotated); err != nil {
		t.Fatalf("client: %v", err)
	}
	if len(dialed) != 2 {
		t.Errorf("a rotated key reused the cached client — dialed %d times, want 2", len(dialed))
	}
}

// A dial failure is cached: it can only change when the operator edits the
// plugin config, so retrying it per zone per poll would produce identical
// errors at the cost of drowning the real ones.
func TestReader_DialFailureIsCachedAndExplains(t *testing.T) {
	calls := 0
	dial := func(cfg powerdns.Config) (*powerdns.Client, error) {
		calls++
		return powerdns.New(cfg)
	}
	r := NewReader(&fakePVE{}, dial)
	broken := pve.SDNDnsPlugin{ID: "pdns", URL: ""} // no url: not dialable

	if _, err := r.client(broken); err == nil {
		t.Fatal("want an error for a plugin with no url")
	}
	_, err1 := r.client(broken)
	if err1 == nil {
		t.Fatal("want an error for a plugin with no url")
	}
	if !strings.Contains(err1.Error(), "pdns") {
		t.Errorf("error = %v, want it to name the plugin instance", err1)
	}
	if calls != 1 {
		t.Errorf("dial attempted %d times, want the failure cached after 1", calls)
	}
}

// Plugins re-reads each instance individually, because the index route's
// declared schema names only dns and type — trusting the list to carry url
// and key would be assuming a shape rather than reading one.
func TestPlugins_ReadsEachInstanceIndividually(t *testing.T) {
	f := &fakePVE{plugins: []pve.SDNDnsPlugin{
		{ID: "pdns1", Type: "powerdns"},
		{ID: "pdns2", Type: "powerdns"},
	}}
	got, err := NewReader(f, nil).Plugins(context.Background())
	if err != nil {
		t.Fatalf("Plugins: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("plugins = %+v, want both", got)
	}
	if f.getCalls != 2 {
		t.Errorf("per-instance reads = %d, want 2", f.getCalls)
	}
}

// A cluster with no DNS plugin configured is the ordinary case, not a
// failure, and must not cost three SDN reads to discover nothing.
func TestService_NoPluginsSkipsTheSDNReads(t *testing.T) {
	topo := &countingTopo{}
	svc := NewService(topo, &fakePVE{}, nil)

	zones, skipped, err := svc.Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}
	if len(zones) != 0 || len(skipped) != 0 {
		t.Errorf("zones=%+v skipped=%+v, want both empty", zones, skipped)
	}
	if topo.calls != 0 {
		t.Errorf("made %d SDN reads with no DNS plugin configured, want 0", topo.calls)
	}
}

type countingTopo struct {
	calls int
}

func (c *countingTopo) ListSDNZones(context.Context) ([]pve.SDNZone, error) {
	c.calls++
	return nil, nil
}

func (c *countingTopo) ListSDNVnets(context.Context) ([]pve.SDNVnet, error) {
	c.calls++
	return nil, nil
}

func (c *countingTopo) ListSDNSubnets(context.Context, string) ([]pve.SDNSubnet, error) {
	c.calls++
	return nil, nil
}
