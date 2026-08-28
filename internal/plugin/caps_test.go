// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
)

// fakeChangeCreator records whether Create was reached and with which ops, so a
// capability-rejection test can prove an out-of-scope op never reaches the
// change engine (T-1702 AC2).
type fakeChangeCreator struct {
	gotOps       []change.Op
	createCalled bool
}

func (f *fakeChangeCreator) Create(_ context.Context, _, _ string, ops []change.Op) (change.Changeset, error) {
	f.createCalled = true
	f.gotOps = ops
	return change.Changeset{ID: "cs-test"}, nil
}

func (f *fakeChangeCreator) Validate(_ context.Context, id, _ string) (change.Changeset, error) {
	return change.Changeset{ID: id}, nil
}

func TestRequiredCap(t *testing.T) {
	cases := []struct {
		op   change.OpType
		want auth.Cap
	}{
		{change.OpFwRuleCreate, auth.CapFWWrite},
		{change.OpFwOptionsUpdate, auth.CapFWWrite},
		{change.OpSdnZoneCreate, auth.CapSDNWrite},
		{change.OpSdnDnsRecordCreate, auth.CapSDNWrite},
		{change.OpSdnApply, auth.CapSDNWrite},
		{change.OpGuestNicUpdate, auth.CapGuestNet},
		{change.OpIfaceUpdate, auth.CapNetWrite},
		{change.OpBondCreate, auth.CapNetWrite},
		{change.OpType("some.future.op"), auth.CapNetWrite}, // fail-safe default
	}
	for _, tc := range cases {
		if got := RequiredCap(tc.op); got != tc.want {
			t.Errorf("RequiredCap(%q) = %q, want %q", tc.op, got, tc.want)
		}
	}
}

func TestNewScope_RejectsUnknownCapability(t *testing.T) {
	if _, err := NewScope([]string{"netRead", "notACap"}); err == nil {
		t.Fatal("NewScope accepted an unknown capability; want error")
	}
	s, err := NewScope([]string{"netRead", "netWrite"})
	if err != nil {
		t.Fatalf("NewScope(valid) error: %v", err)
	}
	if !s.Has(auth.CapNetRead) || !s.Has(auth.CapNetWrite) || s.Has(auth.CapSDNWrite) {
		t.Errorf("scope.Has wrong: %+v", s.Names())
	}
}

func TestValidateScope(t *testing.T) {
	readOnly, _ := NewScope([]string{"netRead"})
	withWrite, _ := NewScope([]string{"netRead", "netWrite"})

	// A read-only scope cannot attach the write-adjacent switch-driver point.
	if err := ValidateScope([]ExtensionPoint{ExtSwitchDriver}, readOnly); err == nil {
		t.Error("ValidateScope allowed switchDriver under a netRead-only scope; want error")
	}
	// With netWrite it may.
	if err := ValidateScope([]ExtensionPoint{ExtSwitchDriver}, withWrite); err != nil {
		t.Errorf("ValidateScope(switchDriver, netWrite) error: %v", err)
	}
	// Read-only points are fine under netRead.
	if err := ValidateScope([]ExtensionPoint{ExtFlowIngestor, ExtDashboardTile}, readOnly); err != nil {
		t.Errorf("ValidateScope(read points, netRead) error: %v", err)
	}
	// An unknown point is refused.
	if err := ValidateScope([]ExtensionPoint{ExtensionPoint("bogus")}, withWrite); err == nil {
		t.Error("ValidateScope allowed an unknown extension point; want error")
	}
	// No points at all is refused.
	if err := ValidateScope(nil, withWrite); err == nil {
		t.Error("ValidateScope allowed an empty extension-point set; want error")
	}
}

// TestScopedStager_RejectsOutOfScopeOp is T-1702 AC2: a plugin whose declared
// capabilities exclude netWrite cannot construct a netWrite-class op — it is
// rejected before reaching internal/change.
func TestScopedStager_RejectsOutOfScopeOp(t *testing.T) {
	readOnly, _ := NewScope([]string{"netRead"})
	fake := &fakeChangeCreator{}
	stager := newScopedStager(fake, "plugin:test", readOnly)

	_, err := stager.Create(context.Background(), "attempt", []change.Op{{Type: change.OpIfaceUpdate}})
	if !errors.Is(err, ErrCapabilityExceeded) {
		t.Fatalf("Create returned %v, want ErrCapabilityExceeded", err)
	}
	if fake.createCalled {
		t.Fatal("out-of-scope op reached the change engine; enforcement must happen before internal/change")
	}
}

// TestScopedStager_AllowsInScopeOp is the positive half: with netWrite the same
// op stages, and the underlying stage-only Create is reached exactly once.
func TestScopedStager_AllowsInScopeOp(t *testing.T) {
	withWrite, _ := NewScope([]string{"netRead", "netWrite"})
	fake := &fakeChangeCreator{}
	stager := newScopedStager(fake, "plugin:test", withWrite)

	cs, err := stager.Create(context.Background(), "ok", []change.Op{{Type: change.OpIfaceUpdate}})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if !fake.createCalled || cs.ID != "cs-test" {
		t.Fatalf("in-scope op did not reach the change engine: called=%v cs=%+v", fake.createCalled, cs)
	}
}

// TestScopedStager_MixedBatchRejectedWholesale proves the all-or-nothing rule: a
// batch mixing an in-scope and an out-of-scope op stages nothing.
func TestScopedStager_MixedBatchRejectedWholesale(t *testing.T) {
	fwOnly, _ := NewScope([]string{"netRead", "fwWrite"})
	fake := &fakeChangeCreator{}
	stager := newScopedStager(fake, "plugin:test", fwOnly)

	ops := []change.Op{
		{Type: change.OpFwRuleCreate}, // in scope (fwWrite)
		{Type: change.OpIfaceUpdate},  // out of scope (needs netWrite)
	}
	if _, err := stager.Create(context.Background(), "mixed", ops); !errors.Is(err, ErrCapabilityExceeded) {
		t.Fatalf("Create returned %v, want ErrCapabilityExceeded", err)
	}
	if fake.createCalled {
		t.Fatal("a mixed batch was partially staged; must be all-or-nothing")
	}
}
