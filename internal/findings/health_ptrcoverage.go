// SPDX-License-Identifier: Apache-2.0

// health_ptrcoverage.go implements T-4109's reverse-DNS coverage audit:
// for every forward (A/AAAA) record vnprox knows about via
// internal/inventory/dns.go (itself populated by internal/collect's poll of
// the same PowerDNS-backed SDN DNS plugin internal/sdn/dns.go reads —
// internal/sdndns joins PVE's config to the PowerDNS server, no second DNS
// client here), does a matching PTR exist in the corresponding reverse zone?
//
// GROUND TRUTH (read off pvecube 2026-08-28, PVE 9.2.4's own
// PVE::Network::SDN::Dns::PowerdnsPlugin.pm and PVE::Network::SDN::Subnets.pm
// — see this task's completion report, not filed as a transcript since the
// cluster has no DNS plugin configured to query live): forward and reverse
// DNS are two INDEPENDENTLY OPTIONAL plugin references on an SDN zone
// (`dns` vs `reversedns`) — a zone can have forward records with no reverse
// delegation at all, and the reverse zone lives at a completely different
// domain (an *.in-addr.arpa/*.ip6.arpa name computed from the address,
// standard RFC 1035 zone-cut rules) than the forward zone. Real PVE exposes
// no "list records"/"resolve" API at all (confirmed: `pvesh usage
// /cluster/sdn/dns/<id>` returns only get/set/delete, and `pvesh ls
// /cluster/sdn/dns/<id>` reports "does not define child links" — every
// record read PVE itself does goes straight to the PowerDNS HTTP API using
// the plugin's own url+key).
//
// T-4112 UPDATE. When this check was written, the seam it builds on was
// calling PVE routes that do not exist, so on any real cluster it received
// zero records for every zone and reported ptr_zone_unreadable for all of
// them — correctly, given what it was told, and permanently. This file needed
// no change for that: internal/sdndns now reads the PowerDNS server this
// comment already named as ground truth, and the check finally sees the
// records it was written against. The one real change here is Values: a
// PowerDNS rrset can hold several addresses under one name, and auditing only
// the first would report a covered address as uncovered.
//
// THREE STATES, not two:
//   - PTR present and matching -> no finding.
//   - PTR missing or pointing somewhere else -> ptr_missing / ptr_mismatch,
//     but ONLY inside a reverse zone vnprox actually has a config entry for
//     (an inventory.SdnDnsZone whose ID names an in-addr.arpa/ip6.arpa
//     domain covering the address). An address whose reverse domain matches
//     no zone vnprox knows about is silently out of scope — that reverse
//     zone is not delegated to this PowerDNS instance, so it is the
//     operator's wider DNS estate, not vnprox's business, and a false
//     finding there would train them to ignore this check (T-4109's card).
//   - "cannot determine": internal/collect's SDN DNS poll
//     (internal/collect/pve.go) skips a zone's ENTIRE record set on any
//     per-zone read error rather than partially populating it — so a
//     reverse zone inventory knows about but that contributed zero records
//     of any type is indistinguishable, from inventory alone, between
//     "genuinely empty" and "PowerDNS was unreachable at the last poll".
//     Reporting ptr_missing here would be exactly the missing-vs-unknown
//     conflation CLAUDE.md forbids, so this check raises a third, distinct
//     finding (ptr_zone_unreadable, informational) instead — once per
//     affected zone, not once per address, and only for a zone at least one
//     known forward record's reverse name actually falls inside.
//
// Hysteresis-exempt, the same reasoning as orphan_vnet/mgmt_single_path:
// whether a PTR record exists for a given forward record is a structural
// fact about current DNS config, not a noisy live counter to debounce.

package findings

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const (
	// CheckPtrMissing fires when a forward A/AAAA record has no PTR at all
	// in the reverse zone vnprox manages for its address.
	CheckPtrMissing = "ptr_missing"
	// CheckPtrMismatch fires when a PTR record exists for the address but
	// its target does not match the forward record's FQDN (stale/dangling).
	CheckPtrMismatch = "ptr_mismatch"
	// CheckPtrZoneUnreadable fires once per reverse zone that vnprox has a
	// config entry for, that at least one known forward record's address
	// falls inside, but that contributed zero records on the last poll —
	// "unknown", never collapsed into ptr_missing.
	CheckPtrZoneUnreadable = "ptr_zone_unreadable"
)

