// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// --- fake external IPAM HTTP double (T-1203 deliverable: a fake
// NetBox/phpIPAM HTTP double with controllable write acceptance/rejection) ---

// fakeExternalIPAMServer is an httptest-backed stand-in for a NetBox/phpIPAM
// address API: GET /records returns every held record, POST /records creates
// one, DELETE /records/{ip} removes one. rejectWrites, when set, makes every
// mutating call answer 403 (the "external system rejects the write" scenario)
// and leaves state unchanged.
type fakeExternalIPAMServer struct {
	server       *httptest.Server
	records      map[string]ipam.ExternalRecord
	mu           sync.Mutex
	writes       int
	rejectWrites bool
}

func newFakeExternalIPAMServer(seed []ipam.ExternalRecord) *fakeExternalIPAMServer {
	f := &fakeExternalIPAMServer{records: map[string]ipam.ExternalRecord{}}
	for _, r := range seed {
		f.records[r.IP] = r
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/records", f.handleRecords)
	mux.HandleFunc("/records/", f.handleRecordByIP)
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeExternalIPAMServer) close() { f.server.Close() }

func (f *fakeExternalIPAMServer) snapshot() []ipam.ExternalRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ipam.ExternalRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

func (f *fakeExternalIPAMServer) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *fakeExternalIPAMServer) handleRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(f.snapshot())
	case http.MethodPost:
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.rejectWrites {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var rec ipam.ExternalRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.writes++
		f.records[rec.IP] = rec
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeExternalIPAMServer) handleRecordByIP(w http.ResponseWriter, r *http.Request) {
	ip := strings.TrimPrefix(r.URL.Path, "/records/")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rejectWrites {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		f.writes++
		delete(f.records, ip)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		var rec ipam.ExternalRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.writes++
		f.records[ip] = rec
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// httpExternalClient is a thin ipam.ExternalIPAMClient that talks to the fake
// server over real HTTP — so the sync engine is exercised end-to-end through
// an HTTP round-trip, not a bare in-process interface.
type httpExternalClient struct {
	base string
}

func (c httpExternalClient) ListRecords(ctx context.Context) ([]ipam.ExternalRecord, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/records", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out []ipam.ExternalRecord
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c httpExternalClient) CreateRecord(ctx context.Context, rec ipam.ExternalRecord) error {
	body, _ := json.Marshal(rec)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/records", strings.NewReader(string(body)))
	return c.do(req)
}

func (c httpExternalClient) UpdateRecord(ctx context.Context, rec ipam.ExternalRecord) error {
	body, _ := json.Marshal(rec)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/records/"+rec.IP, strings.NewReader(string(body)))
	return c.do(req)
}

func (c httpExternalClient) DeleteRecord(ctx context.Context, ip string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/records/"+ip, nil)
	return c.do(req)
}

func (c httpExternalClient) do(req *http.Request) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return errors.New("external IPAM write rejected: " + resp.Status)
	}
	return nil
}

// --- fake PVE reader with a controlled allocation set ---

type fakeSyncPVE struct {
	entries []pve.IPAMEntry
}

func (f fakeSyncPVE) ListSDNZones(context.Context) ([]pve.SDNZone, error) { return nil, nil }
func (f fakeSyncPVE) ListSDNVnets(context.Context) ([]pve.SDNVnet, error) { return nil, nil }
func (f fakeSyncPVE) ListSDNSubnets(context.Context, string) ([]pve.SDNSubnet, error) {
	return nil, nil
}
func (f fakeSyncPVE) ListIPAMs(context.Context) ([]pve.IPAM, error) {
	return []pve.IPAM{{ID: "pve", Type: "pve"}}, nil
}
func (f fakeSyncPVE) GetIPAMStatus(context.Context, string) ([]pve.IPAMEntry, error) {
	return f.entries, nil
}
func (f fakeSyncPVE) GetGuestAgentInterfaces(context.Context, string, int) ([]pve.AgentIface, error) {
	return nil, nil
}

