// SPDX-License-Identifier: Apache-2.0

package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// validateReferentialForTest runs class-2 validation over ops against a
// snapshot seeded with entities.
func validateReferentialForTest(t *testing.T, ops []Op, entities ...inventory.Entity) []Finding {
	t.Helper()
	return referentialValidate(ops, previewSnapshot(entities...))
}

// T-4112: sdn.dns.zone.* creates a PowerDNS server CONNECTION, and the
// referential projection used to file it under the DNS DOMAIN index. That
// made this changeset validate clean:
//
//	sdn.dns.zone.create   pdns1                     (a server connection)
//	sdn.dns.record.create pdns1/web/A               (a record in "zone pdns1")
//
// ...because the create had just inserted "pdns1" into the domain index. The
// apply would then try to write a record into a DNS domain that does not
// exist. The two indexes are separate now, so the record op is rejected at
// stage time with a message naming the zone.
//
// The op family's target carries KindSDNDnsServer (T-4114).
func TestReferential_ARecordCannotUseAServerConnectionAsItsZone(t *testing.T) {
	server := inventory.Ref{Kind: inventory.KindSDNDnsServer, ID: "pdns1"}
	record := inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "pdns1/web/A"}

	findings := validateReferentialForTest(t, []Op{
		mkOp(OpSdnDnsServerCreate, server, &SdnDnsServerCreateParams{
			Type: "powerdns", URL: "https://pdns:8081/api/v1/servers/localhost", Key: "k",
		}),
		mkOp(OpSdnDnsRecordCreate, record, &SdnDnsRecordCreateParams{
			Zone: "pdns1", Name: "web", Type: "A", Value: "10.0.0.5",
		}),
	})

	var found bool
	for _, f := range findings {
		if f.Code == codeDNSZoneNotFound && strings.Contains(f.Message, "pdns1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a record naming a server connection as its zone validated clean; findings = %+v", findings)
	}
}

// The other half: a record in a domain that genuinely exists in inventory
// must still validate. Without this the test above could be satisfied by a
// check that rejects everything.
func TestReferential_ARecordInARealDomainStillValidates(t *testing.T) {
	domain := inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: "lab.example"}
	record := inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example/web/A"}

	findings := validateReferentialForTest(t,
		[]Op{mkOp(OpSdnDnsRecordCreate, record, &SdnDnsRecordCreateParams{
			Zone: "lab.example", Name: "web", Type: "A", Value: "10.0.0.5",
		})},
		&inventory.SdnDnsZone{Ref: domain, ID: "lab.example", DNS: "pdns1"},
	)

	for _, f := range findings {
		if f.Code == codeDNSZoneNotFound {
			t.Fatalf("a record in a real domain was rejected: %s", f.Message)
		}
	}
}

// A sdn.dns.zone.update against a connection this same changeset created must
// still resolve — the connection has no inventory entity, so existence can
// only come from the changeset itself.
func TestReferential_AServerConnectionCreatedHereCanBeUpdated(t *testing.T) {
	server := inventory.Ref{Kind: inventory.KindSDNDnsServer, ID: "pdns1"}

	findings := validateReferentialForTest(t, []Op{
		mkOp(OpSdnDnsServerCreate, server, &SdnDnsServerCreateParams{
			Type: "powerdns", URL: "https://pdns:8081/api/v1/servers/localhost", Key: "k",
		}),
		mkOp(OpSdnDnsServerUpdate, server, &SdnDnsServerUpdateParams{TTL: intPtr(120)}),
	})

	for _, f := range findings {
		if f.Code == codeTargetNotFound {
			t.Fatalf("a connection created in this changeset was not found by a later op: %s", f.Message)
		}
	}
}
