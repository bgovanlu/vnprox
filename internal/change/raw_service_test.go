// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// stubRawNodeAgent is a minimal NodeAgent test double for the raw-editor
// Service methods, which only ever call ReadInterfaces (validate/read-raw);
// Stage/Reload/Discard are unused stubs.
type stubRawNodeAgent struct {
	files map[string]string
}

func (a *stubRawNodeAgent) ReadInterfaces(_ context.Context, node string) (string, error) {
	c, ok := a.files[node]
	if !ok {
		return "", fmt.Errorf("stubRawNodeAgent: no file seeded for node %s", node)
	}
	return c, nil
}
func (a *stubRawNodeAgent) StageInterfaces(context.Context, string, string) error { return nil }
func (a *stubRawNodeAgent) ReloadInterfaces(context.Context, string) error        { return nil }
func (a *stubRawNodeAgent) DiscardStaged(context.Context, string) error           { return nil }

func TestService_ReadRawInterfaces(t *testing.T) {
	svc := newTestService(t, nil)
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": rawBaseFile}}

	content, hash, err := svc.ReadRawInterfaces(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("ReadRawInterfaces: %v", err)
	}
	if content != rawBaseFile {
		t.Errorf("content = %q, want the seeded file", content)
	}
	if hash != rawFileHash(rawBaseFile) {
		t.Errorf("hash = %q, want %q", hash, rawFileHash(rawBaseFile))
	}
}

func TestService_ReadRawInterfaces_NoNodeAgent(t *testing.T) {
	svc := newTestService(t, nil)
	if _, _, err := svc.ReadRawInterfaces(context.Background(), "pve1"); err == nil {
		t.Fatal("expected an error with no NodeAgent configured")
	}
}

// TestService_Create_RawReplace_DeletesManagementBridge_BlockedBySafety is
// this task's AC2: saving a raw file that deletes the management bridge
// must surface T-203's safety interlock, exactly as if the user had used
// the bridge-delete dialog — because expandRawReplaceOps synthesizes the
// equivalent bridge.delete op and feeds it through the same
// ValidateWithSafety pipeline.
func TestService_Create_RawReplace_DeletesManagementBridge_BlockedBySafety(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	writeProtectedFixture(t, protectedPath, map[string][]string{"pve1": {vmbr0.String()}})

	snap := buildSnapshot(
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)
	svc, _ := newSafetyTestService(t, snap, protectedPath, false)

	before := rawBaseFile // declares vmbr0 with address 10.10.0.1/24
	after := "auto lo\niface lo inet loopback\n"
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": before}}

	ctx := context.Background()
	c, err := svc.Create(ctx, "alice@pam", "raw edit deletes mgmt bridge", []Op{{
		Type:   OpIfaceRawReplace,
		Target: rawNodeTarget("pve1"),
		Params: &IfaceRawReplaceParams{Content: after, BaseHash: rawFileHash(before)},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Status != StatusDraft {
		t.Errorf("Status = %q, want draft (blocking finding must not promote it)", c.Status)
	}
	var found bool
	for _, f := range c.Findings {
		if f.Code == codeProtectedInterface && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a safety.protected_interface error finding, got %+v", c.Findings)
	}

	// The explicit Validate call must agree (it re-runs the same pipeline)
	// and must refuse to promote the changeset to validated.
	validated, err := svc.Validate(ctx, c.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != StatusDraft {
		t.Errorf("Validate: Status = %q, want draft", validated.Status)
	}
}

func TestService_Create_RawReplace_HashConflict(t *testing.T) {
	svc := newTestService(t, nil)
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": rawBaseFile}}

	// Editor opened against a stale hash — someone else changed the live
	// file (or this is simply a wrong/garbage hash) since then.
	c, err := svc.Create(context.Background(), "alice@pam", "stale raw edit", []Op{{
		Type:   OpIfaceRawReplace,
		Target: rawNodeTarget("pve1"),
		Params: &IfaceRawReplaceParams{Content: "auto lo\niface lo inet loopback\n", BaseHash: "0000stalehash0000"},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var found bool
	for _, f := range c.Findings {
		if f.Code == codeRawReplaceHashConflict && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a raw.hash_conflict finding, got %+v", c.Findings)
	}
}

func TestService_Create_RawReplace_MatchingHashNoConflict(t *testing.T) {
	svc := newTestService(t, nil)
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": rawBaseFile}}

	c, err := svc.Create(context.Background(), "alice@pam", "matching hash raw edit", []Op{{
		Type:   OpIfaceRawReplace,
		Target: rawNodeTarget("pve1"),
		Params: &IfaceRawReplaceParams{Content: rawBaseFile, BaseHash: rawFileHash(rawBaseFile)},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, f := range c.Findings {
		if f.Code == codeRawReplaceHashConflict {
			t.Errorf("unexpected hash-conflict finding for a matching hash: %+v", f)
		}
	}
}

func TestService_Create_RawReplace_ParseErrorFinding(t *testing.T) {
	svc := newTestService(t, nil)
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": rawBaseFile}}

	c, err := svc.Create(context.Background(), "alice@pam", "broken raw edit", []Op{{
		Type:   OpIfaceRawReplace,
		Target: rawNodeTarget("pve1"),
		Params: &IfaceRawReplaceParams{Content: "iface eth0 inet static\n\taddress 10.0.0.1\\", BaseHash: rawFileHash(rawBaseFile)},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var found bool
	for _, f := range c.Findings {
		if f.Code == codeRawReplaceParseError && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a raw.parse_error finding, got %+v", c.Findings)
	}
}

func TestService_Create_RawReplace_CombinedWithOtherOpSameNode_Rejected(t *testing.T) {
	svc := newTestService(t, nil)
	svc.nodes = &stubRawNodeAgent{files: map[string]string{"pve1": rawBaseFile}}

	c, err := svc.Create(context.Background(), "alice@pam", "raw plus another op", []Op{
		{Type: OpIfaceRawReplace, Target: rawNodeTarget("pve1"), Params: &IfaceRawReplaceParams{Content: rawBaseFile, BaseHash: rawFileHash(rawBaseFile)}},
		{Type: OpBridgeDelete, Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Params: &BridgeDeleteParams{}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var found bool
	for _, f := range c.Findings {
		if f.Code == codeRawReplaceNotExclusive {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a raw.not_exclusive_with_other_ops finding, got %+v", c.Findings)
	}
}

func TestLintRawInterfaces_OK(t *testing.T) {
	if markers := LintRawInterfaces(rawBaseFile); len(markers) != 0 {
		t.Errorf("markers = %+v, want none for valid content", markers)
	}
}

func TestLintRawInterfaces_ParseError(t *testing.T) {
	markers := LintRawInterfaces("iface eth0 inet static\n\taddress 10.0.0.1\\")
	if len(markers) != 1 {
		t.Fatalf("markers = %+v, want exactly one", markers)
	}
	if markers[0].Line != 2 {
		t.Errorf("Line = %d, want 2 (the unterminated continuation line)", markers[0].Line)
	}
	if markers[0].Message == "" {
		t.Errorf("Message is empty")
	}
}
