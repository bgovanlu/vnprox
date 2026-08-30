// SPDX-License-Identifier: Apache-2.0

package sdndns

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func pluginPDNS() pve.SDNDnsPlugin {
	return pve.SDNDnsPlugin{ID: "pdns", Type: "powerdns", URL: "https://pdns:8081/api/v1/servers/localhost", Key: "k", TTL: 3600}
}

// The forward domain comes from the SDN zone's dnszone field, and the plugin
// from its dns field. Neither lives under /cluster/sdn/dns, which is what
// vnprox used to read as if it were the domain list.
func TestDeriveZones_ForwardDomainComesFromTheSDNZone(t *testing.T) {
	zones, skipped := DeriveZones(
		[]pve.SDNZone{{ID: "zone1", DnsZone: "example.com", DNS: "pdns"}},
		nil, nil,
		[]pve.SDNDnsPlugin{pluginPDNS()},
	)

	if len(skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", skipped)
	}
	want := []Zone{{Domain: "example.com.", Plugin: "pdns", SDNZone: "zone1", TTL: 3600}}
	if !reflect.DeepEqual(zones, want) {
		t.Errorf("zones = %+v, want %+v", zones, want)
	}
}

// Reverse zones are derived per subnet from its CIDR — PVE stores no reverse
// domain anywhere. Several subnets under one private /8 collapse to one
// reverse zone, which is why the result is deduplicated: reading
// 10.in-addr.arpa three times a poll would return the same records three
// times.
func TestDeriveZones_ReverseDomainsAreDerivedAndDeduplicated(t *testing.T) {
	zones, skipped := DeriveZones(
		[]pve.SDNZone{{ID: "zone1", DnsZone: "example.com", DNS: "pdns", ReverseDNS: "pdns"}},
		[]pve.SDNVnet{{ID: "vnet1", Zone: "zone1"}},
		[]pve.SDNSubnet{
			{ID: "s1", Vnet: "vnet1", CIDR: "10.0.0.0/24"},
			{ID: "s2", Vnet: "vnet1", CIDR: "10.0.1.0/24"},
			{ID: "s3", Vnet: "vnet1", CIDR: "192.168.5.0/24"},
		},
		[]pve.SDNDnsPlugin{pluginPDNS()},
	)

	if len(skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", skipped)
	}
	var reverse []string
	for _, z := range zones {
		if z.Reverse {
			reverse = append(reverse, z.Domain)
		}
	}
	want := []string{"10.in-addr.arpa.", "168.192.in-addr.arpa."}
	if !reflect.DeepEqual(reverse, want) {
		t.Errorf("reverse zones = %v, want %v (the two /24s under 10/8 collapse to one)", reverse, want)
	}
}

// Subnets.pm passes $zone->{reversedns} to get_reversedns_zone with no
// fallback to $zone->{dns}. A zone with a forward domain and no reversedns
// therefore has no PTRs at all — which is a configuration choice, not a gap
// vnprox should invent a zone for and then report as uncovered.
func TestDeriveZones_NoReverseDNSMeansNoReverseZones(t *testing.T) {
	zones, skipped := DeriveZones(
		[]pve.SDNZone{{ID: "zone1", DnsZone: "example.com", DNS: "pdns"}},
		[]pve.SDNVnet{{ID: "vnet1", Zone: "zone1"}},
		[]pve.SDNSubnet{{ID: "s1", Vnet: "vnet1", CIDR: "10.0.0.0/24"}},
		[]pve.SDNDnsPlugin{pluginPDNS()},
	)

	for _, z := range zones {
		if z.Reverse {
			t.Errorf("derived reverse zone %s for a zone with no reversedns plugin", z.Domain)
		}
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %+v — a zone that simply has no reverse DNS is not a problem to report", skipped)
	}
}

// A zone naming a plugin that is not configured is reported, not dropped:
// "my DNS zone is missing" has one question behind it and the reason is the
// answer.
func TestDeriveZones_UnconfiguredPluginIsReported(t *testing.T) {
	zones, skipped := DeriveZones(
		[]pve.SDNZone{{ID: "zone1", DnsZone: "example.com", DNS: "missing"}},
		nil, nil,
		[]pve.SDNDnsPlugin{pluginPDNS()},
	)

	if len(zones) != 0 {
		t.Fatalf("zones = %+v, want none", zones)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %+v, want the misconfiguration reported", skipped)
	}
	if !strings.Contains(skipped[0].Reason, "missing") || skipped[0].SDNZone != "zone1" {
		t.Errorf("skip = %+v, want it to name both the zone and the plugin", skipped[0])
	}
}

