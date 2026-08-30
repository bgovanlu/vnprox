// SPDX-License-Identifier: Apache-2.0

package change

// T-1204 SDN DNS op params, corrected by T-4112.
//
// # What sdn.dns.zone.* actually manages
//
// It manages a /cluster/sdn/dns entry, and that is a PowerDNS SERVER
// CONNECTION — url, key, ttl, fingerprint — not a DNS zone. The op names date
// from a model in which /cluster/sdn/dns listed domains; it does not, and
// never did (internal/pve/sdn_dns.go's package comment). The DNS domains live
// on the SDN zone's own `dnszone`/`dns`/`reversedns` fields.
//
// The op type strings are left as they are: they are a wire contract other
// tasks and the Terraform/Ansible integrations depend on, and CLAUDE.md is
// explicit that a contract is not renamed unilaterally. What IS fixed here is
// that the params could not produce a valid request: PVE requires dns, type,
// url and key on a create, and these params carried only dns and ttl, so
// every applied sdn.dns.zone.create was a parameter-verification 400 on real
// PVE. Renaming the ops to sdn.dns.server.* is filed separately.
//
// A record (sdn.dns.record.*) is one A/AAAA/PTR/CNAME/TXT entry, realized
// against the PowerDNS server directly — PVE has no record API at all.
// Targets carry identity — a zone's is Ref{KindSDNDnsZone, ID: domain}, a
// record's is Ref{KindSDNDnsRecord, ID: "<zone>/<name>/<type>"} — so, like
// SdnVnetCreateParams' Zone field, the record params keep Zone/Name/Type as
// explicit fields alongside the target so referential validators need not
// parse the composite id back apart.

// SdnDnsZoneCreateParams is op "sdn.dns.zone.create": it creates a PowerDNS
// server connection under /cluster/sdn/dns (see this file's comment for why
// the op is not named that).
//
// Type/URL/Key are what PVE requires and what this op omitted before T-4112.
// Key is an API secret: it is carried here because the op cannot be applied
// without it, and internal/change's redaction is what keeps it out of
// previews, diffs and audit records.
type SdnDnsZoneCreateParams struct {
	DNS  string `json:"dns,omitempty"`  // the connection's own id, when it differs from the target
	Type string `json:"type,omitempty"` // PVE plugin type; only "powerdns" exists on 9.2.4
	URL  string `json:"url,omitempty"`  // PowerDNS API base, including /api/v1/servers/<server>
	Key  string `json:"key,omitempty"`  // PowerDNS X-API-Key
	// Fingerprint pins the server's leaf certificate (PVE's colon-separated
	// SHA-256). When set, vnprox verifies exactly as PVE does — see
	// internal/powerdns's pinnedTLS.
	Fingerprint   string `json:"fingerprint,omitempty"`
	TTL           int    `json:"ttl,omitempty"`           // default record TTL
	ReverseMaskV6 int    `json:"reversemaskv6,omitempty"` // forced IPv6 reverse-zone prefix length
}

func (SdnDnsZoneCreateParams) isChangeParams() {}

// SdnDnsZoneUpdateParams is op "sdn.dns.zone.update": a partial update. The
// connection id (the target's own ID) is identity and not editable in place.
type SdnDnsZoneUpdateParams struct {
	DNS           *string `json:"dns,omitempty"`
	Type          *string `json:"type,omitempty"`
	URL           *string `json:"url,omitempty"`
	Key           *string `json:"key,omitempty"`
	Fingerprint   *string `json:"fingerprint,omitempty"`
	TTL           *int    `json:"ttl,omitempty"`
	ReverseMaskV6 *int    `json:"reversemaskv6,omitempty"`
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
