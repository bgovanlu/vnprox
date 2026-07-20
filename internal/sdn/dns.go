package sdn

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// DNSReader is the subset of *pve.Client the DNS read view needs (T-1204):
// the configured DNS zones, each zone's config-authoritative record set, and
// a live PowerDNS "resolve" read. A small seam so this package's dependency
// on the concrete client stays reviewable and test-doubleable, mirroring
// PVEReader above.
type DNSReader interface {
	ListSDNDnsZones(ctx context.Context) ([]pve.SDNDnsZone, error)
	ListSDNDnsRecords(ctx context.Context, zone string) ([]pve.SDNDnsRecord, error)
	ResolveSDNDnsRecords(ctx context.Context, zone string) ([]pve.SDNDnsRecord, error)
}

// DNSService builds docs/api.md's GET /sdn/dns response from a DNSReader.
type DNSService struct {
	pve DNSReader
	now func() time.Time
}

// NewDNSService builds a DNSService backed by reader (in production, the
// daemon's own read-only *pve.Client).
func NewDNSService(reader DNSReader) *DNSService {
	return &DNSService{pve: reader, now: time.Now}
}

// DNSRecord is one record in the GET /sdn/dns response. FQDN is the record's
// fully-qualified name ("<name>.<zone>"), computed here so the map's dnsName
// correlation and the UI both read one canonical form.
type DNSRecord struct {
	Zone  string `json:"zone"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	FQDN  string `json:"fqdn"`
	TTL   int    `json:"ttl,omitempty"`
}

// DNSView is docs/api.md's GET /sdn/dns response: the config-authoritative
// records (from /etc/pve/sdn/dns.cfg + PVE's own record set) and the live
// PowerDNS resolve read, mirroring GET /sdn/dhcp's Reservation(config)/
// Lease(observed) duality. Records is authoritative; Resolved is best-effort
// (a zone whose PowerDNS server is unreachable simply contributes nothing to
// Resolved, never an error). Zones (optionally scoped by the ?zone= param)
// carries each configured zone's own metadata.
type DNSView struct {
	Zones       []DNSZone    `json:"zones"`
	Records     []DNSRecord  `json:"records"`
	Resolved    []DNSRecord  `json:"resolved"`
	GeneratedAt int64        `json:"generatedAt"`
}

// DNSZone is one configured DNS zone's metadata in the GET /sdn/dns response.
type DNSZone struct {
	ID  string `json:"id"`
	DNS string `json:"dns,omitempty"`
	TTL int    `json:"ttl,omitempty"`
}

// DNS builds docs/api.md's GET /sdn/dns response for zone (every configured
// DNS zone cluster-wide when zone == ""). An unconfigured cluster (no DNS
// zones) yields empty arrays, never an error (T-1204 acceptance criterion 1);
// a specific ?zone= naming no configured zone likewise yields empty arrays.
func (s *DNSService) DNS(ctx context.Context, zone string) (DNSView, error) {
	allZones, err := s.pve.ListSDNDnsZones(ctx)
	if err != nil {
		return DNSView{}, fmt.Errorf("sdn: listing dns zones: %w", err)
	}

	view := DNSView{
		Zones:       []DNSZone{},
		Records:     []DNSRecord{},
		Resolved:    []DNSRecord{},
		GeneratedAt: s.now().Unix(),
	}

	for _, z := range allZones {
		if zone != "" && z.ID != zone {
			continue
		}
		view.Zones = append(view.Zones, DNSZone{ID: z.ID, DNS: z.DNS, TTL: z.TTL})

		recs, err := s.pve.ListSDNDnsRecords(ctx, z.ID)
		if err != nil {
			return DNSView{}, fmt.Errorf("sdn: listing dns records for zone %s: %w", z.ID, err)
		}
		for _, r := range recs {
			view.Records = append(view.Records, toDNSRecord(z.ID, r))
		}

		// Resolved is a live read — soft-fail per zone (an unreachable
		// PowerDNS server contributes nothing rather than failing the request).
		if live, err := s.pve.ResolveSDNDnsRecords(ctx, z.ID); err == nil {
			for _, r := range live {
				view.Resolved = append(view.Resolved, toDNSRecord(z.ID, r))
			}
		}
	}

	sortRecords(view.Records)
	sortRecords(view.Resolved)
	sort.Slice(view.Zones, func(i, j int) bool { return view.Zones[i].ID < view.Zones[j].ID })
	return view, nil
}

func toDNSRecord(zone string, r pve.SDNDnsRecord) DNSRecord {
	return DNSRecord{
		Zone: zone, Name: r.Name, Type: r.Type, Value: r.Value, TTL: r.TTL,
		FQDN: fqdn(r.Name, zone),
	}
}

// fqdn joins a record's name label and its zone domain into a canonical
// fully-qualified name. A bare "@" (zone apex) or a name already ending in
// the zone is returned as the zone itself / unchanged.
func fqdn(name, zone string) string {
	switch {
	case name == "" || name == "@":
		return zone
	case zone == "":
		return name
	default:
		return name + "." + zone
	}
}

func sortRecords(rs []DNSRecord) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Zone != rs[j].Zone {
			return rs[i].Zone < rs[j].Zone
		}
		if rs[i].Name != rs[j].Name {
			return rs[i].Name < rs[j].Name
		}
		return rs[i].Type < rs[j].Type
	})
}
