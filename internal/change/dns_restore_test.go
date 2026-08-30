// SPDX-License-Identifier: Apache-2.0

package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// The DNS half of the rollback plan (T-4114).
//
// Renaming sdn.dns.zone.* to sdn.dns.server.* forced a question the old name
// let everyone avoid: when the rollback reconciles "DNS zones", which object
// is it reconciling? It was reconciling DERIVED DNS DOMAINS with ops that can
// only manage PowerDNS SERVER CONNECTIONS, which is why the ops it produced
// could not validate — and nothing noticed, because no test ever ran a
// restore op back through the validator that would apply to it.

// A domain is not an object PVE has an API for, so no op may target one. This
// is the defect that motivated the rewrite: the restore used to emit
// sdn.dns.zone.create with a dotted domain as its target id, carrying none of
// the type/url/key PVE requires — four schema violations at once, on the
// rollback path.
func TestDnsRestore_DomainsProduceNoOps(t *testing.T) {
	pre := SDNConfig{
		DnsZones: []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1", TTL: 600}},
	}
	removals, recreations := sdnDnsRestoreOps(pre, SDNConfig{})

	for _, ro := range append(removals, recreations...) {
		if ro.op.Target.Kind == inventory.KindSDNDnsZone {
			t.Errorf("the restore emitted an op targeting a DNS domain, which has no API: %s %s",
				ro.op.Type, ro.op.Target)
		}
	}
}

