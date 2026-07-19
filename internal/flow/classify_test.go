package flow

import "testing"

// TestClassifier_Classify covers T-1504 AC1: synthetic flow records
// matching corosync's ring addresses, a declared migration network, PBS
// backup-path edges (via a registered source, since this repo has no
// T-1206 yet — see classify.go's doc comment), and a declared Ceph CIDR
// (via a registered test source, standing in for T-1503) each classify
// correctly; an unmatched record classifies unclassified.
func TestClassifier_Classify(t *testing.T) {
	c := NewClassifier()
	c.RegisterNetworkSource(NetworkSourceKindCorosync, NewCorosyncSource([]string{"10.10.10.1", "10.10.10.2"}, nil))

	migSrc, err := NewMigrationNetworkSource([]string{"10.20.0.0/24"}, nil)
	if err != nil {
		t.Fatalf("NewMigrationNetworkSource: %v", err)
	}
	c.RegisterNetworkSource(NetworkSourceKindMigration, migSrc)

	c.RegisterNetworkSource(NetworkSourceKindBackup, NewBackupPathSource([]string{"10.30.0.5"}, nil))

	cephSrc, err := NewCIDRSource(NetworkSourceKindCeph, ServiceClassCephPublic, []string{"10.40.0.0/24"}, nil)
	if err != nil {
		t.Fatalf("NewCIDRSource(ceph): %v", err)
	}
	c.RegisterNetworkSource(NetworkSourceKindCeph, cephSrc)

	tests := []struct {
		name string
		want ServiceClass
		rec  Record
	}{
		{
			name: "corosync ring address",
			rec:  Record{SrcIP: "10.10.10.1", DstIP: "10.10.10.2", Proto: 17},
			want: ServiceClassCorosync,
		},
		{
			name: "corosync ring address as dst",
			rec:  Record{SrcIP: "192.168.1.5", DstIP: "10.10.10.2", Proto: 17},
			want: ServiceClassCorosync,
		},
		{
			name: "configured migration network",
			rec:  Record{SrcIP: "10.20.0.5", DstIP: "10.20.0.9", Proto: 6, DstPort: 60000},
			want: ServiceClassMigration,
		},
		{
			name: "PBS backup-path edge (registered test source)",
			rec:  Record{SrcIP: "192.168.1.10", DstIP: "10.30.0.5", Proto: 6, DstPort: 8007},
			want: ServiceClassBackup,
		},
		{
			name: "declared Ceph CIDR (registered test source, standing in for T-1503)",
			rec:  Record{SrcIP: "10.40.0.3", DstIP: "10.40.0.4", Proto: 6, DstPort: 6789},
			want: ServiceClassCephPublic,
		},
		{
			name: "unmatched record classifies unclassified",
			rec:  Record{SrcIP: "203.0.113.5", DstIP: "203.0.113.9", Proto: 6, DstPort: 443},
			want: ServiceClassUnclassified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Classify(tt.rec)
			if got != tt.want {
				t.Errorf("Classify(%+v) = %q, want %q", tt.rec, got, tt.want)
			}
		})
	}
}