func TestDeriveZones_UnparseableSubnetIsReported(t *testing.T) {
	_, skipped := DeriveZones(
		[]pve.SDNZone{{ID: "zone1", DNS: "pdns", ReverseDNS: "pdns"}},
		[]pve.SDNVnet{{ID: "vnet1", Zone: "zone1"}},
		[]pve.SDNSubnet{{ID: "s1", Vnet: "vnet1", CIDR: "not-a-cidr"}},
		[]pve.SDNDnsPlugin{pluginPDNS()},
	)

	if len(skipped) != 1 || skipped[0].Subnet != "not-a-cidr" {
		t.Fatalf("skipped = %+v, want the bad subnet named", skipped)
	}
}

// A subnet whose vnet belongs to a different SDN zone must not contribute a
// reverse zone to this one — the vnet is what links them, and getting the
// join wrong would read another zone's records under this zone's plugin.
func TestDeriveZones_SubnetsFollowTheirOwnZone(t *testing.T) {
	zones, _ := DeriveZones(
		[]pve.SDNZone{
			{ID: "zone1", DNS: "pdns", ReverseDNS: "pdns"},
			{ID: "zone2"},
		},
		[]pve.SDNVnet{{ID: "vnet1", Zone: "zone1"}, {ID: "vnet2", Zone: "zone2"}},
		[]pve.SDNSubnet{
			{ID: "s1", Vnet: "vnet1", CIDR: "10.0.0.0/24"},
			{ID: "s2", Vnet: "vnet2", CIDR: "203.0.113.0/24"},
		},
		[]pve.SDNDnsPlugin{pluginPDNS()},
	)

	for _, z := range zones {
		if strings.HasPrefix(z.Domain, "113.0.203") {
			t.Errorf("zone2's subnet produced a reverse zone under zone1's plugin: %+v", z)
		}
	}
}

func TestRelativeName(t *testing.T) {
	for _, tt := range []struct{ fqdn, zone, want string }{
		{"web.example.com.", "example.com.", "web"},
		{"web.example.com", "example.com", "web"},
		{"example.com.", "example.com.", "@"},
		{"a.b.example.com.", "example.com.", "a.b"},
		// Not inside the zone: returned unchanged rather than mangled, so a
		// real misconfiguration stays visible.
		{"other.net.", "example.com.", "other.net"},
	} {
		if got := RelativeName(tt.fqdn, tt.zone); got != tt.want {
			t.Errorf("RelativeName(%q, %q) = %q, want %q", tt.fqdn, tt.zone, got, tt.want)
		}
	}
}

// SOA and NS at the apex are PowerDNS's own bookkeeping, not records an
// operator manages through vnprox. Including them would make every zone look
// like it has two records nobody created.
func TestFromZone_DropsSOAAndNS(t *testing.T) {
	recs := FromZone("example.com", powerdns.Zone{RRSets: []powerdns.RRSet{
		{Name: "example.com.", Type: "SOA", Records: []powerdns.Record{{Content: "ns1. hostmaster. 1 2 3 4 5"}}},
		{Name: "example.com.", Type: "NS", Records: []powerdns.Record{{Content: "ns1.example.com."}}},
		{Name: "web.example.com.", Type: "A", TTL: 300, Records: []powerdns.Record{{Content: "10.0.0.5"}}},
	}})

	if len(recs) != 1 || recs[0].Type != "A" {
		t.Fatalf("records = %+v, want only the A record", recs)
	}
	if recs[0].Name != "web" || recs[0].Zone != "example.com." {
		t.Errorf("record = %+v, want name relative to the canonical zone", recs[0])
	}
}

// Value is the first of Values, not the only one. Reporting a round-robin A
// record as a single address is the quiet lie this field pair exists to
// prevent.
func TestFromZone_MultiValuedRRSetKeepsEveryValue(t *testing.T) {
	recs := FromZone("example.com.", powerdns.Zone{RRSets: []powerdns.RRSet{{
		Name: "web.example.com.", Type: "A", TTL: 300,
		Records: []powerdns.Record{{Content: "10.0.0.6"}, {Content: "10.0.0.5"}},
	}}})

	if len(recs) != 1 {
		t.Fatalf("records = %+v, want one entity for the rrset", recs)
	}
	want := []string{"10.0.0.5", "10.0.0.6"}
	if !reflect.DeepEqual(recs[0].Values, want) {
		t.Errorf("Values = %v, want %v (sorted)", recs[0].Values, want)
	}
	if recs[0].Value != want[0] {
		t.Errorf("Value = %q, want the first of Values", recs[0].Value)
	}
}

// A disabled record is not an absent one: a resolver will not serve it, but
// reporting the rrset as missing would collapse two different states.
func TestFromZone_FullyDisabledRRSetIsFlaggedNotDropped(t *testing.T) {
	recs := FromZone("example.com.", powerdns.Zone{RRSets: []powerdns.RRSet{{
		Name: "old.example.com.", Type: "A",
		Records: []powerdns.Record{{Content: "10.0.0.9", Disabled: true}},
	}}})

	if len(recs) != 1 {
		t.Fatalf("a disabled record was dropped entirely: %+v", recs)
	}
	if !recs[0].Disabled {
		t.Error("a wholly-disabled rrset was not flagged as disabled")
	}
}