const ptrCoverageDocsLink = "docs/features/monitoring.md#5-health-checks"

// ptrZoneEntry is one reverse SdnDnsZone plus the SdnDnsRecord entities
// inventory currently has for it (any type — A/AAAA/PTR/CNAME/TXT all share
// one zone's record set in vnprox's model, mirroring internal/sdn/dns.go's
// DNSView.Records).
type ptrZoneEntry struct {
	zone    *inventory.SdnDnsZone
	records []*inventory.SdnDnsRecord
}

// checkPtrCoverage evaluates every forward A/AAAA record in snap against
// the reverse zone (if any) vnprox manages for its address.
func checkPtrCoverage(snap inventory.Snapshot) []Finding {
	zones := map[string]*ptrZoneEntry{}
	for _, e := range snap.All() {
		if z, ok := e.(*inventory.SdnDnsZone); ok {
			zones[z.ID] = &ptrZoneEntry{zone: z}
		}
	}
	for _, e := range snap.All() {
		r, ok := e.(*inventory.SdnDnsRecord)
		if !ok {
			continue
		}
		if zr, present := zones[r.Zone]; present {
			zr.records = append(zr.records, r)
		}
	}

	var out []Finding
	unreadable := map[string]bool{}

	for _, e := range snap.All() {
		rec, ok := e.(*inventory.SdnDnsRecord)
		if !ok || (!strings.EqualFold(rec.Type, "A") && !strings.EqualFold(rec.Type, "AAAA")) {
			continue
		}
		// Every address under this name, not just the first (T-4112). A
		// round-robin A record covers each of its addresses independently:
		// one may have a PTR and another may not, and reporting only the
		// first would hide half the gap.
		for _, value := range recordValues(rec) {
			ip := net.ParseIP(value)
			if ip == nil {
				continue // not a real address; nothing to audit
			}
			ptrName := reverseDNSName(ip)
			if ptrName == "" {
				continue
			}

			zr, label, managed := findReverseZone(zones, ptrName)
			if !managed {
				// Reverse zone not delegated to any DNS zone vnprox knows
				// about — out of scope, never a finding.
				continue
			}
			if len(zr.records) == 0 {
				unreadable[zr.zone.ID] = true
				continue
			}

			fwdFQDN := ptrFQDN(rec.Name, rec.Zone)
			var ptrRec *inventory.SdnDnsRecord
			for _, r := range zr.records {
				if strings.EqualFold(r.Type, "PTR") && strings.EqualFold(r.Name, label) {
					ptrRec = r
					break
				}
			}

			fwdRef := rec.GetRef().String()
			switch {
			case ptrRec == nil:
				detail := fmt.Sprintf(
					"%s (%s) has a forward %s record but no PTR in reverse zone %s — reverse lookups on %s will fail",
					fwdFQDN, value, strings.ToUpper(rec.Type), zr.zone.ID, value)
				f := newHealthFinding(CheckPtrMissing, SeverityWarning, detail, nil, []string{fwdRef})
				f.DocsLink = ptrCoverageDocsLink
				out = append(out, f)
			// A PTR rrset may itself hold several targets. Matching ANY of
			// them is a match: a resolver asked for this address gets every
			// target back, so a shared address with two names is correctly
			// configured, not a mismatch. Only when none matches is the
			// reverse record stale.
			case !ptrPointsAt(ptrRec, fwdFQDN):
				detail := fmt.Sprintf(
					"%s (%s) has a forward %s record, but its PTR in reverse zone %s points to %q instead — a stale or mismatched reverse record",
					fwdFQDN, value, strings.ToUpper(rec.Type), zr.zone.ID,
					strings.Join(recordValues(ptrRec), ", "))
				f := newHealthFinding(CheckPtrMismatch, SeverityWarning, detail, nil,
					[]string{fwdRef, ptrRec.GetRef().String()})
				f.DocsLink = ptrCoverageDocsLink
				out = append(out, f)
			}
		}
	}

	for zoneID := range unreadable {
		zr := zones[zoneID]
		detail := fmt.Sprintf(
			"reverse DNS zone %s is configured but PowerDNS returned no records for it on the last poll — PTR completeness cannot be verified for any address in this zone (vnprox cannot currently tell a genuinely empty zone from an unreachable one)",
			zoneID)
		f := newHealthFinding(CheckPtrZoneUnreadable, SeverityInfo, detail, nil,
			[]string{zr.zone.GetRef().String()})
		f.DocsLink = ptrCoverageDocsLink
		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ptrFQDN mirrors internal/sdn/dns.go's unexported fqdn(): a record's name
// label joined with its owning zone's domain into a canonical FQDN. Kept as
// a second tiny helper rather than an import of internal/sdn — that
// package's own fqdn() is unexported, and this is plain string composition,
// not a second read path to the PowerDNS server.
func ptrFQDN(name, zone string) string {
	switch {
	case name == "" || name == "@":
		return zone
	case zone == "":
		return name
	default:
		return name + "." + zone
	}
}

// ptrFQDNEqual compares two FQDNs the way DNS does: case-insensitive, and
// tolerant of a trailing root dot either side may or may not carry (real
// PowerDNS always writes one per PVE::Network::SDN::Dns::PowerdnsPlugin.pm;
// vnprox's own fqdn()/ptrFQDN never add one).
func ptrFQDNEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

// reverseDNSName returns ip's standard reverse-lookup owner name
// (RFC 1035 in-addr.arpa for v4, RFC 3596 nibble-reversed ip6.arpa for v6),
// without a trailing root dot — matching every other zone/record ID this
// package already stores without one. "" for an address that parses to
// neither (should not happen once net.ParseIP has already succeeded).
func reverseDNSName(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", v4[3], v4[2], v4[1], v4[0])
	}
	v6 := ip.To16()
	if v6 == nil {
		return ""
	}
	var b strings.Builder
	for i := len(v6) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%x.%x.", v6[i]&0x0f, v6[i]>>4)
	}
	b.WriteString("ip6.arpa")
	return b.String()
}

