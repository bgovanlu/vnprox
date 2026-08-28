// SPDX-License-Identifier: Apache-2.0

package pve_test

// Integration tests for pve.Client.CephConfig/CephOSDs (T-1503), exercised
// against internal/pvemock's fixture-driven implementation of the same two
// routes — the same "real httptest.Server round trip" convention every
// other client method in this package is tested against.

import (
	"context"
	"testing"
)

const fixtureCephClean = "../../testdata/ceph/clean.yaml"

func TestCephConfig_ReadsPublicClusterCIDRs(t *testing.T) {
	ts := newMockServer(t, fixtureCephClean)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")

	cfg, err := c.CephConfig(context.Background())
	if err != nil {
		t.Fatalf("CephConfig: %v", err)
	}
	if cfg.PublicNetwork != "10.20.0.0/24" {
		t.Errorf("PublicNetwork = %q, want 10.20.0.0/24", cfg.PublicNetwork)
	}
	if cfg.ClusterNetwork != "10.30.0.0/24" {
		t.Errorf("ClusterNetwork = %q, want 10.30.0.0/24", cfg.ClusterNetwork)
	}
}

func TestCephConfig_NoCephInstalled(t *testing.T) {
	// single-node.yaml declares no ceph: block at all — CephConfig must
	// report a zero value, not an error (see CephConfig's doc comment).
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")

	cfg, err := c.CephConfig(context.Background())
	if err != nil {
		t.Fatalf("CephConfig: %v", err)
	}
	if cfg.PublicNetwork != "" || cfg.ClusterNetwork != "" {
		t.Errorf("CephConfig on a Ceph-less cluster = %+v, want zero value", cfg)
	}
}

func TestCephOSDs_PerNodePlacement(t *testing.T) {
	ts := newMockServer(t, fixtureCephClean)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")

	osds, err := c.CephOSDs(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("CephOSDs: %v", err)
	}
	if len(osds) != 2 {
		t.Fatalf("pve1 OSDs = %d, want 2", len(osds))
	}
	for _, o := range osds {
		if o.Node != "pve1" {
			t.Errorf("OSD %d Node = %q, want pve1 (client-stamped)", o.ID, o.Node)
		}
		if !o.Up || !o.In {
			t.Errorf("OSD %d = %+v, want up=true in=true per fixture", o.ID, o)
		}
	}

	empty, err := c.CephOSDs(context.Background(), "pve-not-a-node")
	if err != nil {
		t.Fatalf("CephOSDs (unknown node): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("CephOSDs on unknown node = %v, want empty", empty)
	}
}
