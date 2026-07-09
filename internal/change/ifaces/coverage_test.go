package ifaces

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestMutate_NotFoundAndExistsErrors exercises every op's error path: a
// Create op targeting a name that already has an iface stanza (ErrExists),
// and an Update/Delete/PortAdd/PortRemove op targeting a name that has none
// (ErrNotFound).
func TestMutate_NotFoundAndExistsErrors(t *testing.T) {
	f, _ := parseCorpus(t, "03-bond-with-vlans.interfaces") // has bond0, vmbr1; no "missing"/"bond0" dup guard

	createCases := []Op{
		BondCreate{Target: ref(inventory.KindBond, "pve1", "bond0")},
		BridgeCreate{Target: ref(inventory.KindBridge, "pve1", "vmbr1")},
		VlanCreate{Target: ref(inventory.KindVlan, "pve1", "bond0.10")},
	}
	for _, op := range createCases {
		if err := Mutate(f, op, "cs"); !errors.Is(err, ErrExists) {
			t.Errorf("%T: err = %v, want ErrExists", op, err)
		}
	}

	notFoundCases := []Op{
		BondUpdate{Target: ref(inventory.KindBond, "pve1", "missing")},
		BondDelete{Target: ref(inventory.KindBond, "pve1", "missing")},
		BridgeUpdate{Target: ref(inventory.KindBridge, "pve1", "missing")},
		BridgeDelete{Target: ref(inventory.KindBridge, "pve1", "missing")},
		BridgePortAdd{Target: ref(inventory.KindBridge, "pve1", "missing"), Port: "eno9"},
		BridgePortRemove{Target: ref(inventory.KindBridge, "pve1", "missing"), Port: "eno9"},
		VlanUpdate{Target: ref(inventory.KindVlan, "pve1", "missing")},
		VlanDelete{Target: ref(inventory.KindVlan, "pve1", "missing")},
		IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve1", "missing")},
	}
	for _, op := range notFoundCases {
		if err := Mutate(f, op, "cs"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%T: err = %v, want ErrNotFound", op, err)
		}
	}
}

func TestMutate_UnsupportedOpType(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	if err := Mutate(f, unsupportedOp{}, "cs"); err == nil {
		t.Fatal("expected an error for an unrecognized Op implementation")
	}
}

type unsupportedOp struct{}

func (unsupportedOp) Kind() OpType       { return "bogus.op" }
func (unsupportedOp) Ref() inventory.Ref { return inventory.Ref{} }

func TestMutateAll_StopsAtFirstError(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	ops := []Op{
		BondCreate{Target: ref(inventory.KindBond, "pve1", "bond0"), Slaves: []string{"eno1"}},
		BondDelete{Target: ref(inventory.KindBond, "pve1", "does-not-exist")},
	}
	err := MutateAll(f, ops, "cs")
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want wrapped ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "op[1]") {
		t.Errorf("error %q should identify the failing op index", err.Error())
	}
}

// TestBridgePortRemove_LastPortClearsOption checks that removing the only
// remaining port drops the option line entirely (via setOption's
// empty-value -> removeOptionKey path) rather than leaving a
// "bridge-ports " line with a trailing space.
func TestBridgePortRemove_LastPortClearsOption(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	op := BridgePortRemove{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Port: "eno1"}
	if err := Mutate(f, op, "cs"); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	e, ok := f.Iface("vmbr0")
	if !ok {
		t.Fatal("vmbr0 not found")
	}
	if _, ok := e.Get("bridge-ports"); ok {
		t.Errorf("expected bridge-ports option to be removed entirely, got %+v", e.Options())
	}
}

// TestSetAutostart_MultiNameLine checks turning autostart off for one name
// in a shared "auto eno1 eno2" line regenerates that line with only the
// other name (rather than dropping the line, or leaving eno1 in it) — the
// multi-name branch of removeAutoReference/regenerateIfaceListRaw. None of
// T-102's corpus fixtures happen to declare a shared multi-name auto line
// (interfaces(5) allows it, ifupdown2's own reference config just doesn't
// use it), so this constructs one directly; the resulting file must still
// reparse cleanly and AutoIfaces() must reflect exactly the remaining name.
func TestSetAutostart_MultiNameLine(t *testing.T) {
	src := "auto eno1 eno2\niface eno1 inet manual\niface eno2 inet manual\n"
	f, err := host.ParseInterfaces([]byte(src))
	if err != nil {
		t.Fatalf("ParseInterfaces: %v", err)
	}
	autostart := false
	op := IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve1", "eno1"), Autostart: &autostart}
	if mutErr := Mutate(f, op, "cs"); mutErr != nil {
		t.Fatalf("Mutate: %v", mutErr)
	}
	reparsed, err := host.ParseInterfaces([]byte(f.Render()))
	if err != nil {
		t.Fatalf("mutated output does not reparse: %v\n%s", err, f.Render())
	}
	auto := reparsed.AutoIfaces()
	hasEno1, hasEno2 := false, false
	for _, n := range auto {
		if n == "eno1" {
			hasEno1 = true
		}
		if n == "eno2" {
			hasEno2 = true
		}
	}
	if hasEno1 {
		t.Errorf("eno1 should no longer be autostarted, AutoIfaces() = %v", auto)
	}
	if !hasEno2 {
		t.Errorf("eno2 should remain autostarted, AutoIfaces() = %v", auto)
	}
	if strings.Contains(f.Render(), "auto eno1 eno2") {
		t.Errorf("expected the shared auto line to be rewritten, got:\n%s", f.Render())
	}
}

