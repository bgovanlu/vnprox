package topology

// Direct table tests for the status-derivation and badge helpers
// (statusOf / bondStatus / badgesOf) — the docs/features/topology.md §2
// "Status painting" rules that the pvemock golden fixtures never exercise
// beyond ok/unknown (audit finding F-17). Snapshots are built by applying
// hand-constructed inventory entities through the real merge
// (Graph.ApplyPoll), so provenance — which the helpers consult for
// host-netlink-only fields — is produced by the real pipeline, not faked.

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// applyAll builds a graph from per-source entity batches and returns its
// snapshot. Batches apply in map-independent, explicit order.
type sourceBatch struct {
	source   inventory.Source
	entities []inventory.Entity
}

func snapshotOf(t *testing.T, batches ...sourceBatch) inventory.Snapshot {
	t.Helper()
	g := inventory.NewGraph()
	for _, b := range batches {
		g.ApplyPoll(b.source, inventory.Scope{}, b.entities)
	}
	return g.Snapshot()
}

func resolved(t *testing.T, snap inventory.Snapshot, ref inventory.Ref) inventory.Entity {
	t.Helper()
	e, ok := snap.Get(ref)
	if !ok {
		t.Fatalf("entity %s not resolved in snapshot", ref)
	}
	return e
}

func TestStatusOf_PhysNic(t *testing.T) {
	ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: "eno1"}

	tests := []struct {
		name    string
		want    Status
		batches []sourceBatch
	}{
		{
			name: "link down reported by netlink paints down",
			batches: []sourceBatch{{inventory.SourceHostNetlink, []inventory.Entity{
				&inventory.PhysNic{Ref: ref, Name: "eno1", LinkUp: false, LinkUpSet: true, OperState: "down"},
			}}},
			want: StatusDown,
		},
		{
			name: "link up reported by netlink paints ok",
			batches: []sourceBatch{{inventory.SourceHostNetlink, []inventory.Entity{
				&inventory.PhysNic{Ref: ref, Name: "eno1", LinkUp: true, LinkUpSet: true, OperState: "up"},
			}}},
			want: StatusOK,
		},
		{
			// The wave-1 optional-boolean contract: a NIC no source ever
			// reported linkUp for (peer node, pve-network-only data) must
			// render unknown, never a false "down" from the zero value.
			name: "link unreported (pve-network only) paints unknown, not down",
			batches: []sourceBatch{{inventory.SourcePVENetwork, []inventory.Entity{
				&inventory.PhysNic{Ref: ref, Name: "eno1", MTUDeclared: 1500},
			}}},
			want: StatusUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := snapshotOf(t, tt.batches...)
			if got := statusOf(snap, resolved(t, snap, ref)); got != tt.want {
				t.Errorf("statusOf = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBondStatus(t *testing.T) {
	ref := inventory.Ref{Kind: inventory.KindBond, Node: "n1", ID: "bond0"}

	tests := []struct {
		name       string
		batches    []sourceBatch
		want       Status
		wantBadges []string // badges that must be present on the node
		banBadges  []string // badges that must NOT be present
	}{
		{
			name: "declared slave missing from runtime membership is degraded with missing-slave badge",
			batches: []sourceBatch{
				{inventory.SourceHostNetlink, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", Mode: "802.3ad",
						Slaves:      []string{"eno1"},
						SlaveDetail: []inventory.BondSlaveState{{Name: "eno1", MIIStatus: "up", Active: true}},
					},
				}},
				{inventory.SourcePVENetwork, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", DeclaredSlaves: []string{"eno1", "eno2"}},
				}},
			},
			want:       StatusDegraded,
			wantBadges: []string{"missing-slave", "mode=802.3ad"},
		},
		{
			name: "inactive slave is degraded",
			batches: []sourceBatch{
				{inventory.SourceHostNetlink, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", Mode: "active-backup",
						Slaves: []string{"eno1", "eno2"},
						SlaveDetail: []inventory.BondSlaveState{
							{Name: "eno1", MIIStatus: "up", Active: true},
							{Name: "eno2", MIIStatus: "down", Active: false},
						},
					},
				}},
				{inventory.SourcePVENetwork, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", DeclaredSlaves: []string{"eno1", "eno2"}},
				}},
			},
			want:       StatusDegraded,
			wantBadges: []string{"missing-slave"},
		},
		{
			name: "all declared slaves present and active is ok",
			batches: []sourceBatch{
				{inventory.SourceHostNetlink, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", Mode: "802.3ad",
						Slaves: []string{"eno1", "eno2"},
						SlaveDetail: []inventory.BondSlaveState{
							{Name: "eno1", MIIStatus: "up", Active: true},
							{Name: "eno2", MIIStatus: "up", Active: true},
						},
					},
				}},
				{inventory.SourcePVENetwork, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", DeclaredSlaves: []string{"eno1", "eno2"}},
				}},
			},
			want:      StatusOK,
			banBadges: []string{"missing-slave"},
		},
		{
			// No source ever reported runtime membership (declared-only
			// pve-network data, e.g. a peer node): unknown, never a guessed
			// "degraded" — and no missing-slave badge either.
			name: "runtime slaves unreported is unknown, not degraded",
			batches: []sourceBatch{
				{inventory.SourcePVENetwork, []inventory.Entity{
					&inventory.Bond{Ref: ref, Name: "bond0", Mode: "802.3ad", DeclaredSlaves: []string{"eno1", "eno2"}},
				}},
			},
			want:      StatusUnknown,
			banBadges: []string{"missing-slave"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := snapshotOf(t, tt.batches...)
			e := resolved(t, snap, ref)
			bond, ok := e.(*inventory.Bond)
			if !ok {
				t.Fatalf("resolved entity is %T, want *inventory.Bond", e)
			}
			prov, _ := snap.Provenance(ref)
			if got := bondStatus(bond, prov); got != tt.want {
				t.Errorf("bondStatus = %q, want %q", got, tt.want)
			}
			if got := statusOf(snap, e); got != tt.want {
				t.Errorf("statusOf = %q, want %q", got, tt.want)
			}
			badges := badgesOf(snap, e)
			for _, want := range tt.wantBadges {
				if !hasBadge(badges, want) {
					t.Errorf("badges = %v, want to contain %q", badges, want)
				}
			}
			for _, ban := range tt.banBadges {
				if hasBadge(badges, ban) {
					t.Errorf("badges = %v, must not contain %q", badges, ban)
				}
			}
		})
	}
}