// Every op the restore DOES emit has to survive the same schema validation a
// staged op does. This is the assertion whose absence let the old behaviour
// live: the ops were produced, never checked, and only failed against real
// PVE at the worst possible moment.
func TestDnsRestore_EveryEmittedOpValidates(t *testing.T) {
	pre := SDNConfig{
		DnsServers: []SDNDnsServerConfig{
			{ID: "pdns1", Type: "powerdns", URL: "https://ns1.example:8081/api/v1/servers/localhost", TTL: 600},
			{ID: "pdns2", Type: "powerdns", URL: "https://ns2.example:8081/api/v1/servers/localhost", TTL: 900},
		},
		DnsZones:   []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1", TTL: 600}},
		DnsRecords: []SDNDnsRecordConfig{{ID: "lab.example./web/A", Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5"}},
	}
	// current: pdns1 survives but with a changed TTL, pdns2 is gone, a record
	// was added that pre did not have.
	current := SDNConfig{
		DnsServers: []SDNDnsServerConfig{
			{ID: "pdns1", Type: "powerdns", URL: "https://ns1.example:8081/api/v1/servers/localhost", TTL: 30},
		},
		DnsZones:   []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1", TTL: 600}},
		DnsRecords: []SDNDnsRecordConfig{{ID: "lab.example./db/A", Zone: "lab.example.", Name: "db", Type: "A", Value: "10.0.0.9"}},
	}

	removals, recreations := sdnDnsRestoreOps(pre, current)
	all := append(removals, recreations...)
	if len(all) == 0 {
		t.Fatal("no inverse ops produced for a snapshot that clearly differs")
	}

	for _, ro := range all {
		// A blocked op is one the restore knows it cannot execute; it is
		// reported to the operator, not staged, so it is not held to this.
		if ro.blocked != "" {
			continue
		}
		for _, f := range schemaValidate([]Op{ro.op}) {
			t.Errorf("restore op %s %s does not validate: %s: %s",
				ro.op.Type, ro.op.Target.ID, f.Code, f.Message)
		}
	}
}

// Recreating a deleted PowerDNS connection is knowable but not executable:
// PVE never returns the API key on a read, so no snapshot can hold one. The
// restore must say so rather than emit an op guaranteed to fail or skip the
// object in silence — a rollback that quietly does nothing reads as success.
func TestDnsRestore_ARecreatedConnectionReportsTheMissingKey(t *testing.T) {
	pre := SDNConfig{DnsServers: []SDNDnsServerConfig{
		{ID: "pdns1", Type: "powerdns", URL: "https://ns1.example:8081/api/v1/servers/localhost"},
	}}

	_, recreations := sdnDnsRestoreOps(pre, SDNConfig{})
	if len(recreations) != 1 {
		t.Fatalf("recreations = %+v, want the one connection", recreations)
	}
	got := recreations[0]
	if got.blocked == "" {
		t.Fatal("the restore claims it can recreate a connection whose key it cannot have")
	}
	for _, want := range []string{"pdns1", "key"} {
		if !strings.Contains(got.blocked, want) {
			t.Errorf("blocked reason %q does not mention %q", got.blocked, want)
		}
	}
}

// T-4112 added DnsUnreadable with an explicit rationale — "a rollback that
// silently restores 'no records' for a domain whose PowerDNS server happened
// to be unreachable at capture time would delete records nobody asked it to"
// — and then nothing read the field. This is that rationale as a test.
func TestDnsRestore_RecordsInAnUnreadableDomainAreLeftAlone(t *testing.T) {
	// pre could not read lab.example, so its record list for that domain is
	// "unknown", not "empty".
	pre := SDNConfig{
		DnsZones:      []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1"}},
		DnsUnreadable: []SDNDnsUnreadable{{ID: "lab.example.", Reason: "connection refused"}},
	}
	// The live side has records. Diffing them against pre's silence would
	// delete every one.
	current := SDNConfig{
		DnsZones: []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1"}},
		DnsRecords: []SDNDnsRecordConfig{
			{ID: "lab.example./web/A", Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5"},
			{ID: "lab.example./db/A", Zone: "lab.example.", Name: "db", Type: "A", Value: "10.0.0.9"},
		},
	}

	removals, recreations := sdnDnsRestoreOps(pre, current)
	for _, ro := range append(removals, recreations...) {
		if ro.op.Target.Kind == inventory.KindSDNDnsRecord {
			t.Errorf("a domain the snapshot could not read produced record op %s %s — "+
				"unknown was treated as empty", ro.op.Type, ro.op.Target.ID)
		}
	}
}

// The same protection when it is the LIVE side that cannot be read: absence
// there means "not observed", and diffing against it would recreate every
// record the snapshot holds on top of ones that may still exist.
func TestDnsRestore_RecordsAreLeftAloneWhenTheLiveSideIsUnreadable(t *testing.T) {
	pre := SDNConfig{
		DnsZones:   []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1"}},
		DnsRecords: []SDNDnsRecordConfig{{ID: "lab.example./web/A", Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5"}},
	}
	current := SDNConfig{
		DnsZones:      []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1"}},
		DnsUnreadable: []SDNDnsUnreadable{{ID: "lab.example.", Reason: "connection refused"}},
	}

	_, recreations := sdnDnsRestoreOps(pre, current)
	for _, ro := range recreations {
		if ro.op.Target.Kind == inventory.KindSDNDnsRecord {
			t.Errorf("an unreadable live side produced record op %s %s", ro.op.Type, ro.op.Target.ID)
		}
	}
}

// A readable domain still reconciles normally — the guard above must not have
// turned the record half off altogether.
func TestDnsRestore_RecordsInAReadableDomainStillReconcile(t *testing.T) {
	pre := SDNConfig{
		DnsZones:   []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1"}},
		DnsRecords: []SDNDnsRecordConfig{{ID: "lab.example./web/A", Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5"}},
	}
	current := SDNConfig{DnsZones: []SDNDnsZoneConfig{{ID: "lab.example.", DNS: "pdns1"}}}

	_, recreations := sdnDnsRestoreOps(pre, current)
	var found bool
	for _, ro := range recreations {
		if ro.op.Type == OpSdnDnsRecordCreate && ro.op.Target.ID == "lab.example./web/A" {
			found = true
		}
	}
	if !found {
		t.Errorf("a readable domain's missing record was not restored: %+v", recreations)
	}
}

// --- the deletion guard -----------------------------------------------------

// dnsZoneDeletionGuardFindings keyed the deleted-op index by connection id and
// then looked it up by DNS domain. Those are different namespaces after
// T-4112, so the lookup could never hit: the guard passed on every input,
// including the one it exists to catch. The join it was missing is
// SdnDnsZone.DNS — the domain's pointer at the connection serving it.
func TestDnsDeletionGuard_FiresWhenAConnectionStillServesRecords(t *testing.T) {
	snap := previewSnapshot(
		&inventory.SdnDnsZone{
			Ref: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: "lab.example."},
			ID:  "lab.example.", DNS: "pdns1",
		},
		&inventory.SdnDnsRecord{
			Ref:  inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example./web/A"},
			Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5",
		},
	)

	findings := dnsZoneDeletionGuardFindings([]Op{
		mkOp(OpSdnDnsServerDelete, inventory.Ref{Kind: inventory.KindSDNDnsServer, ID: "pdns1"}, &SdnDnsServerDeleteParams{}),
	}, snap)

	if len(findings) == 0 {
		t.Fatal("deleting a connection that still serves records was allowed")
	}
	if !strings.Contains(findings[0].Message, "web/A") {
		t.Errorf("finding %q does not name the record that blocks the delete", findings[0].Message)
	}
	// One connection can serve several domains, so "web/A" alone would not
	// tell an operator where to look.
	if !strings.Contains(findings[0].Message, "lab.example.") {
		t.Errorf("finding %q does not name the domain the record is in", findings[0].Message)
	}
}

// Net-effect awareness, matching the subnet/vnet guards: deleting every record
// and then the connection is a legitimate single changeset.
func TestDnsDeletionGuard_CascadedDeletesValidateClean(t *testing.T) {
	snap := previewSnapshot(
		&inventory.SdnDnsZone{
			Ref: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: "lab.example."},
			ID:  "lab.example.", DNS: "pdns1",
		},
		&inventory.SdnDnsRecord{
			Ref:  inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example./web/A"},
			Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5",
		},
	)

	findings := dnsZoneDeletionGuardFindings([]Op{
		mkOp(OpSdnDnsRecordDelete, inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example./web/A"}, &SdnDnsRecordDeleteParams{}),
		mkOp(OpSdnDnsServerDelete, inventory.Ref{Kind: inventory.KindSDNDnsServer, ID: "pdns1"}, &SdnDnsServerDeleteParams{}),
	}, snap)

	if len(findings) != 0 {
		t.Errorf("cascaded delete was blocked: %+v", findings)
	}
}

// A connection serving no domains deletes freely — the guard must not fire on
// everything now that it fires at all.
func TestDnsDeletionGuard_AnUnusedConnectionDeletesFreely(t *testing.T) {
	snap := previewSnapshot(
		&inventory.SdnDnsZone{
			Ref: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: "lab.example."},
			ID:  "lab.example.", DNS: "pdns2",
		},
		&inventory.SdnDnsRecord{
			Ref:  inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example./web/A"},
			Zone: "lab.example.", Name: "web", Type: "A", Value: "10.0.0.5",
		},
	)

	findings := dnsZoneDeletionGuardFindings([]Op{
		mkOp(OpSdnDnsServerDelete, inventory.Ref{Kind: inventory.KindSDNDnsServer, ID: "pdns1"}, &SdnDnsServerDeleteParams{}),
	}, snap)

	if len(findings) != 0 {
		t.Errorf("deleting a connection that serves nothing was blocked: %+v", findings)
	}
}
