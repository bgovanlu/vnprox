package peer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeWriteGuard scripts HostWriteGuard: findings to return per call, and a
// record of what was asked — so a provenance-exempt restore can assert
// validation was never consulted, not merely that the write went through.
type fakeWriteGuard struct {
	findings      []string
	known         bool
	validateCalls int
	knownCalls    int
}

func (g *fakeWriteGuard) ValidateStagedContent(_ context.Context, _, _ string) []string {
	g.validateCalls++
	return g.findings
}

func (g *fakeWriteGuard) KnownContent(_ context.Context, _, _ string) bool {
	g.knownCalls++
	return g.known
}

// recordingAuditor captures every HostWriteAudit appended.
type recordingAuditor struct{ rows []HostWriteAudit }

func (a *recordingAuditor) AppendHostWrite(_ context.Context, e HostWriteAudit) {
	a.rows = append(a.rows, e)
}

// newGuardedHarness is newTwoDaemonHarness with T-2902's WriteGuard/
// WriteAudit wired on daemon A, driven through the real Client so the
// attribution context round-trips the actual wire format.
func newGuardedHarness(t *testing.T, guard *fakeWriteGuard) (*Client, Peer, *spyHostWriter, *recordingAuditor) {
	t.Helper()
	writer := newSpyHostWriter()
	auditor := &recordingAuditor{}
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	srv := NewServer(ServerOptions{
		Secrets:    newStaticSecretStore(testSecret),
		Reader:     newSpyHostReader(),
		Writer:     writer,
		WriteGuard: guard,
		WriteAudit: auditor,
		Version:    "test",
		Logger:     discardLogger(),
		Now:        now,
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
		Now:     now,
	})
	return client, Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}, writer, auditor
}

// attributedCtx is the context the coordinating side would carry after
// change.withHostWriteActor + ClusterNodeAgent.withOrigin (T-2902).
func attributedCtx(t *testing.T) context.Context {
	t.Helper()
	return WithAttribution(t.Context(), Attribution{
		Actor: "alice@pam", OriginNode: "pve9", OriginIP: "192.0.2.7",
	})
}

