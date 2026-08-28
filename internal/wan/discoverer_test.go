// SPDX-License-Identifier: Apache-2.0

package wan

import (
	"context"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

type fakeTargetStore struct {
	byNode map[string][]store.WanTarget
}

func (f *fakeTargetStore) ListByNode(_ context.Context, node string) ([]store.WanTarget, error) {
	return f.byNode[node], nil
}

func (f *fakeTargetStore) ReplaceForNode(_ context.Context, node string, targets []store.WanTarget, _ int64) error {
	if f.byNode == nil {
		f.byNode = map[string][]store.WanTarget{}
	}
	f.byNode[node] = targets
	return nil
}

func TestTargetDiscoverer_Pairs(t *testing.T) {
	ts := &fakeTargetStore{byNode: map[string][]store.WanTarget{
		"pve1": {
			{Node: "pve1", Uplink: "vmbr0", Host: "1.1.1.1"},
			{Node: "pve1", Uplink: "vmbr1", Host: "8.8.8.8"},
		},
		"pve2": {
			{Node: "pve2", Uplink: "vmbr0", Host: "9.9.9.9"},
		},
	}}
	d := &TargetDiscoverer{Store: ts, LocalNode: func() string { return "pve1" }}

	pairs := d.Pairs()
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2 (pve1's own targets only): %+v", len(pairs), pairs)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Label < pairs[j].Label })

	if pairs[0].Label != "vmbr0" || pairs[0].ToNode != "1.1.1.1" || pairs[0].ToAddr != "1.1.1.1" {
		t.Errorf("pairs[0] = %+v, want Label=vmbr0 ToNode=ToAddr=1.1.1.1", pairs[0])
	}
	if pairs[0].FromNode != "pve1" || pairs[0].Fabric != Fabric {
		t.Errorf("pairs[0] FromNode/Fabric = %s/%s, want pve1/%s", pairs[0].FromNode, pairs[0].Fabric, Fabric)
	}
	wantLinkID := "wan:vmbr0|pve1->1.1.1.1"
	if pairs[0].LinkID != wantLinkID {
		t.Errorf("pairs[0].LinkID = %q, want %q", pairs[0].LinkID, wantLinkID)
	}

	if pairs[1].Label != "vmbr1" || pairs[1].ToNode != "8.8.8.8" {
		t.Errorf("pairs[1] = %+v, want Label=vmbr1 ToNode=8.8.8.8", pairs[1])
	}
}

func TestTargetDiscoverer_NilStoreOrLocalNode(t *testing.T) {
	d := &TargetDiscoverer{}
	if got := d.Pairs(); got != nil {
		t.Fatalf("got %v, want nil for a fully-unwired discoverer", got)
	}

	d2 := &TargetDiscoverer{Store: &fakeTargetStore{}, LocalNode: func() string { return "" }}
	if got := d2.Pairs(); got != nil {
		t.Fatalf("got %v, want nil for an empty LocalNode()", got)
	}
}

func TestTargetDiscoverer_NoConfiguredTargets(t *testing.T) {
	d := &TargetDiscoverer{Store: &fakeTargetStore{}, LocalNode: func() string { return "pve1" }}
	if got := d.Pairs(); len(got) != 0 {
		t.Fatalf("got %d pairs, want 0 for a node with no configured targets", len(got))
	}
}
