// SPDX-License-Identifier: Apache-2.0

package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// tcMirrorTestSnapshot is a one-node snapshot with two bridges (vmbr0,
// vmbr99) — the "known interface" T-4014's referential source/dest checks
// need.
func tcMirrorTestSnapshot() inventory.Snapshot {
	return buildSnapshot(
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr99"}, Name: "vmbr99"},
	)
}

// TestValidate_TcMirrorCreate is T-4014 acceptance criterion 1's table
// test: schema class catches a missing field / bad duration, referential
// class catches a nonexistent source/dest, and a fully valid op passes
// clean.
func TestValidate_TcMirrorCreate(t *testing.T) {
	snap := tcMirrorTestSnapshot()
	target := testRef(inventory.KindTcMirror, "pve1", "span1")

	cases := []struct {
		name   string
		params *TcMirrorCreateParams
		want   []wantFinding
	}{
		{
			name:   "valid",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: 3600},
			want:   nil,
		},
		{
			name:   "missing sourceIface",
			params: &TcMirrorCreateParams{DestIface: "vmbr99", MaxDurationSec: 3600},
			want:   []wantFinding{{sev: SeverityError, code: codeRequiredFieldMissing, ref: target.String()}},
		},
		{
			name:   "missing destIface",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", MaxDurationSec: 3600},
			want:   []wantFinding{{sev: SeverityError, code: codeRequiredFieldMissing, ref: target.String()}},
		},
		{
			name:   "source equals dest",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr0", MaxDurationSec: 3600},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorSameIface, ref: target.String()}},
		},
		{
			name:   "zero maxDurationSec",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: 0},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorDurationInvalid, ref: target.String()}},
		},
		{
			name:   "negative maxDurationSec",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: -5},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorDurationInvalid, ref: target.String()}},
		},
		{
			name:   "non-positive maxMbit",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: 60, MaxMbit: intPtr(0)},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorBandwidthInvalid, ref: target.String()}},
		},
		{
			name:   "source not found (referential rejection)",
			params: &TcMirrorCreateParams{SourceIface: "vmbr9", DestIface: "vmbr99", MaxDurationSec: 3600},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorSourceNotFound, ref: target.String()}},
		},
		{
			name:   "dest not found (referential rejection)",
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr9", MaxDurationSec: 3600},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorDestNotFound, ref: target.String()}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Validate([]Op{mkOp(OpTcMirrorCreate, target, c.params)}, snap)
			assertFindings(t, got, c.want)
		})
	}
}

// TestValidate_TcMirrorUpdate covers tc.mirror.update's single mutable
// field.
func TestValidate_TcMirrorUpdate(t *testing.T) {
	snap := tcMirrorTestSnapshot()
	target := testRef(inventory.KindTcMirror, "pve1", "span1")

	cases := []struct {
		name   string
		params *TcMirrorUpdateParams
		want   []wantFinding
	}{
		{name: "valid extend", params: &TcMirrorUpdateParams{MaxDurationSec: intPtr(7200)}, want: nil},
		{name: "nil is a no-op patch", params: &TcMirrorUpdateParams{}, want: nil},
		{
			name:   "zero maxDurationSec",
			params: &TcMirrorUpdateParams{MaxDurationSec: intPtr(0)},
			want:   []wantFinding{{sev: SeverityError, code: codeTcMirrorDurationInvalid, ref: target.String()}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Validate([]Op{mkOp(OpTcMirrorUpdate, target, c.params)}, snap)
			assertFindings(t, got, c.want)
		})
	}
}

// TestValidate_TcMirrorProtectedDest covers T-4014 acceptance criterion 3:
// a mirror destination that names the management/corosync path is
// rejected the same way an ordinary mgmt-path-cutting op already is
// (protected.go's ProtectedSet, codeProtectedInterface), and the rejection
// downgrades to a warning under AllowDangerousOps exactly like every other
// safety-class finding.
func TestValidate_TcMirrorProtectedDest(t *testing.T) {
	snap := tcMirrorTestSnapshot()
	target := testRef(inventory.KindTcMirror, "pve1", "span1")
	op := mkOp(OpTcMirrorCreate, target, &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: 60})
	protected := ProtectedSet{"pve1": {testRef(inventory.KindBridge, "pve1", "vmbr99")}}

	got := ValidateWithSafety([]Op{op}, snap, SafetyOptions{Protected: protected})
	assertFindings(t, got, []wantFinding{{sev: SeverityError, code: codeProtectedInterface, ref: target.String()}})

	// AllowDangerousOps downgrades it to a warning, never removes it.
	gotOverridden := ValidateWithSafety([]Op{op}, snap, SafetyOptions{Protected: protected, AllowDangerousOps: true})
	assertFindings(t, gotOverridden, []wantFinding{{sev: SeverityWarning, code: codeProtectedInterface, ref: target.String()}})

	// Mirroring FROM the protected bridge (not TO it) is unaffected — only
	// the destination is checked.
	reversed := mkOp(OpTcMirrorCreate, target, &TcMirrorCreateParams{SourceIface: "vmbr99", DestIface: "vmbr0", MaxDurationSec: 60})
	gotReversed := ValidateWithSafety([]Op{reversed}, snap, SafetyOptions{Protected: protected})
	assertFindings(t, gotReversed, nil)
}

