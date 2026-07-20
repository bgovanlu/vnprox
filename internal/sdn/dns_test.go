package sdn

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakeDNSReader is a hand-rolled DNSReader double: config zones/records are
// fixed in-memory, and resolveErr lets a test model an unreachable PowerDNS
// server for the config-vs-live duality.
type fakeDNSReader struct {
	records    map[string][]pve.SDNDnsRecord
	resolved   map[string][]pve.SDNDnsRecord
	resolveErr map[string]error
	zones      []pve.SDNDnsZone
}

func (f *fakeDNSReader) ListSDNDnsZones(context.Context) ([]pve.SDNDnsZone, error) {
	return f.zones, nil
}
func (f *fakeDNSReader) ListSDNDnsRecords(_ context.Context, zone string) ([]pve.SDNDnsRecord, error) {
	return f.records[zone], nil
}
func (f *fakeDNSReader) ResolveSDNDnsRecords(_ context.Context, zone string) ([]pve.SDNDnsRecord, error) {
	if err := f.resolveErr[zone]; err != nil {
		return nil, err
	}
	if r, ok := f.resolved[zone]; ok {
		return r, nil
	}
	return f.records[zone], nil
}

var _ DNSReader = (*fakeDNSReader)(nil)

// TestDNS_ConfiguredZone is T-1204 acceptance criterion 1: a configured DNS
// plugin returns matching zone/record data across records + resolved.
func TestDNS_ConfiguredZone(t *testing.T) {
	reader := &fakeDNSReader{
		zones: []pve.SDNDnsZone{{ID: "example.com", DNS: "powerdns", TTL: 3600}},
		records: map[string][]pve.SDNDnsRecord{
			"example.com": {
				{Name: "web1", Type: "A", Value: "10.10.0.5", TTL: 300},
				{Name: "db1", Type: "A", Value: "10.10.0.6"},
			},
		},
	}
	view, err := NewDNSService(reader).DNS(context.Background(), "")
	if err != nil {
		t.Fatalf("DNS: %v", err)
	}
	if len(view.Zones) != 1 || view.Zones[0].ID != "example.com" {
		t.Fatalf("zones = %+v", view.Zones)
	}
	if len(view.Records) != 2 {
		t.Fatalf("records = %+v", view.Records)
	}
	if view.Records[0].Name != "db1" { // sorted by name
		t.Fatalf("records not sorted: %+v", view.Records)
	}
	if view.Records[1].FQDN != "web1.example.com" {
		t.Fatalf("fqdn = %q", view.Records[1].FQDN)
	}
	if len(view.Resolved) != 2 {
		t.Fatalf("resolved = %+v", view.Resolved)
	}
}

// TestDNS_Unconfigured is T-1204 acceptance criterion 1's other half: an
// unconfigured fixture returns empty arrays, not an error.
func TestDNS_Unconfigured(t *testing.T) {
	view, err := NewDNSService(&fakeDNSReader{}).DNS(context.Background(), "")
	if err != nil {
		t.Fatalf("DNS on unconfigured fixture errored: %v", err)
	}
	if len(view.Zones) != 0 || len(view.Records) != 0 || len(view.Resolved) != 0 {
		t.Fatalf("expected empty view, got %+v", view)
	}
	// A ?zone= naming no configured zone is likewise empty, not an error.
	view, err = NewDNSService(&fakeDNSReader{
		zones: []pve.SDNDnsZone{{ID: "other.com"}},
	}).DNS(context.Background(), "missing.com")
	if err != nil {
		t.Fatalf("DNS scoped to unknown zone errored: %v", err)
	}
	if len(view.Zones) != 0 || len(view.Records) != 0 {
		t.Fatalf("expected empty scoped view, got %+v", view)
	}
}

// TestDNS_ResolveUnreachable proves the config-vs-live duality: an
// unreachable PowerDNS server yields config records but no resolved records,
// never an error.
func TestDNS_ResolveUnreachable(t *testing.T) {
	reader := &fakeDNSReader{
		zones:      []pve.SDNDnsZone{{ID: "example.com"}},
		records:    map[string][]pve.SDNDnsRecord{"example.com": {{Name: "web1", Type: "A", Value: "10.10.0.5"}}},
		resolveErr: map[string]error{"example.com": errors.New("powerdns unreachable")},
	}
	view, err := NewDNSService(reader).DNS(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("DNS: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("records = %+v", view.Records)
	}
	if len(view.Resolved) != 0 {
		t.Fatalf("expected no resolved records for unreachable server, got %+v", view.Resolved)
	}
}
