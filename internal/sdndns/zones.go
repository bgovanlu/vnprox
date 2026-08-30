// SPDX-License-Identifier: Apache-2.0

// Package sdndns joins PVE's SDN DNS configuration to the PowerDNS server
// that actually holds the records (T-4112).
//
// The split exists because PVE and PowerDNS each hold half the answer, and
// neither half is useful alone:
//
//   - PVE knows WHICH domains matter and WHICH server serves them. An SDN
//     zone carries `dnszone` (the forward domain), `dns` (the PowerDNS
//     connection to write it through) and `reversedns` (a second connection
//     for PTRs); `/cluster/sdn/dns` carries each connection's url, key and
//     fingerprint. Reverse domains are not stored at all — they are derived
//     per subnet from its CIDR, by an algorithm this package reproduces from
//     the plugin (internal/powerdns/reverse.go).
//   - PowerDNS knows what records exist. PVE keeps no copy: PowerdnsPlugin.pm
//     writes each record straight through, so there is exactly one place to
//     read them from.
//
// vnprox used to ask PVE for both halves, through routes that do not exist.
// See internal/pve/sdn_dns.go's package comment for that history.
//
// # Secrets
//
// A Reader holds PowerDNS API keys in memory, because it cannot make a
// request without them. They come from PVE on every refresh and are never
// persisted, logged, or returned through any API — Zone and Record below
// deliberately have no field that could carry one, so a key cannot reach a
// changeset snapshot or a JSON response by accident.
package sdndns

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// Zone is one DNS domain vnprox knows about: a forward domain an SDN zone
// registers guests in, or a reverse domain derived from one of its subnets.
//
// Domain is the canonical trailing-dot form, so the forward and reverse sides
// compare the same way and a domain configured with and without the dot
// cannot become two entries. Plugin is the /cluster/sdn/dns instance id that
// serves it — never the connection itself, so a Zone can be logged and
// serialized freely.
type Zone struct {
	Domain string
	Plugin string
	// SDNZone is the SDN zone this domain came from — the forward zone's
	// owner, or the owner of the subnet a reverse zone was derived from. Kept
	// so a finding can say which zone's configuration is at fault rather than
	// naming a bare domain.
	SDNZone string
	TTL     int
	Reverse bool
}

// Record is one rrset, flattened into the shape vnprox's inventory and API
// already use.
//
// Value and Values both exist because the two models disagree: PowerDNS's
// unit is an rrset that may hold several records, while vnprox identifies a
// record by "<zone>/<name>/<type>" and has room for one value. Values is the
// full set in sorted order; Value is the first, so existing callers keep
// working and nothing is silently dropped from the ones that need all of it —
// a round-robin A record reported as a single address is exactly the kind of
// quiet lie this card was opened over.
type Record struct {
	Zone   string
	Name   string
	Type   string
	Value  string
	Values []string
	TTL    int
	// Disabled is true when every record in the rrset is disabled in
	// PowerDNS. A disabled PTR resolves like a missing one but is emphatically
	// not missing, and the PTR audit has to tell those apart.
	Disabled bool
}