// findReverseZone returns the most specific reverse SdnDnsZone (longest
// domain suffix match against ptrName, real DNS zone-cut behavior — vnprox
// does not need to replicate PVE's CIDR-boundary zone-choice algorithm,
// which only matters for record *creation*, to audit whichever zone it was
// actually given) plus ptrName's record label relative to that zone. Only
// zone IDs naming an in-addr.arpa/ip6.arpa domain are eligible, so a
// same-named forward zone can never accidentally match.
func findReverseZone(zones map[string]*ptrZoneEntry, ptrName string) (*ptrZoneEntry, string, bool) {
	ptrName = strings.ToLower(strings.TrimSuffix(ptrName, "."))
	var best *ptrZoneEntry
	var bestID string
	for id, zr := range zones {
		zid := strings.ToLower(strings.TrimSuffix(id, "."))
		if !strings.Contains(zid, "in-addr.arpa") && !strings.Contains(zid, "ip6.arpa") {
			continue
		}
		if zid != ptrName && !strings.HasSuffix(ptrName, "."+zid) {
			continue
		}
		if best == nil || len(zid) > len(bestID) {
			best, bestID = zr, zid
		}
	}
	if best == nil {
		return nil, "", false
	}
	if bestID == ptrName {
		return best, "@", true
	}
	return best, strings.TrimSuffix(ptrName, "."+bestID), true
}

// recordValues returns every value a record carries, in a stable order
// (T-4112). Values is the full rrset; Value is its first entry, kept for the
// single-valued case and for callers that construct a record by hand. Reading
// Values with a Value fallback means a hand-built test fixture and a real
// poll behave the same, which is the only way this check's tests are worth
// anything.
func recordValues(rec *inventory.SdnDnsRecord) []string {
	if len(rec.Values) > 0 {
		return rec.Values
	}
	if rec.Value == "" {
		return nil
	}
	return []string{rec.Value}
}

// ptrPointsAt reports whether a PTR rrset names fqdn among its targets.
//
// Any match counts. A resolver asked for the address receives every target in
// the rrset, so an address deliberately given two names resolves correctly
// for both; calling that a mismatch would train an operator to ignore this
// check, which is the failure mode T-4109's card names explicitly.
func ptrPointsAt(ptr *inventory.SdnDnsRecord, fqdn string) bool {
	for _, target := range recordValues(ptr) {
		if ptrFQDNEqual(target, fqdn) {
			return true
		}
	}
	return false
}