func TestStatusOf_SdnZone(t *testing.T) {
	ref := inventory.Ref{Kind: inventory.KindSDNZone, ID: "z1"}

	tests := []struct {
		name       string
		nodeStatus map[string]string
		want       Status
	}{
		{"no per-node status reported yet is unknown", nil, StatusUnknown},
		{"all nodes ok is ok", map[string]string{"n1": "ok", "n2": "OK"}, StatusOK},
		{"any non-ok node degrades the zone", map[string]string{"n1": "ok", "n2": "error"}, StatusDegraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := snapshotOf(t, sourceBatch{inventory.SourcePVESDN, []inventory.Entity{
				&inventory.SdnZone{Ref: ref, ID: "z1", Type: "vlan", NodeStatus: tt.nodeStatus},
			}})
			if got := statusOf(snap, resolved(t, snap, ref)); got != tt.want {
				t.Errorf("statusOf = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusAndBadgesOf_GuestNic(t *testing.T) {
	guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: "100"}
	ref := inventory.Ref{Kind: inventory.KindGuestNic, Node: "n1", ID: "100/net0"}

	snap := snapshotOf(t, sourceBatch{inventory.SourcePVEGuest, []inventory.Entity{
		&inventory.Guest{Ref: guestRef, VMID: 100, Name: "vm100", Status: "running"},
		// Vid, not EffectiveVid: the graph's linking pass recomputes
		// EffectiveVid from Vid (+ any VNet tag) — see inventory/link.go.
		&inventory.GuestNic{Ref: ref, Guest: guestRef, Key: "net0", TargetName: "vmbr0", Vid: 55, LinkDown: true},
	}})
	e := resolved(t, snap, ref)
	if got := statusOf(snap, e); got != StatusDown {
		t.Errorf("statusOf(link-down guest nic) = %q, want %q", got, StatusDown)
	}
	badges := badgesOf(snap, e)
	for _, want := range []string{"link-down", "vid=55"} {
		if !hasBadge(badges, want) {
			t.Errorf("badges = %v, want to contain %q", badges, want)
		}
	}
}

func TestBadgesOf_Bridge_OptionalVlanAware(t *testing.T) {
	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}

	t.Run("vlan-aware bridge shows trunked vid ranges", func(t *testing.T) {
		snap := snapshotOf(t, sourceBatch{inventory.SourceHostNetlink, []inventory.Entity{
			&inventory.Bridge{Ref: ref, Name: "vmbr0", Virt: inventory.BridgeLinux,
				VlanAware: true, VlanAwareSet: true,
				Vids:      []inventory.VidRange{{Low: 10, High: 20}},
				PortNames: []string{"eno1"},
			},
		}})
		badges := badgesOf(snap, resolved(t, snap, ref))
		if !hasBadge(badges, "vlans=10-20") {
			t.Errorf("badges = %v, want to contain vlans=10-20", badges)
		}
	})

	t.Run("vlanAware unreported renders as not-vlan-aware (no badge), not false data", func(t *testing.T) {
		// pve-network partial that never reported vlanAware (VlanAwareSet
		// false): the resolved bridge must not sprout a vlans badge from
		// zero-value noise.
		snap := snapshotOf(t, sourceBatch{inventory.SourcePVENetwork, []inventory.Entity{
			&inventory.Bridge{Ref: ref, Name: "vmbr0", Virt: inventory.BridgeLinux, DeclaredPortNames: []string{"eno1"}},
		}})
		badges := badgesOf(snap, resolved(t, snap, ref))
		if hasBadge(badges, "vlans=") || len(badges) != 0 {
			t.Errorf("badges = %v, want none", badges)
		}
	})
}