// TestSummarize_AllOpTypes exercises Summarize/summaryText for every op
// type this package implements, including branches within IfaceUpdate's
// field-list rendering (mtu, clear-address, clear-gateway, autostart,
// comments) that the golden/property tests don't otherwise reach.
func TestSummarize_AllOpTypes(t *testing.T) {
	mtu := 1500
	comment := "c"
	autostart := true
	ops := []Op{
		IfaceUpdate{Target: ref(inventory.KindPhysNic, "n", "eno1"), MTU: &mtu, Comments: &comment, Autostart: &autostart},
		IfaceUpdate{Target: ref(inventory.KindPhysNic, "n", "eno1"), RemoveAddress: true, RemoveGateway: true},
		BondCreate{Target: ref(inventory.KindBond, "n", "bond0")}, // empty Mode -> orDash "-"
		BondUpdate{Target: ref(inventory.KindBond, "n", "bond0")},
		BondDelete{Target: ref(inventory.KindBond, "n", "bond0")},
		BridgeCreate{Target: ref(inventory.KindOVSBridge, "n", "vmbr9")},
		BridgeUpdate{Target: ref(inventory.KindBridge, "n", "vmbr0")},
		BridgeDelete{Target: ref(inventory.KindBridge, "n", "vmbr0")},
		BridgePortAdd{Target: ref(inventory.KindBridge, "n", "vmbr0"), Port: "eno2"},
		BridgePortRemove{Target: ref(inventory.KindBridge, "n", "vmbr0"), Port: "eno2"},
		VlanCreate{Target: ref(inventory.KindVlan, "n", "vmbr0.10"), Parent: "vmbr0", VID: 10},
		VlanUpdate{Target: ref(inventory.KindVlan, "n", "vmbr0.10")},
		VlanDelete{Target: ref(inventory.KindVlan, "n", "vmbr0.10")},
		unsupportedOp{},
	}
	for _, op := range ops {
		s := Summarize(op)
		if s.Summary == "" {
			t.Errorf("%T: empty summary", op)
		}
		if s.Op != string(op.Kind()) {
			t.Errorf("%T: Op = %q, want %q", op, s.Op, op.Kind())
		}
	}
}

func TestDecodeOp_MalformedEnvelope(t *testing.T) {
	if _, err := DecodeOp(json.RawMessage(`not json`)); err == nil {
		t.Fatal("expected an error for malformed envelope JSON")
	}
	if _, err := DecodeOp(json.RawMessage(`{"op":"bond.create","target":"bond:n:b0","params":"not an object"}`)); err == nil {
		t.Fatal("expected an error for malformed params JSON")
	}
}

func TestDecodeOps_MalformedArray(t *testing.T) {
	if _, err := DecodeOps(json.RawMessage(`not an array`)); err == nil {
		t.Fatal("expected an error for malformed ops array JSON")
	}
	if _, err := DecodeOps(json.RawMessage(`[{"op":"bogus","target":"bond:n:b0","params":{}}]`)); err == nil {
		t.Fatal("expected an error propagated from a bad element")
	}
}

