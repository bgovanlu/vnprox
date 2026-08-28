// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"context"
	"encoding/json"
	"testing"
)

// TestFixtureHostReader_NftRuleset_DefaultsToEmptyRealShape covers T-3904's
// most common case, mirroring TestFixtureHostReader_MDB's precedent: a node
// with no `nft_ruleset:` declared must render the same metainfo-only,
// no-tables document a real disabled-firewall node produces (evidence
// file §2), not an empty byte slice.
func TestFixtureHostReader_NftRuleset_DefaultsToEmptyRealShape(t *testing.T) {
	f := &Fixture{Nodes: map[string]*NodeSpec{"n1": {}}}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)

	raw, err := reader.NftRuleset(context.Background(), "n1")
	if err != nil {
		t.Fatalf("NftRuleset: %v", err)
	}
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("NftRuleset did not produce valid JSON: %v (%s)", err, raw)
	}
	if len(doc.Nftables) != 1 {
		t.Fatalf("expected exactly 1 top-level object (metainfo only), got %d: %s", len(doc.Nftables), raw)
	}
	if _, ok := doc.Nftables[0]["metainfo"]; !ok {
		t.Errorf("expected a metainfo object, got %s", raw)
	}
	if _, ok := doc.Nftables[0]["table"]; ok {
		t.Errorf("a node with no declared nft_ruleset must have no tables, got %s", raw)
	}
}

// TestFixtureHostReader_NftRuleset_DeclaredPassesThroughVerbatim proves a
// fixture-declared ruleset is returned byte-for-byte, unlike MDB/BGP's
// synthesized renders — see NodeSpec.NftRuleset's doc comment for why.
func TestFixtureHostReader_NftRuleset_DeclaredPassesThroughVerbatim(t *testing.T) {
	const declared = `{"nftables": [{"table": {"family": "inet", "name": "proxmox-firewall"}}]}`
	f := &Fixture{Nodes: map[string]*NodeSpec{"n1": {NftRuleset: declared}}}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)

	raw, err := reader.NftRuleset(context.Background(), "n1")
	if err != nil {
		t.Fatalf("NftRuleset: %v", err)
	}
	if string(raw) != declared {
		t.Errorf("NftRuleset = %s, want verbatim %s", raw, declared)
	}
}

func TestFixtureHostReader_NftRuleset_UnknownNode(t *testing.T) {
	srv := NewServer(&Fixture{Nodes: map[string]*NodeSpec{}})
	reader := NewFixtureHostReader(srv)
	if _, err := reader.NftRuleset(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for an unknown node")
	}
}
