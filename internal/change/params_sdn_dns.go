package change

// T-1204 SDN DNS op params. A DNS zone (sdn.dns.zone.*) is a forward domain
// registered in /etc/pve/sdn/dns.cfg and backed by a PowerDNS plugin
// instance; a record (sdn.dns.record.*) is one A/AAAA/PTR/CNAME/TXT entry in
// that zone, realized against PowerDNS. Targets carry identity — a zone's is
// Ref{KindSDNDnsZone, ID: domain}, a record's is Ref{KindSDNDnsRecord, ID:
// "<zone>/<name>/<type>"} — so, like SdnVnetCreateParams' Zone field, the
// record params keep Zone/Name/Type as explicit fields alongside the target
// so referential validators need not parse the composite id back apart.

// SdnDnsZoneCreateParams is op "sdn.dns.zone.create".
type SdnDnsZoneCreateParams struct {
	DNS string `json:"dns,omitempty"` // backing PowerDNS plugin instance id
	TTL int    `json:"ttl,omitempty"` // zone default record TTL
}

func (SdnDnsZoneCreateParams) isChangeParams() {}

// SdnDnsZoneUpdateParams is op "sdn.dns.zone.update": a partial update. The
// domain (the target's own ID) is identity and not editable in place.
type SdnDnsZoneUpdateParams struct {
	DNS *string `json:"dns,omitempty"`
	TTL *int    `json:"ttl,omitempty"`
}

func (SdnDnsZoneUpdateParams) isChangeParams() {}

// SdnDnsZoneDeleteParams is op "sdn.dns.zone.delete".
type SdnDnsZoneDeleteParams struct{}

func (SdnDnsZoneDeleteParams) isChangeParams() {}

// SdnDnsRecordCreateParams is op "sdn.dns.record.create". Zone/Name/Type are
// the record's identity (mirrored into the target's composite ID); Value is
// the record data (an IP for A/AAAA/PTR, a target for CNAME, text for TXT).
type SdnDnsRecordCreateParams struct {
	Zone  string `json:"zone"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl,omitempty"`
}

func (SdnDnsRecordCreateParams) isChangeParams() {}

// SdnDnsRecordUpdateParams is op "sdn.dns.record.update": a partial update.
// Zone/Name/Type are identity (changing any of them is a delete+create), so
// only the record's data/TTL are editable in place.
type SdnDnsRecordUpdateParams struct {
	Value *string `json:"value,omitempty"`
	TTL   *int    `json:"ttl,omitempty"`
}

func (SdnDnsRecordUpdateParams) isChangeParams() {}

// SdnDnsRecordDeleteParams is op "sdn.dns.record.delete".
type SdnDnsRecordDeleteParams struct{}

func (SdnDnsRecordDeleteParams) isChangeParams() {}