// TestNewDiffHandler_LookupError checks the plain-error (non-ErrChangesetNotFound)
// branch maps to a 500, and TestNewDiffHandler_DiffError checks a
// DiffChangeset failure (unknown node) also maps to a 500.
func TestNewDiffHandler_LookupError(t *testing.T) {
	reader := loadThreeNodeVlanReader(t)
	lookup := fakeLookup{err: errors.New("boom")}
	h := NewDiffHandler(lookup, reader, func(r *http.Request) string { return "x" })
	req := httptest.NewRequest(http.MethodGet, "/changesets/x/diff", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestNewDiffHandler_DiffError(t *testing.T) {
	reader := loadThreeNodeVlanReader(t)
	lookup := fakeLookup{ops: []Op{IfaceUpdate{Target: ref(inventory.KindPhysNic, "no-such-node", "eno1")}}}
	h := NewDiffHandler(lookup, reader, func(r *http.Request) string { return "x" })
	req := httptest.NewRequest(http.MethodGet, "/changesets/x/diff", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

func TestOVSBondModeOptions_DefaultMode(t *testing.T) {
	if got := ovsBondModeOptions("active-backup"); got != "bond_mode=active-backup" {
		t.Errorf("ovsBondModeOptions(active-backup) = %q", got)
	}
}

func TestSetCommentLine_Remove(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	comment := "temp"
	set := comment
	if err := Mutate(f, IfaceUpdate{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Comments: &set}, "cs"); err != nil {
		t.Fatalf("Mutate (set): %v", err)
	}
	empty := ""
	if err := Mutate(f, IfaceUpdate{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Comments: &empty}, "cs"); err != nil {
		t.Fatalf("Mutate (clear): %v", err)
	}
	e, _ := f.Iface("vmbr0")
	for _, b := range e.Body {
		if strings.Contains(b.Raw, "#temp") {
			t.Errorf("expected the comment line to be removed, still present: %+v", e.Body)
		}
	}
}

// TestSetCommentLine_Overwrite covers replacing an already-present plain
// comment line with new text in place (as opposed to inserting a fresh one
// or removing it), the third branch setCommentLine's idx>=0/text!=""
// path exercises.
func TestSetCommentLine_Overwrite(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	first := "first comment"
	if err := Mutate(f, IfaceUpdate{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Comments: &first}, "cs"); err != nil {
		t.Fatalf("Mutate (first): %v", err)
	}
	second := "second comment"
	if err := Mutate(f, IfaceUpdate{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Comments: &second}, "cs"); err != nil {
		t.Fatalf("Mutate (second): %v", err)
	}
	out := f.Render()
	if strings.Contains(out, "first comment") {
		t.Errorf("expected the first comment to be replaced, got:\n%s", out)
	}
	if !strings.Contains(out, "second comment") {
		t.Errorf("expected the second comment to be present, got:\n%s", out)
	}
}

// TestSetAutostart_InsertsForPreviouslyUnstartedIface covers setAutostart's
// insertion branch (want=true, no existing auto/allow-auto line) — as
// opposed to the Create mutators' own appendStanza-based auto-line
// handling, this is IfaceUpdate flipping an *existing*, not-yet-autostarted
// stanza on.
func TestSetAutostart_InsertsForPreviouslyUnstartedIface(t *testing.T) {
	src := "iface eno5 inet manual\n\tmtu 1500\n"
	f, err := host.ParseInterfaces([]byte(src))
	if err != nil {
		t.Fatalf("ParseInterfaces: %v", err)
	}
	autostart := true
	if mutErr := Mutate(f, IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve1", "eno5"), Autostart: &autostart}, "cs"); mutErr != nil {
		t.Fatalf("Mutate: %v", mutErr)
	}
	reparsed, err := host.ParseInterfaces([]byte(f.Render()))
	if err != nil {
		t.Fatalf("mutated output does not reparse: %v\n%s", err, f.Render())
	}
	found := false
	for _, n := range reparsed.AutoIfaces() {
		if n == "eno5" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected eno5 to be autostarted, AutoIfaces() = %v", reparsed.AutoIfaces())
	}
}

// TestAddToken_AlreadyPresent checks BridgePortAdd is a no-op (rather than
// duplicating the port) when the port is already in the list.
func TestAddToken_AlreadyPresent(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	op := BridgePortAdd{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Port: "eno1"}
	if err := Mutate(f, op, "cs"); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	e, _ := f.Iface("vmbr0")
	v, _ := e.Get("bridge-ports")
	if v != "eno1" {
		t.Errorf("bridge-ports = %q, want unchanged %q (no duplicate)", v, "eno1")
	}
}

func TestDecodeOp_NoParamsField(t *testing.T) {
	op, err := DecodeOp(json.RawMessage(`{"op":"bond.delete","target":"bond:pve1:bond0"}`))
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	if op.Kind() != OpBondDelete {
		t.Errorf("Kind() = %v, want %v", op.Kind(), OpBondDelete)
	}
}

func TestUnifiedDiff_SingleLineHunk(t *testing.T) {
	d := UnifiedDiff("f", "f", "x\n", "y\n")
	if !strings.Contains(d, "@@ -1 +1 @@") {
		t.Errorf("expected a singular (count=1) hunk header, got:\n%s", d)
	}
}
