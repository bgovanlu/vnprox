// SPDX-License-Identifier: Apache-2.0

package sdn

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/powerdns"

	"github.com/bgovanlu/vnprox/internal/sdndns"
)

// DNSReader is what the DNS read view needs (T-1204, re-sourced by T-4112):
// the DNS domains vnprox derived from SDN configuration, and each domain's
// records read back from the PowerDNS server that serves it.
//
// The two methods used to be three, and all three called PVE. The record
// reads went to /cluster/sdn/dns/{zone}/records and .../resolve, which do not
// exist on any PVE (internal/pve/sdn_dns.go's package comment). The
// "resolved" half went with them: it modelled a config-vs-live duality that
// has no counterpart in reality, because PVE stores no records of its own —
// there is one record source and it is PowerDNS. See DNSView.Resolved.
//
// A small seam so this package's dependency on internal/sdndns stays
// reviewable and test-doubleable, mirroring PVEReader above.
type DNSReader interface {
	// Zones returns every DNS domain vnprox can read, forward and reverse,
	// along with the domains it deliberately could not derive.
	Zones(ctx context.Context) ([]sdndns.Zone, []sdndns.Skip, error)
	// Records reads one domain's records from its PowerDNS server.
	Records(ctx context.Context, zone sdndns.Zone) ([]sdndns.Record, error)
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
	// Values is every value under this name and type (T-4112). PowerDNS's
	// unit is an rrset that may hold several records; Value is the first so
	// existing clients keep working, and Values is the whole set so a
	// round-robin A record is not reported as a single address. It sits after
	// the strings rather than beside Value because fieldalignment packs the
	// string pointers ahead of the slice's.
	Values []string `json:"values,omitempty"`
	TTL    int      `json:"ttl,omitempty"`
}

// DNSView is GET /sdn/dns's response: every DNS domain vnprox derived from
// SDN configuration, and the records read back from the PowerDNS servers that
// serve them.
//
// Resolved is retained on the wire and is always empty (T-4112). It was
// modelled on GET /sdn/dhcp's Reservation(config)/Lease(observed) duality,
// which DNS does not have: PVE writes each record straight into PowerDNS and
// keeps no copy, so "the configured records" and "the live records" are one
// list read from one place. Filling both with the same data would assert a
// cross-check that never happened; dropping the field outright would break
// clients mid-flight. It stays empty, documented, and is removed when the
// route's consumers have moved off it.
//
// Unreadable names the domains this view could not produce records for, so a
// caller can tell "this zone has no records" from "vnprox could not read this
// zone" — the distinction T-4109's PTR audit is built on.
type DNSView struct {
	// Field order is fieldalignment's, not reading order: the four slices
	// pack ahead of the int64.
	Zones       []DNSZone     `json:"zones"`
	Records     []DNSRecord   `json:"records"`
	Resolved    []DNSRecord   `json:"resolved"`
	Unreadable  []DNSZoneFail `json:"unreadable"`
	GeneratedAt int64         `json:"generatedAt"`
}

// DNSZone is one DNS domain in the GET /sdn/dns response. ID is the domain,
// DNS the /cluster/sdn/dns plugin instance that serves it, Reverse true for
// an in-addr.arpa/ip6.arpa domain derived from a subnet's CIDR.
type DNSZone struct {
	ID      string `json:"id"`
	DNS     string `json:"dns,omitempty"`
	SDNZone string `json:"sdnZone,omitempty"`
	TTL     int    `json:"ttl,omitempty"`
	Reverse bool   `json:"reverse,omitempty"`
}

// DNSZoneFail is one domain vnprox could not read, with the reason. Reason is
// an operator-facing sentence, never a raw transport error: it says whether
// PowerDNS does not serve the zone, or vnprox could not reach the server.
type DNSZoneFail struct {
	ID      string `json:"id"`
	DNS     string `json:"dns,omitempty"`
	SDNZone string `json:"sdnZone,omitempty"`
	Reason  string `json:"reason"`
}

// DNS builds GET /sdn/dns's response for zone (every domain vnprox knows
// about when zone == ""). An unconfigured cluster (no SDN zone sets a
// dnszone, or no PowerDNS plugin is configured) yields empty arrays, never an
// error (T-1204 acceptance criterion 1); a specific ?zone= naming no known
// domain likewise yields empty arrays.
//
// A domain whose PowerDNS server refuses or cannot be reached lands in
// Unreadable rather than failing the request: one broken DNS server must not
// blank the view of the others. Only the PVE-side read — which domains exist
// at all — is a hard failure, because without it there is nothing to report.
func (s *DNSService) DNS(ctx context.Context, zone string) (DNSView, error) {
	allZones, skipped, err := s.pve.Zones(ctx)
	if err != nil {
		return DNSView{}, fmt.Errorf("sdn: listing dns zones: %w", err)
	}

	view := DNSView{
		Zones:       []DNSZone{},
		Records:     []DNSRecord{},
		Resolved:    []DNSRecord{},
		Unreadable:  []DNSZoneFail{},
		GeneratedAt: s.now().Unix(),
	}

	// A domain vnprox deliberately did not derive is unreadable for a reason
	// it can state — a zone naming an unconfigured plugin, a subnet whose
	// CIDR does not parse. Reporting it beats omitting it: the operator's
	// question is "why is this zone missing", and silence is the one answer
	// that cannot be acted on.
	for _, sk := range skipped {
		if zone != "" && sk.Domain != zone {
			continue
		}
		view.Unreadable = append(view.Unreadable, DNSZoneFail{
			ID: sk.Domain, SDNZone: sk.SDNZone, Reason: sk.Reason,
		})
	}

	for _, z := range allZones {
		if zone != "" && z.Domain != zone && strings.TrimSuffix(z.Domain, ".") != zone {
			continue
		}
		view.Zones = append(view.Zones, DNSZone{
			ID: z.Domain, DNS: z.Plugin, SDNZone: z.SDNZone, TTL: z.TTL, Reverse: z.Reverse,
		})

		recs, err := s.pve.Records(ctx, z)
		if err != nil {
			view.Unreadable = append(view.Unreadable, DNSZoneFail{
				ID: z.Domain, DNS: z.Plugin, SDNZone: z.SDNZone, Reason: unreadableReason(err),
			})
			continue
		}
		for _, r := range recs {
			view.Records = append(view.Records, toDNSRecord(z.Domain, r))
		}
	}

	sortRecords(view.Records)
	sort.Slice(view.Zones, func(i, j int) bool { return view.Zones[i].ID < view.Zones[j].ID })
	sort.Slice(view.Unreadable, func(i, j int) bool {
		if view.Unreadable[i].ID != view.Unreadable[j].ID {
			return view.Unreadable[i].ID < view.Unreadable[j].ID
		}
		return view.Unreadable[i].Reason < view.Unreadable[j].Reason
	})
	return view, nil
}

// unreadableReason turns a read failure into something an operator can act
// on. PowerDNS's own 404 means the server does not serve this zone, which is
// a configuration mistake with an obvious fix; anything else is a reachability
// or auth problem, which is not.
func unreadableReason(err error) string {
	if powerdns.IsNotFound(err) {
		return "the PowerDNS server does not serve this zone"
	}
	return "the PowerDNS server could not be read: " + err.Error()
}

func toDNSRecord(zone string, r sdndns.Record) DNSRecord {
	return DNSRecord{
		Zone: zone, Name: r.Name, Type: r.Type, Value: r.Value, Values: r.Values, TTL: r.TTL,
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
