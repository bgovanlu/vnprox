// SPDX-License-Identifier: Apache-2.0

package sdn

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/sdndns"
)

// fakeDNSReader is a hand-rolled DNSReader double: the derived domains and
// their records are fixed in-memory, and readErr models a PowerDNS server
// that cannot be read.
//
// The third method this double used to have — ResolveSDNDnsRecords — is gone
// with the duality it served (T-4112). PVE keeps no record copy, so there was
// never a second source to compare against; see DNSView.Resolved.
type fakeDNSReader struct {
	records map[string][]sdndns.Record
	readErr map[string]error
	zones   []sdndns.Zone
	skipped []sdndns.Skip
}

func (f *fakeDNSReader) Zones(context.Context) ([]sdndns.Zone, []sdndns.Skip, error) {
	return f.zones, f.skipped, nil
}

func (f *fakeDNSReader) Records(_ context.Context, z sdndns.Zone) ([]sdndns.Record, error) {
	if err := f.readErr[z.Domain]; err != nil {
		return nil, err
	}
	return f.records[z.Domain], nil
}

var _ DNSReader = (*fakeDNSReader)(nil)

// TestDNS_ConfiguredZone is T-1204 acceptance criterion 1: a configured DNS
// plugin returns matching zone/record data.
func TestDNS_ConfiguredZone(t *testing.T) {
	reader := &fakeDNSReader{
		zones: []sdndns.Zone{{Domain: "example.com.", Plugin: "powerdns", SDNZone: "zone1", TTL: 3600}},
		records: map[string][]sdndns.Record{
			"example.com.": {
				{Zone: "example.com.", Name: "web1", Type: "A", Value: "10.10.0.5", Values: []string{"10.10.0.5"}, TTL: 300},
				{Zone: "example.com.", Name: "db1", Type: "A", Value: "10.10.0.6", Values: []string{"10.10.0.6"}},
			},
		},
	}
	view, err := NewDNSService(reader).DNS(context.Background(), "")
	if err != nil {
		t.Fatalf("DNS: %v", err)
	}
	if len(view.Zones) != 1 || view.Zones[0].ID != "example.com." {
		t.Fatalf("zones = %+v", view.Zones)
	}
	if view.Zones[0].SDNZone != "zone1" {
		t.Errorf("sdnZone = %q, want the SDN zone the domain came from", view.Zones[0].SDNZone)
	}
	if len(view.Records) != 2 {
		t.Fatalf("records = %+v", view.Records)
	}
	if view.Records[0].Name != "db1" { // sorted by name
		t.Fatalf("records not sorted: %+v", view.Records)
	}
	if view.Records[1].FQDN != "web1.example.com." {
		t.Fatalf("fqdn = %q", view.Records[1].FQDN)
	}
	if len(view.Unreadable) != 0 {
		t.Errorf("unreadable = %+v, want none for a healthy read", view.Unreadable)
	}
}

// Resolved is on the wire and always empty (T-4112). A test asserts it rather
// than leaving it to drift, because the field's whole purpose now is to be a
// documented, deliberate emptiness — a future change that starts filling it
// with a copy of Records would be re-asserting the cross-check that never
// happened.
func TestDNS_ResolvedIsEmptyAndSaysSo(t *testing.T) {
	reader := &fakeDNSReader{
		zones: []sdndns.Zone{{Domain: "example.com.", Plugin: "powerdns"}},
		records: map[string][]sdndns.Record{
			"example.com.": {{Zone: "example.com.", Name: "web1", Type: "A", Value: "10.10.0.5"}},
		},
	}
	view, err := NewDNSService(reader).DNS(context.Background(), "")
	if err != nil {
		t.Fatalf("DNS: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("records = %+v", view.Records)
	}
	if view.Resolved == nil {
		t.Error("resolved must be an empty array on the wire, not null")
	}
	if len(view.Resolved) != 0 {
		t.Errorf("resolved = %+v, want empty: PVE keeps no second copy to compare against", view.Resolved)
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
	// A ?zone= naming no known domain is likewise empty, not an error.
	view, err = NewDNSService(&fakeDNSReader{
		zones: []sdndns.Zone{{Domain: "other.com."}},
	}).DNS(context.Background(), "missing.com")
	if err != nil {
		t.Fatalf("DNS scoped to unknown zone errored: %v", err)
	}
	if len(view.Zones) != 0 || len(view.Records) != 0 {
		t.Fatalf("expected empty scoped view, got %+v", view)
	}
}

// A ?zone= may be given with or without the trailing dot; PowerDNS's
// canonical form has one and an operator typing a domain will not.
func TestDNS_ScopeAcceptsEitherTrailingDotForm(t *testing.T) {
	reader := &fakeDNSReader{
		zones:   []sdndns.Zone{{Domain: "example.com.", Plugin: "powerdns"}},
		records: map[string][]sdndns.Record{"example.com.": {{Name: "web1", Type: "A", Value: "10.0.0.5"}}},
	}
	for _, scope := range []string{"example.com", "example.com."} {
		view, err := NewDNSService(reader).DNS(context.Background(), scope)
		if err != nil {
			t.Fatalf("DNS(%q): %v", scope, err)
		}
		if len(view.Zones) != 1 {
			t.Errorf("DNS(%q) matched no zone", scope)
		}
	}
}

// An unreadable PowerDNS server must not blank the view of the others, and
// must be reported as unreadable rather than as "this zone has no records" —
// the distinction T-4109's PTR audit is built on.
func TestDNS_UnreadableZoneIsNamed(t *testing.T) {
	reader := &fakeDNSReader{
		zones: []sdndns.Zone{
			{Domain: "broken.com.", Plugin: "powerdns"},
			{Domain: "working.com.", Plugin: "powerdns"},
		},
		records: map[string][]sdndns.Record{
			"working.com.": {{Zone: "working.com.", Name: "web1", Type: "A", Value: "10.10.0.5"}},
		},
		readErr: map[string]error{"broken.com.": errors.New("powerdns unreachable")},
	}
	view, err := NewDNSService(reader).DNS(context.Background(), "")
	if err != nil {
		t.Fatalf("DNS: %v", err)
	}
	if len(view.Records) != 1 {
		t.Fatalf("one broken zone cost the healthy zone its records: %+v", view.Records)
	}
	if len(view.Unreadable) != 1 || view.Unreadable[0].ID != "broken.com." {
		t.Fatalf("unreadable = %+v, want the one broken zone named", view.Unreadable)
	}
	if view.Unreadable[0].Reason == "" {
		t.Error("an unreadable zone with no reason cannot be acted on")
	}
}

// A domain vnprox declined to derive is reported the same way, because from
// the operator's side "my zone is missing" has one question behind it and the
// answer is the reason.
func TestDNS_SkippedDerivationIsReported(t *testing.T) {
	reader := &fakeDNSReader{
		skipped: []sdndns.Skip{{
			SDNZone: "zone1",
			Reason:  `dns names plugin "pdns", which is not configured under /cluster/sdn/dns`,
		}},
	}
	view, err := NewDNSService(reader).DNS(context.Background(), "")
	if err != nil {
		t.Fatalf("DNS: %v", err)
	}
	if len(view.Unreadable) != 1 {
		t.Fatalf("unreadable = %+v, want the skipped derivation reported", view.Unreadable)
	}
	if view.Unreadable[0].SDNZone != "zone1" {
		t.Errorf("sdnZone = %q, want the zone whose configuration is at fault", view.Unreadable[0].SDNZone)
	}
}
