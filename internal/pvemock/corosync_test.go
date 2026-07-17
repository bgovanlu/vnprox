package pvemock

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestFixtureHostReader_CorosyncStatus_ThreeNodeVlan loads
// three-node-vlan.yaml (T-803's extension) and verifies the corosync reader
// renders each node's declared ring status into corosync-cfgtool -s's
// realistic-shaped plain-text output, healthy and faulty alike.
func TestFixtureHostReader_CorosyncStatus_ThreeNodeVlan(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "three-node-vlan.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	ctx := context.Background()

	healthy, err := r.CorosyncStatus(ctx, "pve1")
	if err != nil {
		t.Fatalf("CorosyncStatus(pve1): %v", err)
	}
	if !strings.Contains(string(healthy), "RING ID 0") || !strings.Contains(string(healthy), "no faults") {
		t.Errorf("pve1 corosync status = %q, want two healthy rings", healthy)
	}

	faulty, err := r.CorosyncStatus(ctx, "pve3")
	if err != nil {
		t.Fatalf("CorosyncStatus(pve3): %v", err)
	}
	if !strings.Contains(string(faulty), "FAULTY") {
		t.Errorf("pve3 corosync status = %q, want a FAULTY ring", faulty)
	}
}

// TestFixtureHostReader_CorosyncStatus_Unavailable verifies a node with no
// `corosync:` block declared (single-node.yaml's only node has none)
// returns ErrCorosyncUnavailable cleanly (T-803's graceful-degradation
// case, mirroring T-404's FRR-unavailable convention).
func TestFixtureHostReader_CorosyncStatus_Unavailable(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	ctx := context.Background()

	node := onlyNodeName(t, f)
	if _, err := r.CorosyncStatus(ctx, node); !errors.Is(err, ErrCorosyncUnavailable) {
		t.Errorf("CorosyncStatus error = %v, want wrapped ErrCorosyncUnavailable", err)
	}
}

// TestFixtureHostReader_CorosyncStatus_UnknownNode verifies the ErrNotFound
// path (distinct from ErrCorosyncUnavailable) for a node the fixture
// doesn't know about at all.
func TestFixtureHostReader_CorosyncStatus_UnknownNode(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	ctx := context.Background()

	if _, err := r.CorosyncStatus(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("CorosyncStatus error = %v, want wrapped ErrNotFound", err)
	}
}
