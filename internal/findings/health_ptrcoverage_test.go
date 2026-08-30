// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ptrDNSZone/ptrDNSRecord build inventory.SdnDnsZone/SdnDnsRecord entities
// with the same Ref shape internal/inventory/ingest.go's FromPVEDNS
// produces (T-1204), so these tests exercise the check exactly as the
// collector would populate it.
func ptrDNSZone(zoneID string) *inventory.SdnDnsZone {
	return &inventory.SdnDnsZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: zoneID},
		ID:  zoneID,
	}
}

func ptrDNSRecord(zoneID, name, typ, value string) *inventory.SdnDnsRecord {
	return &inventory.SdnDnsRecord{
		Ref:   inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: zoneID + "/" + name + "/" + typ},
		Zone:  zoneID,
		Name:  name,
		Type:  typ,
		Value: value,
	}
}

// TestPtrCoverage_States is T-4109's table-driven core: the four states the
// task card requires distinguishing (PTR present, PTR missing, reverse zone
// not managed, plugin unreachable) plus the mismatch case the acceptance
// criteria also requires as a distinct finding from "missing".
func TestPtrCoverage_States(t *testing.T) {
	// Field order: map (1 pointer word), string (16 bytes), slice (24
	// bytes) — densest-pointer-first, per docs/development.md's Go
	// standards on govet's fieldalignment.
	cases := []struct {
		wantChecks map[string]int
		name       string
		entities   []inventory.Entity
	}{
		{
			name: "PTR present and matching: no finding",
			entities: []inventory.Entity{
				ptrDNSZone("example.com"),
				ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
				ptrDNSZone("1.168.192.in-addr.arpa"),
				ptrDNSRecord("1.168.192.in-addr.arpa", "50", "PTR", "host1.example.com."),
			},
			wantChecks: map[string]int{},
		},
		{
			name: "PTR missing: reverse zone reachable (has other records) but none for this address",
			entities: []inventory.Entity{
				ptrDNSZone("example.com"),
				ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
				ptrDNSZone("1.168.192.in-addr.arpa"),
				ptrDNSRecord("1.168.192.in-addr.arpa", "51", "PTR", "other.example.com."),
			},
			wantChecks: map[string]int{findings.CheckPtrMissing: 1},
		},
		{
			name: "PTR mismatch: stale/dangling PTR pointing elsewhere",
			entities: []inventory.Entity{
				ptrDNSZone("example.com"),
				ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
				ptrDNSZone("1.168.192.in-addr.arpa"),
				ptrDNSRecord("1.168.192.in-addr.arpa", "50", "PTR", "wrong.example.com."),
			},
			wantChecks: map[string]int{findings.CheckPtrMismatch: 1},
		},
		{
			name: "reverse zone not managed: no zone in inventory covers this address at all",
			entities: []inventory.Entity{
				ptrDNSZone("example.com"),
				ptrDNSRecord("example.com", "host2", "A", "203.0.113.10"),
			},
			wantChecks: map[string]int{},
		},
		{
			name: "plugin unreachable: zone known but contributed zero records -> unknown, never ptr_missing",
			entities: []inventory.Entity{
				ptrDNSZone("example.com"),
				ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
				ptrDNSZone("1.168.192.in-addr.arpa"),
				// no SdnDnsRecord entities at all for 1.168.192.in-addr.arpa:
				// mirrors internal/collect/pve.go skipping a zone's whole
				// record set on a per-zone ListSDNDnsRecords error.
			},
			wantChecks: map[string]int{findings.CheckPtrZoneUnreadable: 1},
		},
		{
			name: "AAAA forward record, matching PTR in an ip6.arpa zone: no finding",
			entities: []inventory.Entity{
				ptrDNSZone("example.com"),
				ptrDNSRecord("example.com", "host3", "AAAA", "2001:db8::1"),
				ptrDNSZone("8.b.d.0.1.0.0.2.ip6.arpa"),
				ptrDNSRecord("8.b.d.0.1.0.0.2.ip6.arpa",
					"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0", "PTR", "host3.example.com."),
			},
			wantChecks: map[string]int{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGraphWithNodes("pve1")
			g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, tc.entities)
			eng := findings.New(findings.Config{Graph: g})
			got := eng.Findings()

			gotCounts := map[string]int{}
			for _, f := range got {
				switch f.Check {
				case findings.CheckPtrMissing, findings.CheckPtrMismatch, findings.CheckPtrZoneUnreadable:
					gotCounts[f.Check]++
				}
			}
			for check, want := range tc.wantChecks {
				if gotCounts[check] != want {
					t.Errorf("check %s: got %d findings, want %d (all findings: %+v)", check, gotCounts[check], want, got)
				}
			}
			for check, n := range gotCounts {
				if tc.wantChecks[check] != n {
					t.Errorf("unexpected count for check %s: got %d, want %d (all findings: %+v)", check, n, tc.wantChecks[check], got)
				}
			}
		})
	}
}

