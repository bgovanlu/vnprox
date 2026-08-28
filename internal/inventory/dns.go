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
type SdnDnsRecord struct {
	Ref
	rawSrc
	Zone  string
	Name  string
	Type  string
	Value string
	TTL   int
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
