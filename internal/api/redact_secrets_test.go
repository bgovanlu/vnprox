// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// The sentinel appears nowhere else, so a single search of the serialized
// output is a complete check: if any op family grows a secret field and
// nobody teaches redactOpSecrets about it, the marshalled JSON will carry
// this string and TestRedactOpSecrets_NothingSecretSurvivesSerialization
// fails. That is the point — a per-field assertion only ever tests the
// fields someone remembered.
const secretSentinel = "SECRET-DO-NOT-ECHO"

func opsWithEverySecret() []change.Op {
	return []change.Op{
		{Params: &change.WgPeerAddParams{PresharedKey: secretSentinel, PresharedKeyEnc: []byte(secretSentinel)}},
		{Params: &change.SdnDnsServerCreateParams{Type: "powerdns", URL: "https://pdns:8081", Key: secretSentinel}},
		{Params: &change.SdnDnsServerUpdateParams{Key: strPtr(secretSentinel)}},
		{Params: &change.SdnIpamCreateParams{Type: "netbox", URL: "https://netbox", Token: secretSentinel}},
		{Params: &change.SdnIpamUpdateParams{Token: strPtr(secretSentinel)}},
	}
}

func strPtr(s string) *string { return &s }

func TestRedactOpSecrets_NothingSecretSurvivesSerialization(t *testing.T) {
	ops := opsWithEverySecret()

	// Prove the check measures something: unredacted, every one of these
	// secrets does reach the wire. Without this line the test would still
	// pass if opsWithEverySecret stopped setting them.
	before, err := json.Marshal(ops)
	if err != nil {
		t.Fatalf("marshalling ops: %v", err)
	}
	if want := strings.Count(string(before), secretSentinel); want < len(ops) {
		t.Fatalf("fixture puts the sentinel on the wire %d times, want at least one per op (%d)", want, len(ops))
	}

	raw, err := json.Marshal(redactOpSecrets(ops))
	if err != nil {
		t.Fatalf("marshalling redacted ops: %v", err)
	}
	if strings.Contains(string(raw), secretSentinel) {
		t.Fatalf("a secret survived redaction into the wire form:\n%s", raw)
	}
}

// Redaction must not mutate the caller's ops: the stored changeset — which
// the apply path reads — still needs the key it was staged with. This is the
// property that makes redaction safe to apply on every read.
func TestRedactOpSecrets_LeavesTheStoredOpsIntact(t *testing.T) {
	ops := opsWithEverySecret()
	_ = redactOpSecrets(ops)

	dns, ok := ops[1].Params.(*change.SdnDnsServerCreateParams)
	if !ok {
		t.Fatal("test fixture changed shape")
	}
	if dns.Key != secretSentinel {
		t.Error("redaction mutated the caller's ops — the apply path would have no key to use")
	}
	wg, ok := ops[0].Params.(*change.WgPeerAddParams)
	if !ok {
		t.Fatal("test fixture changed shape")
	}
	if wg.PresharedKey != secretSentinel {
		t.Error("redaction mutated the caller's wireguard op")
	}
}

// The non-secret fields have to survive, or the redacted view stops being
// useful for review — an operator approving a changeset needs to see which
// server it points at, just not the key.
func TestRedactOpSecrets_KeepsEverythingElse(t *testing.T) {
	got := redactOpSecrets(opsWithEverySecret())

	dns, ok := got[1].Params.(*change.SdnDnsServerCreateParams)
	if !ok {
		t.Fatal("redaction changed the params type")
	}
	if dns.URL != "https://pdns:8081" || dns.Type != "powerdns" {
		t.Errorf("redacted params = %+v, want url and type preserved", dns)
	}
	if dns.Key != "" {
		t.Error("key was not emptied")
	}
}

// An op with no secret set must not cost an allocation, and the identical
// slice must come back — the function is called on every changeset read.
func TestRedactOpSecrets_NoSecretIsTheSameSlice(t *testing.T) {
	ops := []change.Op{
		{Params: &change.SdnDnsServerCreateParams{Type: "powerdns", URL: "https://pdns:8081"}},
		{Params: &change.SdnIpamCreateParams{Type: "pve"}},
	}
	got := redactOpSecrets(ops)
	if len(got) != len(ops) || &got[0] != &ops[0] {
		t.Error("redaction copied a slice with nothing to redact")
	}
}

// A changeset using the RETIRED op spelling must be redacted identically
// (T-4114). docs/security.md now states this, and the reason it holds is that
// redactOpSecrets switches on the decoded params type rather than the op
// string — but "the reason it holds" is exactly the kind of claim that stops
// being true when someone adds an op-string switch later, so it is asserted
// here rather than merely documented.
//
// The op arrives as JSON, not as a struct literal, because that is the only
// path on which the old spelling exists at all: change.Op's decoder
// canonicalizes it.
func TestRedactOpSecrets_ADeprecatedOpNameIsRedactedIdentically(t *testing.T) {
	const saved = `{
		"op": "sdn.dns.zone.create",
		"target": "sdn-dns-zone::pdns1",
		"params": {"type": "powerdns", "url": "https://pdns:8081", "key": "` + secretSentinel + `"}
	}`

	var op change.Op
	if err := json.Unmarshal([]byte(saved), &op); err != nil {
		t.Fatalf("a pre-rename changeset no longer decodes: %v", err)
	}

	// Non-vacuity: the secret really is in there before redaction.
	before, err := json.Marshal([]change.Op{op})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(before), secretSentinel) {
		t.Fatal("fixture never carried the secret, so redacting it proves nothing")
	}

	after, err := json.Marshal(redactOpSecrets([]change.Op{op}))
	if err != nil {
		t.Fatalf("marshalling redacted: %v", err)
	}
	if strings.Contains(string(after), secretSentinel) {
		t.Errorf("a changeset using the retired op name echoed its API key: %s", after)
	}
}