func newSyncService(t *testing.T, entries []pve.IPAMEntry, client ipam.ExternalIPAMClient) *ipam.Service {
	t.Helper()
	return ipam.NewService(ipam.Config{
		PVE:          fakeSyncPVE{entries: entries},
		Inventory:    ipamLabInventory(),
		ExternalIPAM: client,
	})
}

// vnprox holds .10 (web1) and .20 (web2); external holds .10 (agreeing) and
// .30 (unknown to vnprox). So: .20 is an add, .30 a remove, and if external
// disagrees on .10's hostname it is a conflict.
func syncFixtureEntries() []pve.IPAMEntry {
	return []pve.IPAMEntry{
		{Subnet: "10.50.0.0/24", IP: "10.50.0.1", Gateway: true},     // gateway — dropped
		{Subnet: "10.50.0.0/24", IP: "10.50.0.10", Hostname: "web1"}, // in both
		{Subnet: "10.50.0.0/24", IP: "10.50.0.20", Hostname: "web2"}, // add
	}
}

// TestExternalSyncPreview_NoWrite is T-1203 AC3 (preview half): preview
// surfaces additions/removals/conflicts and never writes (double state
// unchanged).
func TestExternalSyncPreview_NoWrite(t *testing.T) {
	ext := newFakeExternalIPAMServer([]ipam.ExternalRecord{
		{IP: "10.50.0.10", Hostname: "web1"},   // agrees -> no change
		{IP: "10.50.0.30", Hostname: "legacy"}, // unknown to vnprox -> remove
	})
	defer ext.close()
	svc := newSyncService(t, syncFixtureEntries(), httpExternalClient{base: ext.server.URL})

	plan, err := svc.ExternalSyncPreview(context.Background())
	if err != nil {
		t.Fatalf("ExternalSyncPreview: %v", err)
	}
	kinds := map[ipam.SyncChangeKind][]string{}
	for _, c := range plan.Changes {
		kinds[c.Kind] = append(kinds[c.Kind], c.IP)
	}
	if got := kinds[ipam.SyncAdd]; len(got) != 1 || got[0] != "10.50.0.20" {
		t.Errorf("adds = %v, want [10.50.0.20]", got)
	}
	if got := kinds[ipam.SyncRemove]; len(got) != 1 || got[0] != "10.50.0.30" {
		t.Errorf("removes = %v, want [10.50.0.30]", got)
	}
	// Preview wrote nothing.
	if n := ext.writeCount(); n != 0 {
		t.Errorf("preview issued %d external writes, want 0", n)
	}
	if len(ext.snapshot()) != 2 {
		t.Errorf("external state changed during preview: %+v", ext.snapshot())
	}
}

// TestExternalSyncApply_ConfirmRequired is T-1203 AC3 (confirm half):
// confirm false/omitted → error (mapped to 400 at the API), no write.
func TestExternalSyncApply_ConfirmRequired(t *testing.T) {
	ext := newFakeExternalIPAMServer(nil)
	defer ext.close()
	svc := newSyncService(t, syncFixtureEntries(), httpExternalClient{base: ext.server.URL})

	_, err := svc.ExternalSyncApply(context.Background(), false)
	if !errors.Is(err, ipam.ErrSyncConfirmRequired) {
		t.Fatalf("apply with confirm=false: err = %v, want ErrSyncConfirmRequired", err)
	}
	if n := ext.writeCount(); n != 0 {
		t.Errorf("apply with confirm=false issued %d writes, want 0", n)
	}
}