// TestHostWrite_GuardRefusesUnsafeStage is T-2902 AC1's receiving half: a
// stage whose content the receiving node's own validation refuses never
// reaches the host writer, the refusal names the interlock, and the refusal
// is audited with the originating attribution.
func TestHostWrite_GuardRefusesUnsafeStage(t *testing.T) {
	guard := &fakeWriteGuard{findings: []string{"this changeset would remove or re-address 10.0.0.5/24, currently assigned to protected interface bridge:pve1:vmbr0"}}
	client, node, writer, auditor := newGuardedHarness(t, guard)

	err := client.StageInterfaces(attributedCtx(t), node, "pve1", "auto lo\n")
	if err == nil {
		t.Fatal("StageInterfaces with a guard refusal: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "protected interface") {
		t.Errorf("refusal error = %v, want it to name the interlock finding", err)
	}
	if len(writer.staged) != 0 {
		t.Fatalf("host writer was reached despite guard refusal: staged=%v", writer.staged)
	}
	if len(auditor.rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1 (the refusal)", len(auditor.rows))
	}
	row := auditor.rows[0]
	if row.Action != "peer.host.stage" || row.Result != "refused" {
		t.Errorf("audit row = %+v, want action peer.host.stage result refused", row)
	}
	if row.Actor != "alice@pam" || row.OriginNode != "pve9" || row.OriginIP != "192.0.2.7" {
		t.Errorf("audit attribution = %q/%q/%q, want the coordinator's alice@pam/pve9/192.0.2.7", row.Actor, row.OriginNode, row.OriginIP)
	}
	if !strings.Contains(row.Detail, "protected interface") {
		t.Errorf("audit detail = %q, want the finding text", row.Detail)
	}
	if row.ContentSHA256 == "" {
		t.Error("audit ContentSHA256 empty, want the content digest even on refusal")
	}
}

// TestHostWrite_GuardAllowsSafeStage: a clean verdict writes, and the
// success is audited with attribution — one row, "allowed".
func TestHostWrite_GuardAllowsSafeStage(t *testing.T) {
	guard := &fakeWriteGuard{}
	client, node, writer, auditor := newGuardedHarness(t, guard)

	if err := client.StageInterfaces(attributedCtx(t), node, "pve1", "auto lo\n"); err != nil {
		t.Fatalf("StageInterfaces: %v", err)
	}
	if writer.staged["pve1"] != "auto lo\n" {
		t.Fatalf("staged content = %q, want the request content", writer.staged["pve1"])
	}
	if guard.validateCalls != 1 {
		t.Errorf("validate calls = %d, want 1", guard.validateCalls)
	}
	if len(auditor.rows) != 1 || auditor.rows[0].Result != "allowed" || auditor.rows[0].Actor != "alice@pam" {
		t.Fatalf("audit rows = %+v, want one allowed row attributed to alice@pam", auditor.rows)
	}
}

// TestHostWrite_RestoreProvenanceExempt is T-2902 AC2: content matching a
// snapshot this node holds restores WITHOUT consulting content validation
// (a rollback legitimately re-arms the management path), and the audit row
// says why it was allowed through.
func TestHostWrite_RestoreProvenanceExempt(t *testing.T) {
	guard := &fakeWriteGuard{
		known:    true,
		findings: []string{"would refuse if validation ran"}, // must never be consulted
	}
	client, node, writer, auditor := newGuardedHarness(t, guard)

	if err := client.Restore(attributedCtx(t), node, "pve1", "auto vmbr0\n"); err != nil {
		t.Fatalf("Restore of snapshot-known content: %v", err)
	}
	if writer.restored["pve1"] != "auto vmbr0\n" {
		t.Fatalf("restored content = %q, want the request content", writer.restored["pve1"])
	}
	if guard.validateCalls != 0 {
		t.Errorf("validate calls = %d, want 0 — provenance exempts, it does not merely soften", guard.validateCalls)
	}
	if guard.knownCalls != 1 {
		t.Errorf("KnownContent calls = %d, want 1", guard.knownCalls)
	}
	if len(auditor.rows) != 1 || auditor.rows[0].Provenance != "snapshot" || auditor.rows[0].Result != "allowed" {
		t.Fatalf("audit rows = %+v, want one allowed row with provenance snapshot", auditor.rows)
	}
}

// TestHostWrite_RestoreUnknownContentValidated is AC2's flip side: a
// "restore" of content this node never snapshotted is just a write, and a
// guard refusal blocks it.
func TestHostWrite_RestoreUnknownContentValidated(t *testing.T) {
	guard := &fakeWriteGuard{findings: []string{"management path severed"}}
	client, node, writer, auditor := newGuardedHarness(t, guard)

	err := client.Restore(attributedCtx(t), node, "pve1", "auto lo\n")
	if err == nil || !strings.Contains(err.Error(), "management path severed") {
		t.Fatalf("Restore of unknown unsafe content: err = %v, want the finding", err)
	}
	if len(writer.restored) != 0 {
		t.Fatalf("host writer reached despite refusal: %v", writer.restored)
	}
	if len(auditor.rows) != 1 || auditor.rows[0].Result != "refused" || auditor.rows[0].Action != "peer.host.restore" {
		t.Fatalf("audit rows = %+v, want one refused peer.host.restore row", auditor.rows)
	}
}

// TestHostWrite_ContentFreeActionsAudited is AC3 for the content-free
// writes: ifreload and discard-staged each append exactly one attributed
// row with no content digest.
func TestHostWrite_ContentFreeActionsAudited(t *testing.T) {
	guard := &fakeWriteGuard{}
	client, node, _, auditor := newGuardedHarness(t, guard)
	ctx := attributedCtx(t)

	if err := client.Ifreload(ctx, node, "pve1"); err != nil {
		t.Fatalf("Ifreload: %v", err)
	}
	if err := client.DiscardStaged(ctx, node, "pve1"); err != nil {
		t.Fatalf("DiscardStaged: %v", err)
	}
	if len(auditor.rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(auditor.rows))
	}
	for i, want := range []string{"peer.host.ifreload", "peer.host.discard-staged"} {
		row := auditor.rows[i]
		if row.Action != want || row.Result != "allowed" || row.Actor != "alice@pam" || row.ContentSHA256 != "" {
			t.Errorf("row %d = %+v, want allowed %s attributed to alice@pam with no digest", i, row, want)
		}
	}
}

// TestHostWrite_NoAttributionRecordedAsAbsent: a pre-T-2902 coordinator
// sends no attribution; the receiving side records the absence rather than
// inventing values or refusing the request.
func TestHostWrite_NoAttributionRecordedAsAbsent(t *testing.T) {
	guard := &fakeWriteGuard{}
	client, node, _, auditor := newGuardedHarness(t, guard)

	if err := client.StageInterfaces(t.Context(), node, "pve1", "auto lo\n"); err != nil {
		t.Fatalf("StageInterfaces without attribution: %v", err)
	}
	if len(auditor.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(auditor.rows))
	}
	if row := auditor.rows[0]; row.Actor != "" || row.OriginNode != "" || row.OriginIP != "" {
		t.Errorf("attribution = %+v, want all empty (recorded absence)", row)
	}
}
