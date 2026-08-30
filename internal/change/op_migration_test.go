// SPDX-License-Identifier: Apache-2.0

package change

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// T-4114 renamed sdn.dns.zone.* to sdn.dns.server.*. A rename of a wire
// contract is only safe if the old contract keeps working, so these tests
// exercise the old spelling end to end rather than asserting the alias table
// has the right entries in it — a table can be right while nothing reads it.
//
// What an operator can be holding: a changeset saved from a previous release,
// or one built by a Terraform/Ansible integration pinned to the old constant.
// Both arrive as JSON carrying BOTH the old op string and the old target
// kind, because the two changed together.

func TestOpMigration_ADeprecatedChangesetStillDecodes(t *testing.T) {
	// Verbatim the shape a pre-T-4114 client sends.
	const saved = `{
		"op": "sdn.dns.zone.create",
		"target": "sdn-dns-zone::pdns1",
		"params": {"type": "powerdns", "url": "https://ns1.example:8081/api/v1/servers/localhost", "key": "secret", "ttl": 3600}
	}`

	var op Op
	if err := json.Unmarshal([]byte(saved), &op); err != nil {
		t.Fatalf("a changeset saved before the rename no longer decodes: %v", err)
	}

	if op.Type != OpSdnDnsServerCreate {
		t.Errorf("op type = %q, want it canonicalized to %q", op.Type, OpSdnDnsServerCreate)
	}
	// The target kind must be rewritten too. Leaving it as sdn-dns-zone would
	// send the op to the DNS-domain index in validate_projection.go, where a
	// connection id can never be found.
	if op.Target.Kind != inventory.KindSDNDnsServer {
		t.Errorf("target kind = %q, want %q", op.Target.Kind, inventory.KindSDNDnsServer)
	}
	if op.Target.ID != "pdns1" {
		t.Errorf("target id = %q, want it preserved", op.Target.ID)
	}
	params, ok := op.Params.(*SdnDnsServerCreateParams)
	if !ok {
		t.Fatalf("params = %T, want *SdnDnsServerCreateParams", op.Params)
	}
	if params.URL == "" || params.Key == "" {
		t.Errorf("params lost fields in the rewrite: %+v", params)
	}
}

// Re-encoding a decoded op emits the NEW name. Accepting both spellings while
// emitting only one is what keeps the deprecation finite: a changeset that
// round-trips through vnprox comes back canonical.
func TestOpMigration_DecodingADeprecatedOpEmitsTheCurrentName(t *testing.T) {
	var op Op
	if err := json.Unmarshal([]byte(`{"op":"sdn.dns.zone.delete","target":"sdn-dns-zone::pdns1","params":{}}`), &op); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"sdn.dns.server.delete"`) {
		t.Errorf("re-encoded op = %s, want the current op name", got)
	}
	if strings.Contains(got, `"sdn.dns.zone.delete"`) {
		t.Errorf("re-encoded op still emits the retired name: %s", got)
	}
	if !strings.Contains(got, "sdn-dns-server:") {
		t.Errorf("re-encoded target = %s, want the current kind", got)
	}
}

// A deprecated changeset must also VALIDATE, not merely decode. This is the
// half that would fail if the alias stopped at the op string: schema
// validation reads op.Target.ID against PVE's connection-id pattern, and the
// referential check asks which index the target belongs in.
func TestOpMigration_ADeprecatedChangesetStillValidates(t *testing.T) {
	var op Op
	const saved = `{
		"op": "sdn.dns.zone.create",
		"target": "sdn-dns-zone::pdns1",
		"params": {"type": "powerdns", "url": "https://ns1.example:8081/api/v1/servers/localhost", "key": "secret"}
	}`
	if err := json.Unmarshal([]byte(saved), &op); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, f := range Validate([]Op{op}, previewSnapshot()) {
		if f.Severity == SeverityError {
			t.Errorf("a changeset saved before the rename no longer validates: %s: %s", f.Code, f.Message)
		}
	}
}

// The rewrite is scoped to the three server ops. sdn-dns-zone is still a live
// kind naming a real, different object — a DNS domain — and a record op that
// names one must keep meaning a domain. A blanket kind rewrite would have
// been the easy bug here.
func TestOpMigration_TheKindRewriteDoesNotLeakToRecordOps(t *testing.T) {
	var op Op
	const saved = `{
		"op": "sdn.dns.record.create",
		"target": "sdn-dns-record::lab.example/web/A",
		"params": {"zone": "lab.example", "name": "web", "type": "A", "value": "10.0.0.5"}
	}`
	if err := json.Unmarshal([]byte(saved), &op); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if op.Target.Kind != inventory.KindSDNDnsRecord {
		t.Errorf("record target kind = %q, want it untouched", op.Target.Kind)
	}
}

// The deprecated names are decodable but must not be advertised: an operator
// reading the op vocabulary should be told the current one.
func TestOpMigration_RetiredNamesAreNotInTheAdvertisedVocabulary(t *testing.T) {
	for _, ot := range KnownOpTypes() {
		if ot.Deprecated() {
			t.Errorf("KnownOpTypes advertises the retired name %q", ot)
		}
	}
	// ...and every retired name must map to one that IS advertised, or the
	// alias sends a changeset somewhere that no longer exists.
	known := map[OpType]bool{}
	for _, ot := range KnownOpTypes() {
		known[ot] = true
	}
	for old, current := range DeprecatedOpTypes() {
		if !known[current] {
			t.Errorf("retired name %q maps to %q, which is not a known op type", old, current)
		}
	}
}
