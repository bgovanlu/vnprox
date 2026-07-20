package change

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeMembership is a static node->cluster resolver for the cross-cluster
// scoping wiring test.
type fakeMembership struct {
	m   map[string]string
	err error
}

func (f fakeMembership) NodeClusters(context.Context) (map[string]string, error) {
	return f.m, f.err
}

// TestService_Create_RejectsCrossClusterOp proves the scoping check is wired
// through Create end-to-end (not merely the pure ValidateClusterScope
// function): a Service scoped to cluster "east" flags a bridge op targeting
// a node the membership resolver places in cluster "west", and the persisted
// changeset carries both its ClusterID and the blocking finding.
func TestService_Create_RejectsCrossClusterOp(t *testing.T) {
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:        store.NewChangesetRepo(db),
		Audit:             store.NewAuditRepo(db),
		Now:               func() time.Time { return time.Unix(1_700_000_000, 0) },
		LocalClusterID:    "east",
		ClusterMembership: fakeMembership{m: map[string]string{"pve1": "east", "pve9": "west"}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	ops := []Op{{
		Type:   OpBridgeUpdate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve9", ID: "vmbr0"},
		Params: &BridgeUpdateParams{},
	}}
	c, err := svc.Create(ctx, "alice@pam", "cross-cluster attempt", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ClusterID != "east" {
		t.Errorf("ClusterID = %q, want east", c.ClusterID)
	}
	if !hasFindingCode(c.Findings, codeCrossClusterRef) {
		t.Fatalf("Findings = %+v, want a %s error", c.Findings, codeCrossClusterRef)
	}
	if !hasError(c.Findings) {
		t.Errorf("expected the cross-cluster finding to be error-severity (blocking)")
	}

	// It persists (ClusterID + finding survive a reload), and Validate keeps
	// it in draft (a blocking error never promotes to validated).
	got, err := svc.Validate(ctx, c.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.ClusterID != "east" {
		t.Errorf("reloaded ClusterID = %q, want east", got.ClusterID)
	}
	if got.Status != StatusDraft {
		t.Errorf("Status = %s, want draft (a cross-cluster op must not validate)", got.Status)
	}

	// A same-cluster op on the identical Service validates cleanly.
	clean, err := svc.Create(ctx, "alice@pam", "same-cluster", []Op{{
		Type:   OpBridgeUpdate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
		Params: &BridgeUpdateParams{},
	}})
	if err != nil {
		t.Fatalf("Create (same-cluster): %v", err)
	}
	if hasFindingCode(clean.Findings, codeCrossClusterRef) {
		t.Errorf("same-cluster op unexpectedly flagged cross-cluster: %+v", clean.Findings)
	}
}

func hasFindingCode(findings []Finding, code string) bool {
	for _, f := range findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