// TestPtrCoverage_Missing_FindingShape checks the ptr_missing finding's
// field shape: detection-only (never fixable), a DocsLink is always set
// (the earlier card in this session shipped a docs-link constant that was
// declared but never wired onto a Finding — this asserts it actually is),
// and the detail names both the forward FQDN and its address.
func TestPtrCoverage_Missing_FindingShape(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		ptrDNSZone("example.com"),
		ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
		ptrDNSZone("1.168.192.in-addr.arpa"),
		ptrDNSRecord("1.168.192.in-addr.arpa", "51", "PTR", "other.example.com."),
	})
	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckPtrMissing)
	if len(found) != 1 {
		t.Fatalf("got %d ptr_missing findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Error("ptr_missing should never be fixable — reverse DNS can't be safely auto-remediated by the check")
	}
	if f.DocsLink == "" {
		t.Error("ptr_missing must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "host1.example.com") || !strings.Contains(f.Detail, "192.168.1.50") {
		t.Errorf("detail = %q, want mention of host1.example.com and 192.168.1.50", f.Detail)
	}
	if f.Severity != findings.SeverityWarning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
}

// TestPtrCoverage_Mismatch_FindingShape mirrors the missing-finding shape
// test for the mismatch case, and confirms the two are genuinely distinct
// checks (T-4109 AC2).
func TestPtrCoverage_Mismatch_FindingShape(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		ptrDNSZone("example.com"),
		ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
		ptrDNSZone("1.168.192.in-addr.arpa"),
		ptrDNSRecord("1.168.192.in-addr.arpa", "50", "PTR", "wrong.example.com."),
	})
	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckPtrMismatch)
	if len(found) != 1 {
		t.Fatalf("got %d ptr_mismatch findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.DocsLink == "" {
		t.Error("ptr_mismatch must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "wrong.example.com") {
		t.Errorf("detail = %q, want mention of the stale target wrong.example.com", f.Detail)
	}
	if got := findByCheck(t, eng.Findings(), findings.CheckPtrMissing); len(got) != 0 {
		t.Errorf("a mismatched PTR must not also raise ptr_missing, got %+v", got)
	}
}

// TestPtrCoverage_ZoneUnreadable_FindingShape: the "cannot determine" state
// is informational, not a false "missing" — and is raised once per zone,
// not once per forward record it covers.
func TestPtrCoverage_ZoneUnreadable_FindingShape(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		ptrDNSZone("example.com"),
		ptrDNSRecord("example.com", "host1", "A", "192.168.1.50"),
		ptrDNSRecord("example.com", "host2", "A", "192.168.1.51"),
		ptrDNSZone("1.168.192.in-addr.arpa"),
	})
	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckPtrZoneUnreadable)
	if len(found) != 1 {
		t.Fatalf("got %d ptr_zone_unreadable findings for two addresses in the same unreadable zone, want 1 (deduped per zone): %+v", len(found), found)
	}
	f := found[0]
	if f.Severity != findings.SeverityInfo {
		t.Errorf("severity = %q, want info — this is a coverage gap in the check itself, not a confirmed DNS problem", f.Severity)
	}
	if f.DocsLink == "" {
		t.Error("ptr_zone_unreadable must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "1.168.192.in-addr.arpa") {
		t.Errorf("detail = %q, want the zone named", f.Detail)
	}
	if got := findByCheck(t, eng.Findings(), findings.CheckPtrMissing); len(got) != 0 {
		t.Errorf("an unreadable zone must never be reported as ptr_missing, got %+v", got)
	}
}

// TestPtrCoverage_NotManaged_NoFinding: an address whose reverse zone
// vnprox has no config entry for at all is silently out of scope — not
// ptr_missing, not ptr_zone_unreadable, nothing (T-4109's "do not report on
// zones vnprox does not manage").
func TestPtrCoverage_NotManaged_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		ptrDNSZone("example.com"),
		ptrDNSRecord("example.com", "host2", "A", "203.0.113.10"),
	})
	eng := findings.New(findings.Config{Graph: g})
	for _, check := range []string{findings.CheckPtrMissing, findings.CheckPtrMismatch, findings.CheckPtrZoneUnreadable} {
		if got := findByCheck(t, eng.Findings(), check); len(got) != 0 {
			t.Errorf("unmanaged reverse zone raised %s, want silence: %+v", check, got)
		}
	}
}

