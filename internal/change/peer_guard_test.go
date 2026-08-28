// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestValidatePeerHostWrite_Parity is T-2902 AC1: the receiving-side guard
// and the coordinating change engine give the same verdict to the same
// content, in one table — a peer host write is refused under exactly the
// conditions a local raw-editor changeset is, no stricter and no looser.
func TestValidatePeerHostWrite_Parity(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	writeProtectedFixture(t, protectedPath, map[string][]string{"pve1": {vmbr0.String()}})

	snap := buildSnapshot(
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)
	svc, _ := newSafetyTestService(t, snap, protectedPath, false)
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": rawBaseFile}}

	tests := []struct {
		name       string
		content    string
		wantRefuse bool
	}{
		{
			name:       "content deleting the protected management bridge is refused",
			content:    "auto lo\niface lo inet loopback\n",
			wantRefuse: true,
		},
		{
			name:       "content preserving the management bridge is allowed",
			content:    rawBaseFile,
			wantRefuse: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// The receiving-side guard's verdict.
			findings := svc.ValidatePeerHostWrite(ctx, "pve1", tt.content)

			// The coordinating engine's verdict on the identical content,
			// through the ordinary raw-op pipeline.
			c, err := svc.Create(ctx, "alice@pam", "parity probe", []Op{{
				Type:   OpIfaceRawReplace,
				Target: rawNodeTarget("pve1"),
				Params: &IfaceRawReplaceParams{Content: tt.content},
			}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var localBlocking []string
			for _, f := range c.Findings {
				if f.Severity == SeverityError {
					localBlocking = append(localBlocking, f.Message)
				}
			}

			if (len(findings) > 0) != tt.wantRefuse {
				t.Errorf("guard verdict = %v (findings %v), want refuse=%v", len(findings) > 0, findings, tt.wantRefuse)
			}
			if (len(findings) > 0) != (len(localBlocking) > 0) {
				t.Fatalf("PARITY BROKEN: guard findings %v vs local blocking findings %v — the two paths disagree", findings, localBlocking)
			}
			if tt.wantRefuse {
				joined := strings.Join(findings, "; ")
				if !strings.Contains(joined, "protected interface") {
					t.Errorf("guard findings = %q, want the protected-interface interlock named", joined)
				}
			}
		})
	}
}

// TestAppendAudit_CarriesClientIP is T-2902 AC5's engine half: a mutation
// whose request context carries the API layer's client IP (internal/api's
// auditIPMiddleware) lands in audit_log with that IP in the first-class
// column; a context without one records ”.
func TestAppendAudit_CarriesClientIP(t *testing.T) {
	svc := newTestService(t, nil)

	ctx := store.WithAuditClientIP(context.Background(), "192.0.2.9")
	c, err := svc.Create(ctx, "alice@pam", "with ip", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c2, err := svc.Create(context.Background(), "alice@pam", "without ip", sampleOps())
	if err != nil {
		t.Fatalf("Create (no ip): %v", err)
	}

	rows, err := svc.audit.List(context.Background(), c.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no audit rows for the changeset.create")
	}
	if rows[0].IP != "192.0.2.9" {
		t.Errorf("audit ip = %q, want 192.0.2.9", rows[0].IP)
	}

	rows2, err := svc.audit.List(context.Background(), c2.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if len(rows2) == 0 || rows2[0].IP != "" {
		t.Errorf("audit ip without middleware = %+v, want ''", rows2)
	}
}