// TestExternalSyncApply_Writes is T-1203 AC3 (apply half): apply{confirm:true}
// writes and returns before/after per record for the audit trail.
func TestExternalSyncApply_Writes(t *testing.T) {
	ext := newFakeExternalIPAMServer([]ipam.ExternalRecord{
		{IP: "10.50.0.10", Hostname: "web1"},
		{IP: "10.50.0.30", Hostname: "legacy"},
	})
	defer ext.close()
	svc := newSyncService(t, syncFixtureEntries(), httpExternalClient{base: ext.server.URL})

	res, err := svc.ExternalSyncApply(context.Background(), true)
	if err != nil {
		t.Fatalf("ExternalSyncApply: %v", err)
	}
	// One add (.20) and one remove (.30) applied; each carries before/after.
	if len(res.Applied) != 2 {
		t.Fatalf("applied %d records, want 2: %+v", len(res.Applied), res.Applied)
	}
	for _, r := range res.Applied {
		if !r.OK {
			t.Errorf("record %s failed: %s", r.IP, r.Error)
		}
		switch r.Kind {
		case ipam.SyncAdd:
			if r.After == nil || r.Before != nil {
				t.Errorf("add %s: want After set, Before nil; got before=%v after=%v", r.IP, r.Before, r.After)
			}
		case ipam.SyncRemove:
			if r.Before == nil || r.After != nil {
				t.Errorf("remove %s: want Before set, After nil; got before=%v after=%v", r.IP, r.Before, r.After)
			}
		}
	}
	// External state now matches vnprox: .10 and .20 present, .30 gone.
	got := map[string]string{}
	for _, r := range ext.snapshot() {
		got[r.IP] = r.Hostname
	}
	if _, ok := got["10.50.0.30"]; ok {
		t.Errorf("10.50.0.30 not removed: %+v", got)
	}
	if got["10.50.0.20"] != "web2" {
		t.Errorf("10.50.0.20 not added: %+v", got)
	}
}

// TestExternalSyncApply_WriteRejected proves a rejected external write is
// surfaced per-record (OK false + error) rather than silently dropped, and
// never panics.
func TestExternalSyncApply_WriteRejected(t *testing.T) {
	ext := newFakeExternalIPAMServer([]ipam.ExternalRecord{{IP: "10.50.0.30", Hostname: "legacy"}})
	ext.rejectWrites = true
	defer ext.close()
	svc := newSyncService(t, syncFixtureEntries(), httpExternalClient{base: ext.server.URL})

	res, err := svc.ExternalSyncApply(context.Background(), true)
	if err != nil {
		t.Fatalf("ExternalSyncApply: %v", err)
	}
	for _, r := range res.Applied {
		if r.OK {
			t.Errorf("record %s reported OK despite the double rejecting writes", r.IP)
		}
		if r.Error == "" {
			t.Errorf("record %s: rejected write carried no error string", r.IP)
		}
	}
}

// TestExternalSyncFindings_Conflict is T-1203 AC4: a hostname disagreement on
// one address surfaces as an ipam-source finding, check external_ipam_conflict,
// not fixable, docsLink set.
func TestExternalSyncFindings_Conflict(t *testing.T) {
	ext := newFakeExternalIPAMServer([]ipam.ExternalRecord{
		{IP: "10.50.0.10", Hostname: "DIFFERENT"}, // disagrees with vnprox's web1
		{IP: "10.50.0.20", Hostname: "web2"},      // agrees -> no finding
	})
	defer ext.close()
	svc := newSyncService(t, syncFixtureEntries(), httpExternalClient{base: ext.server.URL})

	found, err := svc.ExternalSyncFindings(context.Background())
	if err != nil {
		t.Fatalf("ExternalSyncFindings: %v", err)
	}
	var conflict *ipam.SyncFinding
	for i := range found {
		if found[i].Check == "external_ipam_conflict" {
			conflict = &found[i]
		}
	}
	if conflict == nil {
		t.Fatalf("no external_ipam_conflict finding produced: %+v", found)
	}
	if conflict.IP != "10.50.0.10" {
		t.Errorf("conflict IP = %q, want 10.50.0.10", conflict.IP)
	}
	if conflict.DocsLink == "" {
		t.Error("conflict finding has no docsLink")
	}
}

func TestExternalSync_NotConfigured(t *testing.T) {
	svc := newSyncService(t, syncFixtureEntries(), nil)
	if _, err := svc.ExternalSyncPreview(context.Background()); !errors.Is(err, ipam.ErrSyncNotConfigured) {
		t.Errorf("preview with no client: err = %v, want ErrSyncNotConfigured", err)
	}
	// Findings degrade to empty (not an error) when sync isn't configured.
	found, err := svc.ExternalSyncFindings(context.Background())
	if err != nil || len(found) != 0 {
		t.Errorf("findings with no client = (%v, %v), want (nil, empty)", found, err)
	}
}