// ptrDNSRecordMulti builds a record with a multi-valued rrset, the shape
// PowerDNS returns for a round-robin A record or an address with two names
// (T-4112). Value is the first entry, matching what FromPVEDNS produces.
func ptrDNSRecordMulti(zoneID, name, typ string, values ...string) *inventory.SdnDnsRecord {
	rec := ptrDNSRecord(zoneID, name, typ, values[0])
	rec.Values = values
	return rec
}

// ptrFindings runs the engine over a fixed entity set and returns only this
// check's findings.
func ptrFindings(entities ...inventory.Entity) []findings.Finding {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, entities)
	var out []findings.Finding
	for _, f := range findings.New(findings.Config{Graph: g}).Findings() {
		switch f.Check {
		case findings.CheckPtrMissing, findings.CheckPtrMismatch, findings.CheckPtrZoneUnreadable:
			out = append(out, f)
		}
	}
	return out
}

// A round-robin A record covers each of its addresses independently. Before
// T-4112 the check read only Value, so the second address was never audited
// at all — an uncovered address reported as fine, which is the direction that
// costs an operator a working reverse lookup.
func TestPtrCoverage_AuditsEveryAddressInAnRRSet(t *testing.T) {
	got := ptrFindings(
		ptrDNSZone("example.com"),
		ptrDNSZone("0.0.10.in-addr.arpa"),
		ptrDNSRecordMulti("example.com", "web", "A", "10.0.0.5", "10.0.0.6"),
		// Only .5 has a PTR.
		ptrDNSRecord("0.0.10.in-addr.arpa", "5", "PTR", "web.example.com."),
	)

	var missing []findings.Finding
	for _, f := range got {
		if f.Check == findings.CheckPtrMissing {
			missing = append(missing, f)
		}
	}
	if len(missing) != 1 {
		t.Fatalf("ptr_missing findings = %d, want exactly one (for 10.0.0.6): %+v", len(missing), got)
	}
	if !strings.Contains(missing[0].Detail, "10.0.0.6") {
		t.Errorf("detail = %q, want it to name the uncovered address", missing[0].Detail)
	}
	if strings.Contains(missing[0].Detail, "10.0.0.5") {
		t.Errorf("detail = %q, names the address that IS covered", missing[0].Detail)
	}
}

// A PTR rrset with several targets is a correct configuration, not a
// mismatch: a resolver receives every target, so an address deliberately
// given two names resolves for both. Reporting it would train an operator to
// ignore this check, which is the failure T-4109's card names.
func TestPtrCoverage_MultiTargetPTRMatchesAnyName(t *testing.T) {
	got := ptrFindings(
		ptrDNSZone("example.com"),
		ptrDNSZone("0.0.10.in-addr.arpa"),
		ptrDNSRecord("example.com", "web", "A", "10.0.0.5"),
		ptrDNSRecordMulti("0.0.10.in-addr.arpa", "5", "PTR", "mail.example.com.", "web.example.com."),
	)

	for _, f := range got {
		if f.Check == findings.CheckPtrMismatch {
			t.Fatalf("a PTR rrset naming this record among its targets was reported as a mismatch: %q", f.Detail)
		}
	}
}

// ...and when NONE of the targets matches, it is still a mismatch, and the
// detail names all of them so the operator can see what is actually there.
func TestPtrCoverage_MultiTargetPTRWithNoMatchIsAMismatch(t *testing.T) {
	got := ptrFindings(
		ptrDNSZone("example.com"),
		ptrDNSZone("0.0.10.in-addr.arpa"),
		ptrDNSRecord("example.com", "web", "A", "10.0.0.5"),
		ptrDNSRecordMulti("0.0.10.in-addr.arpa", "5", "PTR", "mail.example.com.", "old.example.com."),
	)

	var mismatch *findings.Finding
	for i := range got {
		if got[i].Check == findings.CheckPtrMismatch {
			mismatch = &got[i]
			break
		}
	}
	if mismatch == nil {
		t.Fatalf("want a ptr_mismatch when no target matches the forward record, got %+v", got)
	}
	for _, want := range []string{"mail.example.com.", "old.example.com."} {
		if !strings.Contains(mismatch.Detail, want) {
			t.Errorf("detail %q does not name the existing target %s", mismatch.Detail, want)
		}
	}
}
