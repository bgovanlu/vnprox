// SPDX-License-Identifier: Apache-2.0

package inventory

import "strconv"

// SdnDnsZone is one PVE SDN DNS zone (T-1204): a forward DNS domain
// registered in /etc/pve/sdn/dns.cfg and backed by a PowerDNS plugin
// instance. Cluster-scoped (empty Node, like every other sdn-* entity). ID
// is the domain ("example.com"); DNS names the backing PowerDNS plugin
// instance; TTL is the zone's default record TTL. Pending mirrors PVE's own
// staged-edit marker (""|"new"|"changed"|"deleted"), like SdnZone.Pending.
type SdnDnsZone struct {
	Ref
	rawSrc
	ID      string
	DNS     string
	Pending string
	TTL     int
}

func (z *SdnDnsZone) GetRef() Ref { return z.Ref }
func (z *SdnDnsZone) clone() Entity {
	cp := *z
	return &cp
}
func (z *SdnDnsZone) fieldMap() map[string]string {
	return map[string]string{
		"id": z.ID, "dns": z.DNS, "ttl": strconv.Itoa(z.TTL), "pending": z.Pending,
	}
}

// SdnDnsRecord is one A/AAAA/PTR/CNAME/TXT record within an SdnDnsZone
// (T-1204). Cluster-scoped. Ref.ID is the "<zone>/<name>/<type>" composite
// (params_sdn_dns.go's op-target convention); Zone is the owning domain,
// Name the hostname label, Type the record type, Value the record data
// (e.g. the A record's IP). TTL is the record-level override (0 = inherit
// the zone default).
//
// Values (T-4112) is every value under this name and type, sorted. PowerDNS's
// unit is an rrset that may hold several records while this entity's id has
// room for one, so Value is the first of Values and Values is the whole set:
// a round-robin A record reported as a single address would be a quiet lie,
// and the PTR audit needs all of them to decide whether a forward address is
// covered. A single-valued record has one entry here and Value equal to it.
type SdnDnsRecord struct {
	Ref
	rawSrc
	Zone   string
	Name   string
	Type   string
	Value  string
	Values []string
	TTL    int
}

func (r *SdnDnsRecord) GetRef() Ref { return r.Ref }
func (r *SdnDnsRecord) clone() Entity {
	cp := *r
	return &cp
}
func (r *SdnDnsRecord) fieldMap() map[string]string {
	return map[string]string{
		"zone": r.Zone, "name": r.Name, "type": r.Type,
		"value": r.Value, "ttl": strconv.Itoa(r.TTL),
	}
}

// SdnDnsServer is one /cluster/sdn/dns entry: a PowerDNS server connection
// (T-4112). It is NOT a DNS zone — SdnDnsZone above is the zone, and the two
// were the same type until T-4112 found that `/cluster/sdn/dns` lists
// connections rather than domains.
//
// No collector produces this entity: it is projection-only, carrying
// KindSDNDnsServer (T-4114). It exists so `internal/change`'s preview can
// show what a `sdn.dns.server.*` op will do without fabricating a DNS domain
// named after a server — which is what it did before, and which let a record
// op naming that fake domain validate clean and fail at apply.
//
// T-4112 gave this type its own identity while leaving it under
// KindSDNDnsZone, on the reasoning that the two ids are disjoint (a domain's
// id is a DNS name, a connection's is PVE's dotless SDN object pattern) so
// nothing could confuse them. That was true of the projection and false of
// everything else: sharing a Kind is exactly what let validate_safety.go's
// deletion guard key on connection ids and compare them against domain
// names, so it never fired. T-4114 splits the Kind.
//
// Key is deliberately absent. The API key is in the op's params, where
// internal/api's redactOpSecrets strips it from every changeset read; there
// is no reason for a projected entity — which feeds diffs and previews — to
// carry one at all.
type SdnDnsServer struct {
	Ref
	rawSrc
	ID            string
	Type          string
	URL           string
	Fingerprint   string
	TTL           int
	ReverseMaskV6 int
}

func (s *SdnDnsServer) GetRef() Ref { return s.Ref }
func (s *SdnDnsServer) clone() Entity {
	cp := *s
	return &cp
}
func (s *SdnDnsServer) fieldMap() map[string]string {
	return map[string]string{
		"id": s.ID, "type": s.Type, "url": s.URL, "fingerprint": s.Fingerprint,
		"ttl": strconv.Itoa(s.TTL), "reversemaskv6": strconv.Itoa(s.ReverseMaskV6),
	}
}