// DeriveZones works out every DNS domain vnprox can read, from configuration
// PVE already gives the collector: the SDN zones, their vnets, their subnets,
// and the DNS plugin instances.
//
// The result is sorted and deduplicated. Duplicates are ordinary — several
// subnets in one SDN zone routinely share a reverse zone (every RFC1918 /24
// under 10/8 maps to "10.in-addr.arpa.") — and reading the same domain twice
// per poll would double the request count for no new data.
//
// A domain vnprox cannot name is omitted rather than guessed: an SDN zone
// with no `dnszone`, a zone whose `dns` names no configured plugin, a subnet
// with an unparseable CIDR, or a public IPv4 prefix longer than /24 (whose
// reverse zone the plugin itself leaves empty). Each omission is returned in
// the Skipped list so a caller can report "vnprox has no DNS view of this
// zone" rather than showing an empty one.
func DeriveZones(
	sdnZones []pve.SDNZone,
	vnets []pve.SDNVnet,
	subnets []pve.SDNSubnet,
	plugins []pve.SDNDnsPlugin,
) (zones []Zone, skipped []Skip) {
	byPlugin := make(map[string]pve.SDNDnsPlugin, len(plugins))
	for _, p := range plugins {
		byPlugin[p.ID] = p
	}
	vnetZone := make(map[string]string, len(vnets))
	for _, v := range vnets {
		vnetZone[v.ID] = v.Zone
	}
	subnetsByZone := make(map[string][]pve.SDNSubnet, len(sdnZones))
	for _, s := range subnets {
		if z, ok := vnetZone[s.Vnet]; ok {
			subnetsByZone[z] = append(subnetsByZone[z], s)
		}
	}

	seen := make(map[string]bool)
	add := func(z Zone) {
		key := z.Plugin + "\x00" + z.Domain
		if seen[key] {
			return
		}
		seen[key] = true
		zones = append(zones, z)
	}

	for _, sz := range sdnZones {
		if sz.DnsZone != "" {
			plugin, ok := byPlugin[sz.DNS]
			if !ok {
				skipped = append(skipped, Skip{
					SDNZone: sz.ID, Domain: sz.DnsZone,
					Reason: reasonNoPlugin(sz.DNS, "dns"),
				})
			} else {
				add(Zone{
					Domain:  canonical(sz.DnsZone),
					Plugin:  plugin.ID,
					SDNZone: sz.ID,
					TTL:     plugin.TTL,
				})
			}
		}

		// Reverse zones need their own plugin instance. Subnets.pm passes
		// $zone->{reversedns} to get_reversedns_zone with no fallback to
		// $zone->{dns}, so a zone with a forward domain but no reversedns
		// genuinely has no PTRs — not a PTR gap vnprox should report.
		if sz.ReverseDNS == "" {
			continue
		}
		plugin, ok := byPlugin[sz.ReverseDNS]
		if !ok {
			skipped = append(skipped, Skip{
				SDNZone: sz.ID,
				Reason:  reasonNoPlugin(sz.ReverseDNS, "reversedns"),
			})
			continue
		}
		for _, s := range subnetsByZone[sz.ID] {
			cidr, err := netip.ParsePrefix(strings.TrimSpace(s.CIDR))
			if err != nil {
				skipped = append(skipped, Skip{
					SDNZone: sz.ID, Subnet: s.CIDR,
					Reason: fmt.Sprintf("subnet cidr %q does not parse: %v", s.CIDR, err),
				})
				continue
			}
			domain, err := powerdns.ReverseZone(cidr, plugin.ReverseMaskV6)
			if err != nil {
				skipped = append(skipped, Skip{
					SDNZone: sz.ID, Subnet: s.CIDR,
					Reason: fmt.Sprintf("no reverse zone for %s: %v", s.CIDR, err),
				})
				continue
			}
			if domain == "" {
				skipped = append(skipped, Skip{
					SDNZone: sz.ID, Subnet: s.CIDR,
					Reason: fmt.Sprintf("%s is a public prefix longer than /24 — PVE names no reverse zone for it", s.CIDR),
				})
				continue
			}
			add(Zone{
				Domain:  canonical(domain),
				Plugin:  plugin.ID,
				SDNZone: sz.ID,
				TTL:     plugin.TTL,
				Reverse: true,
			})
		}
	}

	sort.Slice(zones, func(i, j int) bool {
		if zones[i].Domain != zones[j].Domain {
			return zones[i].Domain < zones[j].Domain
		}
		return zones[i].Plugin < zones[j].Plugin
	})
	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].SDNZone != skipped[j].SDNZone {
			return skipped[i].SDNZone < skipped[j].SDNZone
		}
		return skipped[i].Reason < skipped[j].Reason
	})
	return zones, skipped
}

// Skip is one domain vnprox deliberately did not derive, with the reason.
// Reporting these is the difference between "this zone has no DNS" and "this
// zone's DNS is misconfigured in a way vnprox can name".
type Skip struct {
	SDNZone string
	Subnet  string
	Domain  string
	Reason  string
}

func reasonNoPlugin(id, field string) string {
	if id == "" {
		return "sdn zone sets no " + field + " plugin"
	}
	return fmt.Sprintf("%s names plugin %q, which is not configured under /cluster/sdn/dns", field, id)
}

// canonical is the trailing-dot form PowerDNS uses for a zone id.
func canonical(domain string) string {
	d := strings.TrimSpace(domain)
	if d == "" || strings.HasSuffix(d, ".") {
		return d
	}
	return d + "."
}

// RelativeName reduces a record's fully-qualified rrset name to the label
// vnprox stores, which is the name relative to its zone — "web" in
// "web.example.com." under "example.com.". The apex becomes "@", matching
// zone-file convention and internal/sdn's own fqdn() helper, which maps it
// back.
//
// A name that is not inside the zone is returned unchanged rather than
// mangled: PowerDNS will not normally serve one, and inventing a relative
// form for it would hide a real misconfiguration.
func RelativeName(fqdn, zone string) string {
	f := strings.TrimSuffix(canonical(fqdn), ".")
	z := strings.TrimSuffix(canonical(zone), ".")
	switch {
	case f == z:
		return "@"
	case z != "" && strings.HasSuffix(f, "."+z):
		return strings.TrimSuffix(f, "."+z)
	default:
		return f
	}
}

// FromZone flattens a PowerDNS zone read into vnprox's records, in a stable
// order. rrsets PowerDNS serves for its own bookkeeping (SOA and NS at the
// apex) are dropped: they are not records an operator manages through vnprox,
// and including them would make every zone look like it has two records it
// did not ask for.
func FromZone(domain string, z powerdns.Zone) []Record {
	out := make([]Record, 0, len(z.RRSets))
	for _, rr := range z.RRSets {
		if rr.Type == "SOA" || rr.Type == "NS" {
			continue
		}
		contents := rr.Contents()
		if len(contents) == 0 {
			continue
		}
		allDisabled := true
		for _, rec := range rr.Records {
			if !rec.Disabled {
				allDisabled = false
				break
			}
		}
		out = append(out, Record{
			Zone:     canonical(domain),
			Name:     RelativeName(rr.Name, domain),
			Type:     rr.Type,
			Value:    contents[0],
			Values:   contents,
			TTL:      rr.TTL,
			Disabled: allDisabled,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	return out
}