// TestTcMirrorCapValidate covers T-4014 acceptance criterion 2: exceeding
// the configured concurrent-session or bandwidth cap is rejected at
// validate time with a named blocking finding — never silently clamped,
// and never downgradable by AllowDangerousOps (a resource ceiling, not a
// connectivity interlock).
func TestTcMirrorCapValidate(t *testing.T) {
	snap := tcMirrorTestSnapshot()
	target := testRef(inventory.KindTcMirror, "pve1", "span2")
	op := mkOp(OpTcMirrorCreate, target, &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxMbit: intPtr(50), MaxDurationSec: 60})

	cases := []struct {
		name  string
		want  []wantFinding
		input TcMirrorSafetyInput
	}{
		{
			name:  "unconfigured ceilings: no cap check runs",
			input: TcMirrorSafetyInput{},
			want:  nil,
		},
		{
			name: "under every ceiling",
			input: TcMirrorSafetyInput{
				Ceilings: TcMirrorLimits{MaxConcurrentPerNode: 4, MaxBandwidthMbit: 1000, MaxDurationSec: 3600},
				Usage:    map[string]TcMirrorUsage{"pve1": {Count: 1, Mbit: 100}},
			},
			want: nil,
		},
		{
			name: "concurrency cap exceeded",
			input: TcMirrorSafetyInput{
				Ceilings: TcMirrorLimits{MaxConcurrentPerNode: 1},
				Usage:    map[string]TcMirrorUsage{"pve1": {Count: 1}},
			},
			want: []wantFinding{{sev: SeverityError, code: codeTcMirrorConcurrencyCap, ref: target.String()}},
		},
		{
			name: "bandwidth cap exceeded",
			input: TcMirrorSafetyInput{
				Ceilings: TcMirrorLimits{MaxBandwidthMbit: 100},
				Usage:    map[string]TcMirrorUsage{"pve1": {Mbit: 80}},
			},
			want: []wantFinding{{sev: SeverityError, code: codeTcMirrorBandwidthCap, ref: target.String()}},
		},
		{
			name: "duration ceiling exceeded",
			input: TcMirrorSafetyInput{
				Ceilings: TcMirrorLimits{MaxDurationSec: 30},
			},
			want: []wantFinding{{sev: SeverityError, code: codeTcMirrorDurationCap, ref: target.String()}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ValidateWithSafety([]Op{op}, snap, SafetyOptions{TcMirror: c.input})
			assertFindings(t, got, c.want)

			// AllowDangerousOps must never downgrade or clear a cap finding.
			if len(c.want) > 0 {
				gotOverridden := ValidateWithSafety([]Op{op}, snap, SafetyOptions{TcMirror: c.input, AllowDangerousOps: true})
				assertFindings(t, gotOverridden, c.want)
			}
		})
	}
}

// TestTcMirrorCapValidate_SourceInUse covers the "no conflicting existing
// qdisc" requirement: a source interface already claimed by another
// active session (per SafetyOptions.TcMirror.Usage) is rejected, checked
// against vnprox's own app-owned session accounting rather than live tc
// state.
func TestTcMirrorCapValidate_SourceInUse(t *testing.T) {
	snap := tcMirrorTestSnapshot()
	target := testRef(inventory.KindTcMirror, "pve1", "span2")
	op := mkOp(OpTcMirrorCreate, target, &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: 60})

	input := TcMirrorSafetyInput{Usage: map[string]TcMirrorUsage{
		"pve1": {Count: 1, Sources: map[string]bool{"vmbr0": true}},
	}}
	got := ValidateWithSafety([]Op{op}, snap, SafetyOptions{TcMirror: input})
	assertFindings(t, got, []wantFinding{{sev: SeverityError, code: codeTcMirrorSourceInUse, ref: target.String()}})

	// A different, unclaimed source is fine.
	free := mkOp(OpTcMirrorCreate, target, &TcMirrorCreateParams{SourceIface: "vmbr99", DestIface: "vmbr0", MaxDurationSec: 60})
	gotFree := ValidateWithSafety([]Op{free}, snap, SafetyOptions{TcMirror: input})
	assertFindings(t, gotFree, nil)

	// The caller's Usage map must not be mutated by validation.
	if !input.Usage["pve1"].Sources["vmbr0"] || len(input.Usage["pve1"].Sources) != 1 {
		t.Fatalf("caller's Usage map was mutated: %+v", input.Usage["pve1"])
	}
}

// TestTcMirrorCapValidate_BatchAccumulatesWithinChangeset proves a single
// changeset staging several tc.mirror.create ops for the same node cannot
// smuggle a batch past the concurrency ceiling one op at a time.
func TestTcMirrorCapValidate_BatchAccumulatesWithinChangeset(t *testing.T) {
	snap := tcMirrorTestSnapshot()
	op1 := mkOp(OpTcMirrorCreate, testRef(inventory.KindTcMirror, "pve1", "span1"),
		&TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxDurationSec: 60})
	op2 := mkOp(OpTcMirrorCreate, testRef(inventory.KindTcMirror, "pve1", "span2"),
		&TcMirrorCreateParams{SourceIface: "vmbr99", DestIface: "vmbr0", MaxDurationSec: 60})

	input := TcMirrorSafetyInput{Ceilings: TcMirrorLimits{MaxConcurrentPerNode: 1}}
	got := ValidateWithSafety([]Op{op1, op2}, snap, SafetyOptions{TcMirror: input})
	assertFindings(t, got, []wantFinding{
		{sev: SeverityError, code: codeTcMirrorConcurrencyCap, ref: op2.Target.String()},
	})
}
