package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/microseg"
	"github.com/bgovanlu/vnprox/internal/store"
)

func writeFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}
}

func openFlowTestDB(t *testing.T) *store.FlowSampleRepo {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewFlowSampleRepo(db)
}

// TestLoadFlowFixtures_TimestampsAndRefs is T-3706's basic contract: daysAgo/
// offsetSec resolve relative to the `now` passed in (not wall-clock time at
// test-run time), guestSide picks which endpoint gets the fixture's guest
// ref, and every seeded row is tagged flow.SourceFixture — never a real
// source name — so it can never be mistaken for a genuine observation in the
// flow explorer (docs/api.md's Flows section).
func TestLoadFlowFixtures_TimestampsAndRefs(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "app01.json", `{
		"guest": "guest:pve1:200",
		"node": "pve1",
		"records": [
			{"daysAgo": 1, "offsetSec": 30, "guestSide": "dst", "srcIp": "10.0.0.10", "dstIp": "10.0.0.5", "srcPort": 40000, "dstPort": 445, "proto": 6, "bytes": 1000, "packets": 1},
			{"daysAgo": 0.5, "offsetSec": 0, "guestSide": "src", "srcIp": "10.0.0.5", "dstIp": "10.0.0.20", "srcPort": 40002, "dstPort": 53, "proto": 17, "bytes": 500, "packets": 2}
		]
	}`)

	repo := openFlowTestDB(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	n, err := loadFlowFixtures(context.Background(), repo, dir, now, nil)
	if err != nil {
		t.Fatalf("loadFlowFixtures: %v", err)
	}
	if n != 2 {
		t.Fatalf("loaded %d records, want 2", n)
	}

	items, _, err := repo.Query(context.Background(), store.FlowFilter{}, "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Query returned %d rows, want 2", len(items))
	}

	byPort := map[int]store.FlowSample{}
	for _, it := range items {
		byPort[it.DstPort] = it
	}

	smb, ok := byPort[445]
	if !ok {
		t.Fatal("no dstPort=445 row")
	}
	wantAt := now.Add(-24*time.Hour).Unix() + 30
	if smb.At != wantAt {
		t.Errorf("smb.At = %d, want %d (now-1d+30s)", smb.At, wantAt)
	}
	if smb.SrcRef != "" || smb.DstRef != "guest:pve1:200" {
		t.Errorf("smb refs = (%q,%q), want (\"\", guest:pve1:200) for guestSide=dst", smb.SrcRef, smb.DstRef)
	}
	if smb.Source != string(flow.SourceFixture) {
		t.Errorf("smb.Source = %q, want %q", smb.Source, flow.SourceFixture)
	}

	dns, ok := byPort[53]
	if !ok {
		t.Fatal("no dstPort=53 row")
	}
	wantAt = now.Add(-12 * time.Hour).Unix()
	if dns.At != wantAt {
		t.Errorf("dns.At = %d, want %d (now-0.5d)", dns.At, wantAt)
	}
	if dns.SrcRef != "guest:pve1:200" || dns.DstRef != "" {
		t.Errorf("dns refs = (%q,%q), want (guest:pve1:200, \"\") for guestSide=src", dns.SrcRef, dns.DstRef)
	}
}

// TestLoadFlowFixtures_RejectsBadGuestSide is the honesty-adjacent guard: a
// fixture author typo ("source"/"dest"/empty) must fail loudly at daemon
// startup rather than silently produce a record with neither ref set, which
// the microseg/baseline adapters would then just never see.
func TestLoadFlowFixtures_RejectsBadGuestSide(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "bad.json", `{
		"guest": "guest:pve1:200",
		"node": "pve1",
		"records": [
			{"daysAgo": 1, "guestSide": "source", "srcIp": "10.0.0.10", "dstIp": "10.0.0.5", "dstPort": 445, "proto": 6, "bytes": 1, "packets": 1}
		]
	}`)

	repo := openFlowTestDB(t)
	if _, err := loadFlowFixtures(context.Background(), repo, dir, time.Now(), nil); err == nil {
		t.Fatal("loadFlowFixtures accepted an invalid guestSide, want an error")
	}
}

// TestFlowFixture_AppFixtureProducesGoldenNASPolicy is a regression guard for
// the actual shipped fixture (testdata/flow-fixtures/app01.json), not a
// synthetic stand-in: it loads the real file through the real loader,
// converts the stored rows back to flow.Record exactly as
// cmd/vnproxd/microsegwire.go's gather() does, and asserts
// internal/microseg.Propose returns the fixture's own golden shape (100%
// coverage, 2 inbound ACCEPT + 1 trailing inbound DROP) in isolation — i.e.
// the JSON fixture web/e2e/microseg.spec.ts's AC4/AC5 depend on is provably
// internally consistent, not merely "shaped right" by eyeball.
func TestFlowFixture_AppFixtureProducesGoldenNASPolicy(t *testing.T) {
	repo := openFlowTestDB(t)
	now := time.Now()

	fixtureDir := filepath.Join("..", "..", "testdata", "flow-fixtures")
	if _, err := loadFlowFixtures(context.Background(), repo, fixtureDir, now, nil); err != nil {
		t.Fatalf("loadFlowFixtures(shipped app01.json): %v", err)
	}

	const guestRef = "guest:pve1:200"
	trainEnd := now.AddDate(0, 0, -2).Unix()
	trainStart := now.AddDate(0, 0, -16).Unix()

	items, _, err := repo.Query(context.Background(), store.FlowFilter{FromTs: trainStart}, "", 5000)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var train []flow.Record
	for _, sm := range items {
		if sm.At > trainEnd {
			continue
		}
		if sm.SrcRef != guestRef && sm.DstRef != guestRef {
			continue
		}
		train = append(train, flowSampleToRecord(sm))
	}
	if len(train) != 24 {
		t.Fatalf("training corpus has %d records for %s in [now-16d,now-2d), want 24 (12 days * 2 inbound flows)", len(train), guestRef)
	}

	subj := microseg.Subject{
		GuestRef:   inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"},
		RulesetRef: microseg.GuestRulesetRef("pve1", "qemu", "200"),
	}
	profile := baseline.Learn(train, guestRef, baseline.Window{Start: trainStart, End: trainEnd})
	// Existing{} — isolated, no live cluster policy — since this test's job
	// is proving the fixture ITSELF is internally consistent (a clean
	// inbound-only corpus proposes a clean inbound-only policy), independent
	// of whatever a particular inventory's cluster firewall does. The e2e
	// spec (web/e2e/microseg.spec.ts) is what proves this same fixture stays
	// self-consistent once actually staged against three-node-vlan.yaml's
	// real (non-empty) existing policy — see its EXPECTED_RULE_COUNT doc
	// comment for why this fixture is deliberately inbound-only.
	prop := microseg.Propose(subj, train, profile, microseg.Existing{}, microseg.DefaultConfig())

	if prop.CoveragePct != 100 {
		t.Errorf("CoveragePct = %.3f, want 100", prop.CoveragePct)
	}
	if len(prop.Rules) != 3 {
		t.Fatalf("rule count = %d, want 3 (2 inbound ACCEPT + 1 trailing inbound DROP — no outbound traffic in this fixture, see its own _comment)", len(prop.Rules))
	}
}
