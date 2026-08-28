// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// This file extends the golden matrix beyond fixtures 01-04 (audit finding
// F-07): OVS bond.update gets byte-level coverage, bridge.delete and
// bridge.port.remove get Linux-form goldens, and each hostile fixture —
// 08 (exotic comments), 09 (dual-stack), 14 (CRLF, no trailing newline),
// 15 (messy brownfield) — receives at least one write-op golden. It also
// carries F-06's CRLF regression tests: mutating fixture 14 must keep the
// file consistently CRLF, and the rendered unified diff must be accepted
// by GNU patch.

// TestGolden_BondUpdate_OVS covers the OVS branch of mutateBondUpdate
// (bond.go), previously without any byte-level golden.
func TestGolden_BondUpdate_OVS(t *testing.T) {
	f, _ := parseCorpus(t, "05-ovs-bond.interfaces")
	op := BondUpdate{
		Target: ref(inventory.KindOVSBond, "pve1", "bond0"),
		Slaves: []string{"eno1", "eno3"}, MTU: 9000,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bond-update-ovs-05.interfaces", f.Render())
}

// TestGolden_BridgeDelete_Linux covers Linux-form bridge.delete
// (previously golden-tested OVS-only): the auto line and both stanza and
// body vanish while the dependent vmbr0.20 VLAN stanza is left untouched.
func TestGolden_BridgeDelete_Linux(t *testing.T) {
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	op := BridgeDelete{Target: ref(inventory.KindBridge, "pve1", "vmbr0")}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bridge-delete-02.interfaces", f.Render())
}

// TestGolden_BridgePortRemove_Linux covers Linux-form bridge.port.remove
// (previously golden-tested OVS-only). Removing vmbr1's only port drops the
// bridge-ports option line entirely (setOption's empty-value path — pinned
// behavior, also asserted in TestBridgePortRemove_LastPortClearsOption).
func TestGolden_BridgePortRemove_Linux(t *testing.T) {
	f, _ := parseCorpus(t, "03-bond-with-vlans.interfaces")
	op := BridgePortRemove{Target: ref(inventory.KindBridge, "pve1", "vmbr1"), Port: "bond0.10"}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bridge-port-remove-03.interfaces", f.Render())
}

// TestGolden_BridgeUpdate_ExoticComments mutates fixture 08 — the corpus's
// "comments preserved" stress case (banner blocks, inter-option comments,
// comments with no leading space, a trailing top-level comment) — and
// asserts every comment byte survives an in-place bridge.update.
func TestGolden_BridgeUpdate_ExoticComments(t *testing.T) {
	f, raw := parseCorpus(t, "08-exotic-comments.interfaces")
	op := BridgeUpdate{
		Target: ref(inventory.KindBridge, "pve1", "vmbr0"),
		Ports:  []string{"eno1", "eno2"}, MTU: 1500,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	out := f.Render()
	for _, comment := range []string{
		"#!/this/is/not/a/shebang/but/looks/like/one -- just a comment",
		"# NETWORK CONFIG — DO NOT EDIT BY HAND",
		"# eno1 is the onboard NIC; eno2 is the add-in card (unused for now)",
		"\t# comments can appear between options too",
		"\t#no leading space either",
		"\t#management bridge",
		"\t#do not remove eno1 from this bridge",
		"# trailing top-level comment at end of file, no blank line after it",
	} {
		if !strings.Contains(raw, comment) {
			t.Fatalf("fixture drift: comment %q no longer in 08-exotic-comments.interfaces", comment)
		}
		if !strings.Contains(out, comment) {
			t.Errorf("comment not preserved through bridge.update: %q", comment)
		}
	}
	checkGolden(t, "bridge-update-08.interfaces", out)
}

// TestGolden_BridgePortAdd_DualStack mutates fixture 09: vmbr0 has one
// stanza per address family, and only the first (inet, the one carrying
// bridge-ports) may change.
func TestGolden_BridgePortAdd_DualStack(t *testing.T) {
	f, _ := parseCorpus(t, "09-dual-stack-multi-stanza.interfaces")
	op := BridgePortAdd{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Port: "eno2"}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	out := f.Render()
	if !strings.Contains(out, "iface vmbr0 inet6 static\n\taddress fd00:1::10/64") {
		t.Errorf("inet6 stanza must be untouched by bridge.port.add:\n%s", out)
	}
	checkGolden(t, "bridge-port-add-09.interfaces", out)
}

// TestGolden_BridgeUpdate_Brownfield mutates fixture 15 (mixed tab/space
// indentation, trailing whitespace, a stray blank line inside the stanza):
// only the inserted mtu line may differ; every messy original byte stays.
func TestGolden_BridgeUpdate_Brownfield(t *testing.T) {
	f, _ := parseCorpus(t, "15-messy-brownfield-style.interfaces")
	op := BridgeUpdate{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), MTU: 9000}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	out := f.Render()
	for _, messy := range []string{
		"iface vmbr0 inet static \n", // trailing space on the header line
		"\taddress 10.20.30.5/24   \n",
		"        gateway 10.20.30.1\n", // space-indented option
	} {
		if !strings.Contains(out, messy) {
			t.Errorf("messy original bytes not preserved: %q missing in:\n%s", messy, out)
		}
	}
	checkGolden(t, "bridge-update-15.interfaces", out)
}

// requireConsistentCRLF asserts every "\n" in content is part of a "\r\n"
// pair — i.e. the file has no stray LF-only line endings (F-06).
func requireConsistentCRLF(t *testing.T, content string) {
	t.Helper()
	if lf, crlf := strings.Count(content, "\n"), strings.Count(content, "\r\n"); lf != crlf {
		t.Errorf("mixed line endings: %d LF vs %d CRLF in:\n%q", lf, crlf, content)
	}
}

// TestGolden_BondCreate_CRLF is F-06's append-path regression: a stanza
// appended to the CRLF, no-trailing-newline fixture must be rendered with
// CRLF terminators throughout (before the fix, appended lines were LF and
// the result had mixed endings).
func TestGolden_BondCreate_CRLF(t *testing.T) {
	orig, _ := parseCorpus(t, "14-crlf-no-trailing-newline.interfaces")
	f, _ := parseCorpus(t, "14-crlf-no-trailing-newline.interfaces")
	op := BondCreate{
		Target: ref(inventory.KindBond, "pve1", "bond0"),
		Mode:   "802.3ad", Slaves: []string{"eno1", "eno2"}, Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	out := f.Render()
	requireConsistentCRLF(t, out)
	if _, err := host.ParseInterfaces([]byte(out)); err != nil {
		t.Fatalf("mutated CRLF output does not reparse: %v", err)
	}
	checkGolden(t, "bond-create-14.interfaces", out)
}

// TestGolden_IfaceUpdate_CRLF is F-06's in-stanza regression: inserting a
// comment line after the fixture's terminator-less final line must first
// terminate that line with the file's CRLF ending (not concatenate onto it,
// and not inject a bare LF).
func TestGolden_IfaceUpdate_CRLF(t *testing.T) {
	f, _ := parseCorpus(t, "14-crlf-no-trailing-newline.interfaces")
	op := IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve1", "eno1"), Comments: strPtr("uplink to core switch")}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	out := f.Render()
	requireConsistentCRLF(t, out)
	if !strings.Contains(out, "\tmtu 1500\r\n\t#uplink to core switch\r\n") {
		t.Errorf("expected the unterminated mtu line to be CRLF-terminated before the inserted comment, got:\n%q", out)
	}
	checkGolden(t, "iface-update-14.interfaces", out)
}

// TestUnifiedDiff_CRLFFixtureAcceptedByGNUPatch is F-06's end-to-end check:
// the rendered unified diff for a mutation of the CRLF/no-trailing-newline
// fixture — including the "\ No newline at end of file" marker — must be
// accepted by GNU patch, reproducing the mutated file byte-for-byte.
func TestUnifiedDiff_CRLFFixtureAcceptedByGNUPatch(t *testing.T) {
	patchBin, err := exec.LookPath("patch")
	if err != nil {
		t.Skip("GNU patch not installed; needs manual validation")
	}

	f, before := parseCorpus(t, "14-crlf-no-trailing-newline.interfaces")
	op := BondCreate{
		Target: ref(inventory.KindBond, "pve1", "bond0"),
		Mode:   "802.3ad", Slaves: []string{"eno1", "eno2"}, Autostart: true,
	}
	if mutErr := Mutate(f, op, goldenChangesetID); mutErr != nil {
		t.Fatalf("Mutate: %v", mutErr)
	}
	after := f.Render()
	ud := UnifiedDiff("a/interfaces", "b/interfaces", before, after)
	if !strings.Contains(ud, "\\ No newline at end of file") {
		t.Fatalf("diff of a no-trailing-newline file must carry the no-newline marker:\n%s", ud)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "interfaces")
	diffPath := filepath.Join(dir, "change.diff")
	if writeErr := os.WriteFile(target, []byte(before), 0o644); writeErr != nil {
		t.Fatalf("writing target: %v", writeErr)
	}
	if writeErr := os.WriteFile(diffPath, []byte(ud), 0o644); writeErr != nil {
		t.Fatalf("writing diff: %v", writeErr)
	}
	cmd := exec.Command(patchBin, "--binary", target, "-i", diffPath)
	if outBytes, patchErr := cmd.CombinedOutput(); patchErr != nil {
		t.Fatalf("GNU patch rejected the rendered diff: %v\n%s\ndiff:\n%s", patchErr, outBytes, ud)
	}
	patched, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading patched file: %v", err)
	}
	if string(patched) != after {
		t.Errorf("patched content != mutated content:\n--- patched ---\n%q\n--- want ---\n%q", patched, after)
	}
}