func TestClassifier_Classify_EmptyClassifierAlwaysUnclassified(t *testing.T) {
	c := NewClassifier()
	got := c.Classify(Record{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", Proto: 6})
	if got != ServiceClassUnclassified {
		t.Errorf("Classify against an empty classifier = %q, want unclassified", got)
	}
}

// TestClassifier_RegistrationOrder_FirstMatchWins covers two sources
// registered under the same kind: the first-registered one that matches
// wins, later ones are never consulted for that record.
func TestClassifier_RegistrationOrder_FirstMatchWins(t *testing.T) {
	c := NewClassifier()
	c.RegisterNetworkSource(NetworkSourceKindCeph, NewAddrSetSource(NetworkSourceKindCeph, ServiceClassCephPublic, []string{"10.40.0.1"}, nil))
	c.RegisterNetworkSource(NetworkSourceKindCeph, NewAddrSetSource(NetworkSourceKindCeph, ServiceClassCephCluster, []string{"10.40.0.1"}, nil))

	got := c.Classify(Record{SrcIP: "10.40.0.1", DstIP: "10.40.0.2"})
	if got != ServiceClassCephPublic {
		t.Errorf("Classify = %q, want ceph-public (first-registered source wins)", got)
	}
}

// TestClassifier_Verdict_WrongNetwork covers the service_traffic_on_wrong_network
// finding's exact input (T-1504 AC3's classifier-level half): a classified
// record whose own VLAN falls outside the matching source's declared VLAN
// set is flagged WrongNetwork; a record on its declared VLAN, an
// unclassified record, a record with no known VLAN, and a match against a
// source declaring no VLANs are all never flagged.
func TestClassifier_Verdict_WrongNetwork(t *testing.T) {
	c := NewClassifier()
	c.RegisterNetworkSource(NetworkSourceKindCorosync, NewCorosyncSource([]string{"10.10.10.1"}, []int{10}))
	// Migration source declares no VLANs at all -> never a wrong-network verdict.
	migSrc, err := NewMigrationNetworkSource([]string{"10.20.0.0/24"}, nil)
	if err != nil {
		t.Fatalf("NewMigrationNetworkSource: %v", err)
	}
	c.RegisterNetworkSource(NetworkSourceKindMigration, migSrc)

	tests := []struct {
		name         string
		wantClass    ServiceClass
		rec          Record
		wantWrongNet bool
	}{
		{
			name:         "corosync traffic on its declared VLAN: not wrong",
			rec:          Record{SrcIP: "10.10.10.1", DstIP: "10.10.10.2", VLAN: 10},
			wantClass:    ServiceClassCorosync,
			wantWrongNet: false,
		},
		{
			name:         "corosync traffic observed on the guest VLAN: wrong network",
			rec:          Record{SrcIP: "10.10.10.1", DstIP: "10.10.10.2", VLAN: 20},
			wantClass:    ServiceClassCorosync,
			wantWrongNet: true,
		},
		{
			name:         "corosync traffic with no known VLAN: never flagged (nothing to judge)",
			rec:          Record{SrcIP: "10.10.10.1", DstIP: "10.10.10.2", VLAN: 0},
			wantClass:    ServiceClassCorosync,
			wantWrongNet: false,
		},
		{
			name:         "migration source declares no VLANs: never flagged regardless of VLAN",
			rec:          Record{SrcIP: "10.20.0.5", DstIP: "10.20.0.9", VLAN: 99},
			wantClass:    ServiceClassMigration,
			wantWrongNet: false,
		},
		{
			name:         "unclassified record: never flagged",
			rec:          Record{SrcIP: "203.0.113.1", DstIP: "203.0.113.2", VLAN: 99},
			wantClass:    ServiceClassUnclassified,
			wantWrongNet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Verdict(tt.rec)
			if got.ServiceClass != tt.wantClass {
				t.Errorf("ServiceClass = %q, want %q", got.ServiceClass, tt.wantClass)
			}
			if got.WrongNetwork != tt.wantWrongNet {
				t.Errorf("WrongNetwork = %v, want %v", got.WrongNetwork, tt.wantWrongNet)
			}
		})
	}
}

func TestClassifier_ClassifyBatch(t *testing.T) {
	c := NewClassifier()
	c.RegisterNetworkSource(NetworkSourceKindCorosync, NewCorosyncSource([]string{"10.10.10.1"}, nil))

	records := []Record{
		{SrcIP: "10.10.10.1", DstIP: "10.10.10.2"},
		{SrcIP: "203.0.113.1", DstIP: "203.0.113.2"},
	}
	got := c.ClassifyBatch(records)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ServiceClass != ServiceClassCorosync {
		t.Errorf("got[0].ServiceClass = %q, want corosync", got[0].ServiceClass)
	}
	if got[1].ServiceClass != ServiceClassUnclassified {
		t.Errorf("got[1].ServiceClass = %q, want unclassified", got[1].ServiceClass)
	}
}

func TestNewCIDRSource_InvalidCIDR(t *testing.T) {
	if _, err := NewCIDRSource(NetworkSourceKindMigration, ServiceClassMigration, []string{"not-a-cidr"}, nil); err == nil {
		t.Fatal("expected an error for a malformed CIDR, got nil")
	}
}

func TestClassifier_RegisterNetworkSource_NilSourceIsNoop(t *testing.T) {
	c := NewClassifier()
	c.RegisterNetworkSource(NetworkSourceKindCeph, nil)
	got := c.Classify(Record{SrcIP: "10.0.0.1", DstIP: "10.0.0.2"})
	if got != ServiceClassUnclassified {
		t.Errorf("Classify = %q, want unclassified (nil source registration should be a no-op)", got)
	}
}
