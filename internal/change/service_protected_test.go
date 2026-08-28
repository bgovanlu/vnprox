// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// newSafetyTestService builds a Service wired for T-203's safety-interlock
// tests: a real store-backed audit repo (so auditSafetyOverride's entries
// are observable), a protectedPath under the test's own temp dir, and an
// injected snapshot via fakeInventorySource. allowDangerous mirrors
// config.Config.Safety.AllowDangerousOps.
func newSafetyTestService(t *testing.T, snap inventory.Snapshot, protectedPath string, allowDangerous bool) (*Service, *store.AuditRepo) {
	t.Helper()
	db := openTestDB(t)
	audit := store.NewAuditRepo(db)
	svc, err := NewService(Config{
		Changesets:        store.NewChangesetRepo(db),
		Audit:             audit,
		Inventory:         fakeInventorySource{snap: snap},
		Now:               func() time.Time { return time.Unix(1_700_000_000, 0) },
		ProtectedPath:     protectedPath,
		AllowDangerousOps: allowDangerous,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, audit
}

func writeProtectedFixture(t *testing.T, path string, nodes map[string][]string) {
	t.Helper()
	if err := SaveProtectedConfig(path, ProtectedConfig{Nodes: nodes}); err != nil {
		t.Fatalf("SaveProtectedConfig: %v", err)
	}
}

func TestService_Create_ProtectedInterfaceBlocksValidation(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	writeProtectedFixture(t, protectedPath, map[string][]string{"pve1": {vmbr0.String()}})

	snap := buildSnapshot(
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)
	svc, _ := newSafetyTestService(t, snap, protectedPath, false)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "delete mgmt bridge", []Op{{
		Type: OpBridgeDelete, Target: vmbr0, Params: &BridgeDeleteParams{},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var found bool
	for _, f := range c.Findings {
		if f.Code == codeProtectedInterface {
			found = true
			if f.Severity != SeverityError {
				t.Errorf("severity = %s, want error (allow_dangerous_ops=false)", f.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("Findings = %+v, want a safety.protected_interface finding", c.Findings)
	}

	// Validate must refuse to promote the changeset while the interlock
	// error stands.
	got, err := svc.Validate(ctx, c.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.Status != StatusDraft {
		t.Errorf("Status = %s, want draft (promotion blocked by the safety-interlock error)", got.Status)
	}
}

// TestService_AllowDangerousOps_DowngradesAndAudits is T-203 acceptance
// criterion 4: with allow_dangerous_ops=true the same protected-interface
// case is a warning (so the changeset can still be validated/applied) and
// an audit entry records that the override was exercised.
func TestService_AllowDangerousOps_DowngradesAndAudits(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	writeProtectedFixture(t, protectedPath, map[string][]string{"pve1": {vmbr0.String()}})

	snap := buildSnapshot(
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)
	svc, audit := newSafetyTestService(t, snap, protectedPath, true)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "delete mgmt bridge", []Op{{
		Type: OpBridgeDelete, Target: vmbr0, Params: &BridgeDeleteParams{},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var found bool
	for _, f := range c.Findings {
		if f.Code == codeProtectedInterface {
			found = true
			if f.Severity != SeverityWarning {
				t.Errorf("severity = %s, want warning (allow_dangerous_ops=true)", f.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("Findings = %+v, want a downgraded safety.protected_interface finding", c.Findings)
	}

	// A clean (no error) changeset should promote fine via Validate.
	got, err := svc.Validate(ctx, c.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.Status != StatusValidated {
		t.Errorf("Status = %s, want validated (only a warning stands, allow_dangerous_ops=true)", got.Status)
	}

	entries, err := audit.List(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var overrideEntries int
	for _, e := range entries {
		if e.Action == "changeset.safety_override" {
			overrideEntries++
			if e.Username != "alice@pam" {
				t.Errorf("override audit entry = %+v, want username=alice@pam", e)
			}
		}
	}
	if overrideEntries == 0 {
		t.Errorf("audit entries = %+v, want at least one changeset.safety_override entry", entries)
	}
}

func TestService_AllowDangerousOps_False_NoOverrideAudited(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	svc, audit := newSafetyTestService(t, buildSnapshot(), protectedPath, false)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries, err := audit.List(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	for _, e := range entries {
		if e.Action == "changeset.safety_override" {
			t.Errorf("unexpected changeset.safety_override entry with allow_dangerous_ops=false: %+v", e)
		}
	}
}

// --- GetProtected / SetProtected ------------------------------------------

func TestService_GetProtected_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	svc, _ := newSafetyTestService(t, buildSnapshot(), filepath.Join(dir, "protected.json"), false)
	cfg, err := svc.GetProtected(context.Background())
	if err != nil {
		t.Fatalf("GetProtected: %v", err)
	}
	if len(cfg.Nodes) != 0 {
		t.Errorf("Nodes = %v, want empty", cfg.Nodes)
	}
}

func TestService_SetProtected_PersistsAndAudits(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	svc, audit := newSafetyTestService(t, buildSnapshot(), protectedPath, false)
	ctx := context.Background()

	in := ProtectedConfig{Nodes: map[string][]string{"pve1": {"bridge:pve1:vmbr0"}}}
	out, err := svc.SetProtected(ctx, "root@pam", in)
	if err != nil {
		t.Fatalf("SetProtected: %v", err)
	}
	if out.UpdatedBy != "root@pam" || out.Version != protectedConfigVersion {
		t.Errorf("SetProtected result = %+v, want UpdatedBy=root@pam Version=%d", out, protectedConfigVersion)
	}

	got, err := svc.GetProtected(ctx)
	if err != nil {
		t.Fatalf("GetProtected: %v", err)
	}
	if len(got.Nodes["pve1"]) != 1 || got.Nodes["pve1"][0] != "bridge:pve1:vmbr0" {
		t.Errorf("GetProtected = %+v, want the just-saved config", got)
	}

	entries, err := audit.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var updateEntries int
	for _, e := range entries {
		if e.Action == "protected.update" {
			updateEntries++
		}
	}
	if updateEntries != 1 {
		t.Errorf("protected.update audit entries = %d, want 1: %+v", updateEntries, entries)
	}
}

func TestService_SetProtected_InvalidRef(t *testing.T) {
	dir := t.TempDir()
	svc, _ := newSafetyTestService(t, buildSnapshot(), filepath.Join(dir, "protected.json"), false)

	_, err := svc.SetProtected(context.Background(), "root@pam", ProtectedConfig{
		Nodes: map[string][]string{"pve1": {"not-a-ref"}},
	})
	var invalidRef *ErrInvalidProtectedRef
	if !errors.As(err, &invalidRef) {
		t.Fatalf("SetProtected error = %v, want *ErrInvalidProtectedRef", err)
	}
}

// TestService_SafetyOptions_ReadsFreshEachValidation proves protected.json
// is not cached at Service construction: a correction saved via
// SetProtected after Create takes effect on the very next Validate call
// without recreating the Service.
func TestService_SafetyOptions_ReadsFreshEachValidation(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	snap := buildSnapshot(
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)
	// Start with nothing protected.
	svc, _ := newSafetyTestService(t, snap, protectedPath, false)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "delete bridge", []Op{{
		Type: OpBridgeDelete, Target: vmbr0, Params: &BridgeDeleteParams{},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, f := range c.Findings {
		if f.Code == codeProtectedInterface {
			t.Fatalf("unexpected protected_interface finding before onboarding confirms anything: %+v", c.Findings)
		}
	}

	// Confirm vmbr0 as protected via the same Service instance.
	if _, setErr := svc.SetProtected(ctx, "root@pam", ProtectedConfig{
		Nodes: map[string][]string{"pve1": {vmbr0.String()}},
	}); setErr != nil {
		t.Fatalf("SetProtected: %v", setErr)
	}

	got, err := svc.Validate(ctx, c.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var found bool
	for _, f := range got.Findings {
		if f.Code == codeProtectedInterface {
			found = true
		}
	}
	if !found {
		t.Errorf("Findings = %+v, want a protected_interface finding after the onboarding confirmation was saved", got.Findings)
	}
}
