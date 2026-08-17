package ipam_test

// T-3104 item 3: NetBoxClient/PhpIPAMClient are modeled from each system's
// public API documentation (see netbox.go/phpipam.go's own package doc
// comments for exactly which surface and what is flagged as unverified) —
// there is no real instance to test against, so these tests pin this
// package's own request/response shape assumptions against a fake HTTP
// server, the same "no real hardware, test the assumption itself" posture
// sync_test.go's own httpExternalClient test double already uses one layer
// up (against ExternalIPAMClient, not a concrete implementation).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

func TestNetBoxClient_ListCreateUpdateDelete(t *testing.T) {
	type stored struct {
		address string
		dns     string
	}
	records := map[int]*stored{1: {address: "10.0.0.5/32", dns: "host1"}}
	nextID := 2

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ipam/ip-addresses/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret-token" {
			t.Errorf("missing/wrong Authorization header: %q", r.Header.Get("Authorization"))
		}
		switch r.Method {
		case http.MethodGet:
			addr := r.URL.Query().Get("address")
			type result struct {
				Address string `json:"address"`
				DNSName string `json:"dns_name"`
				ID      int    `json:"id"`
			}
			var results []result
			for id, rec := range records {
				if addr != "" && rec.address != addr {
					continue
				}
				results = append(results, result{ID: id, Address: rec.address, DNSName: rec.dns})
			}
			sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(results), "next": nil, "results": results})
		case http.MethodPost:
			var body struct {
				Address string `json:"address"`
				DNSName string `json:"dns_name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := nextID
			nextID++
			records[id] = &stored{address: body.Address, dns: body.DNSName}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		default:
			t.Fatalf("unexpected method %s on collection path", r.Method)
		}
	})
	mux.HandleFunc("/api/ipam/ip-addresses/2/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				DNSName string `json:"dns_name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			records[2].dns = body.DNSName
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 2})
		case http.MethodDelete:
			delete(records, 2)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s on item path", r.Method)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := ipam.NewNetBoxClient(ipam.ExternalHTTPConfig{BaseURL: srv.URL, Token: "secret-token", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("NewNetBoxClient: %v", err)
	}

	got, err := client.ListRecords(t.Context())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(got) != 1 || got[0].IP != "10.0.0.5" || got[0].Hostname != "host1" {
		t.Fatalf("ListRecords = %+v, want one 10.0.0.5/host1 record", got)
	}

	if err := client.CreateRecord(t.Context(), ipam.ExternalRecord{IP: "10.0.0.6", Hostname: "host2"}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if records[2] == nil || records[2].address != "10.0.0.6/32" || records[2].dns != "host2" {
		t.Fatalf("after create, records[2] = %+v", records[2])
	}

	if err := client.UpdateRecord(t.Context(), ipam.ExternalRecord{IP: "10.0.0.6", Hostname: "host2-renamed"}); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if records[2].dns != "host2-renamed" {
		t.Fatalf("after update, dns = %q, want host2-renamed", records[2].dns)
	}

	if err := client.DeleteRecord(t.Context(), "10.0.0.6"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if _, ok := records[2]; ok {
		t.Fatalf("record 2 still present after delete")
	}
}

func TestPhpIPAMClient_ListAggregatesAcrossSubnets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/myapp/subnets/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "secret-token" {
			t.Errorf("missing/wrong token header: %q", r.Header.Get("token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": []map[string]string{
				{"id": "10", "subnet": "10.0.0.0", "mask": "24"},
				{"id": "11", "subnet": "10.1.0.0", "mask": "24"},
			},
		})
	})
	mux.HandleFunc("/api/myapp/subnets/10/addresses/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": []map[string]string{
				{"id": "1", "subnetId": "10", "ip": "10.0.0.5", "hostname": "host1"},
			},
		})
	})
	mux.HandleFunc("/api/myapp/subnets/11/addresses/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "No addresses found"})
	})
	mux.HandleFunc("/api/myapp/addresses/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var body struct {
			SubnetID string `json:"subnetId"`
			IP       string `json:"ip"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.SubnetID != "11" {
			t.Fatalf("create resolved subnetId = %q, want 11 (10.1.0.9 is in 10.1.0.0/24)", body.SubnetID)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := ipam.NewPhpIPAMClient(ipam.ExternalHTTPConfig{BaseURL: srv.URL, Token: "secret-token", HTTPClient: srv.Client()}, "myapp")
	if err != nil {
		t.Fatalf("NewPhpIPAMClient: %v", err)
	}

	got, err := client.ListRecords(t.Context())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(got) != 1 || got[0].IP != "10.0.0.5" || got[0].Hostname != "host1" {
		t.Fatalf("ListRecords = %+v, want one 10.0.0.5/host1 record (empty subnet 11 contributes nothing)", got)
	}

	if err := client.CreateRecord(t.Context(), ipam.ExternalRecord{IP: "10.1.0.9", Hostname: "host9"}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
}

func TestPhpIPAMClient_CreateRecord_NoContainingSubnet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/myapp/subnets/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]string{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := ipam.NewPhpIPAMClient(ipam.ExternalHTTPConfig{BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client()}, "myapp")
	if err != nil {
		t.Fatalf("NewPhpIPAMClient: %v", err)
	}
	if err := client.CreateRecord(t.Context(), ipam.ExternalRecord{IP: "192.168.1.1", Hostname: "x"}); err == nil {
		t.Fatalf("CreateRecord with no containing subnet: want an error, got nil")
	}
}
