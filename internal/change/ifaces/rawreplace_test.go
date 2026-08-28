// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// nodeRef builds the inventory.KindNode ref IfaceRawReplace targets (the
// whole node's file, not a single iface-namespace entity).
func nodeRef(node string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindNode, Node: node, ID: node}
}

// TestMutate_IfaceRawReplace is T-208's core AST-swap contract: Mutate does
// not edit f in place like every other op — it discards f's current
// Entries wholesale and re-parses Content, so Render() afterward reproduces
// Content byte-for-byte (host.File's lossless-render guarantee applied to
// an unmutated parse of the new content).
func TestMutate_IfaceRawReplace(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	newContent, err := os.ReadFile("../../../testdata/interfaces/03-bond-with-vlans.interfaces")
	if err != nil {
		t.Fatalf("reading replacement corpus fixture: %v", err)
	}

	op := IfaceRawReplace{Target: nodeRef("pve1"), Content: string(newContent)}
	if err := Mutate(f, op, "TESTCS01"); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if got := f.Render(); got != string(newContent) {
		t.Errorf("Render() after iface.raw.replace = %q, want %q (byte-identical to Content)", got, string(newContent))
	}
}

// TestMutate_IfaceRawReplace_ParseError checks that malformed replacement
// content surfaces as an error from Mutate (rather than silently emptying
// the file), and that f is left untouched on that error path.
func TestMutate_IfaceRawReplace_ParseError(t *testing.T) {
	f, origText := parseCorpus(t, "01-simple-single-bridge.interfaces")
	before := append([]host.Entry(nil), f.Entries...)

	op := IfaceRawReplace{Target: nodeRef("pve1"), Content: "iface eth0 inet static\n\taddress 10.0.0.1\\"}
	if err := Mutate(f, op, "TESTCS01"); err == nil {
		t.Fatal("Mutate: expected a parse error for malformed replacement content, got nil")
	}
	if !entriesEqual(before, f.Entries) {
		t.Errorf("Mutate must leave f untouched on a parse error; original text was:\n%s", origText)
	}
}

// TestDecodeOp_IfaceRawReplace exercises the {op,target,params} wire
// envelope round trip for the new op type.
func TestDecodeOp_IfaceRawReplace(t *testing.T) {
	raw := []byte(`{"op":"iface.raw.replace","target":"node:pve1:pve1","params":{"content":"auto lo\niface lo inet loopback\n"}}`)
	op, err := DecodeOp(raw)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	rr, ok := op.(IfaceRawReplace)
	if !ok {
		t.Fatalf("DecodeOp returned %T, want IfaceRawReplace", op)
	}
	if rr.Target != nodeRef("pve1") {
		t.Errorf("Target = %+v, want %+v", rr.Target, nodeRef("pve1"))
	}
	if rr.Content != "auto lo\niface lo inet loopback\n" {
		t.Errorf("Content = %q", rr.Content)
	}
	if rr.Kind() != OpIfaceRawReplace {
		t.Errorf("Kind() = %q, want %q", rr.Kind(), OpIfaceRawReplace)
	}
	if rr.Ref() != rr.Target {
		t.Errorf("Ref() = %+v, want %+v", rr.Ref(), rr.Target)
	}
}

// TestSummarize_IfaceRawReplace checks the review screen's op card text.
func TestSummarize_IfaceRawReplace(t *testing.T) {
	op := IfaceRawReplace{Target: nodeRef("pve1"), Content: "auto lo\n"}
	sum := Summarize(op)
	if sum.Op != "iface.raw.replace" || sum.Node != "pve1" {
		t.Errorf("Summarize = %+v", sum)
	}
	if !strings.Contains(sum.Summary, "pve1") || !strings.Contains(sum.Summary, "raw edit") {
		t.Errorf("Summary = %q, want it to mention the node and a raw edit", sum.Summary)
	}
}

// TestDiffChangeset_IfaceRawReplace is this task's AC "differ shows
// full-file diff": DiffChangeset's generic parse->MutateAll->Render->
// UnifiedDiff pipeline needs no special-casing for iface.raw.replace since
// Mutate already produces Content as the rendered result.
func TestDiffChangeset_IfaceRawReplace(t *testing.T) {
	reader := loadThreeNodeVlanReader(t)
	ctx := context.Background()

	newContent := "auto lo\niface lo inet loopback\n\nauto vmbr9\niface vmbr9 inet manual\n\tbridge-ports none\n"
	ops := []Op{IfaceRawReplace{Target: nodeRef("pve1"), Content: newContent}}

	diff, err := DiffChangeset(ctx, reader, ops, "CS-RAW")
	if err != nil {
		t.Fatalf("DiffChangeset: %v", err)
	}
	if len(diff.Files) != 1 {
		t.Fatalf("len(diff.Files) = %d, want 1", len(diff.Files))
	}
	fd := diff.Files[0]
	if fd.Node != "pve1" || fd.Path != "/etc/network/interfaces" {
		t.Errorf("FileDiff = %+v", fd)
	}
	if !fd.Changed || fd.Unified == "" {
		t.Errorf("expected a non-empty changed diff, got %+v", fd)
	}
	if !strings.Contains(fd.Unified, "+auto vmbr9") {
		t.Errorf("diff missing new vmbr9 stanza:\n%s", fd.Unified)
	}
	if len(diff.Ops) != 1 || diff.Ops[0].Op != "iface.raw.replace" {
		t.Errorf("diff.Ops = %+v", diff.Ops)
	}
}
