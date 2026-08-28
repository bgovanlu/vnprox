// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"context"
	"strings"
	"testing"
)

func TestFixtureHostReader_InterfacesFileRendersLiveAndPending(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "messy-brownfield.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)
	ctx := context.Background()

	live, err := reader.InterfacesFile(ctx, "pve2", false)
	if err != nil {
		t.Fatalf("InterfacesFile(live): %v", err)
	}
	liveEno1 := stanza(t, live, "eno1")
	if !strings.Contains(liveEno1, "mtu 1500") {
		t.Errorf("live eno1 stanza should show original mtu 1500:\n%s", liveEno1)
	}

	pending, err := reader.InterfacesFile(ctx, "pve2", true)
	if err != nil {
		t.Fatalf("InterfacesFile(pending): %v", err)
	}
	pendingEno1 := stanza(t, pending, "eno1")
	if !strings.Contains(pendingEno1, "mtu 9000") {
		t.Errorf("pending eno1 stanza should show staged mtu 9000:\n%s", pendingEno1)
	}
}

// stanza extracts the "iface <name> ..." paragraph from a rendered
// /etc/network/interfaces(5) file, so assertions can target one
// interface's fields without being confused by others in the same file.
func stanza(t *testing.T, rendered, iface string) string {
	t.Helper()
	marker := "iface " + iface + " "
	idx := strings.Index(rendered, marker)
	if idx == -1 {
		t.Fatalf("stanza for %q not found in:\n%s", iface, rendered)
	}
	rest := rendered[idx:]
	if end := strings.Index(rest, "\n\n"); end != -1 {
		return rest[:end]
	}
	return rest
}

func TestFixtureHostReader_Links(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "three-node-vlan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)

	links, err := reader.Links(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	var bond *LinkState
	for i := range links {
		if links[i].Name == "bond0" {
			bond = &links[i]
		}
	}
	if bond == nil {
		t.Fatalf("bond0 not found in links: %+v", links)
	}
	if len(bond.Members) != 2 {
		t.Errorf("bond0 members = %v, want 2 (eno1, eno2)", bond.Members)
	}
}

func TestFixtureHostReader_LLDPAndStats(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)
	ctx := context.Background()

	lldp, err := reader.LLDP(ctx, "pve1")
	if err != nil {
		t.Fatalf("LLDP: %v", err)
	}
	if !strings.Contains(string(lldp), "sw-access-01") {
		t.Errorf("lldp JSON missing expected chassis name: %s", lldp)
	}

	stats, err := reader.Stats(ctx, "pve1")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if _, ok := stats["eno1"]; !ok {
		t.Errorf("stats missing eno1: %+v", stats)
	}
}

func TestFixtureHostReader_UnknownNode(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)
	if _, err := reader.InterfacesFile(context.Background(), "nope", false); err == nil {
		t.Fatalf("expected error for unknown node")
	}
}
