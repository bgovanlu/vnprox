// SPDX-License-Identifier: Apache-2.0

package host

import "testing"

const bonding8023adFixture = `Ethernet Channel Bonding Driver: v6.6.0

Bonding Mode: IEEE 802.3ad Dynamic link aggregation
Transmit Hash Policy: layer3+4 (1)
MII Status: up
MII Polling Interval (ms): 100
Up Delay (ms): 0
Down Delay (ms): 0

802.3ad info
LACP rate: fast
Min links: 0
Aggregator selection policy (ad_select): stable
System priority: 65535
System MAC address: bc:24:11:00:00:0a
bond bond0 has a active aggregator

Slave Interface: eno1
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 0
Permanent HW addr: bc:24:11:00:00:01
Slave queue ID: 0
Aggregator ID: 1
Actor Churn State: none
Partner Churn State: none

Slave Interface: eno2
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 2
Permanent HW addr: bc:24:11:00:00:02
Slave queue ID: 0
Aggregator ID: 1
`

const bondingActiveBackupFixture = `Ethernet Channel Bonding Driver: v6.6.0

Bonding Mode: fault-tolerance (active-backup)
Primary Slave: None
Currently Active Slave: eno2
MII Status: up
MII Polling Interval (ms): 100
Up Delay (ms): 0
Down Delay (ms): 0

Slave Interface: eno1
MII Status: down
Speed: Unknown
Duplex: Unknown
Link Failure Count: 1
Permanent HW addr: bc:24:11:00:00:03

Slave Interface: eno2
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 0
Permanent HW addr: bc:24:11:00:00:04
`

// bondingLACPMatchedFixture is a golden 802.3ad /proc/net/bonding sample
// (T-804 AC1) with both slaves fully negotiated against the same partner
// system (matched ActorSystemID/PartnerSystemID/Key and port state 63 =
// 0b0011_1111: LACP_Activity|LACP_Timeout|Aggregation|Synchronization|
// Collecting|Distributing on every slave).
const bondingLACPMatchedFixture = `Ethernet Channel Bonding Driver: v6.6.0

Bonding Mode: IEEE 802.3ad Dynamic link aggregation
Transmit Hash Policy: layer3+4 (1)
MII Status: up
MII Polling Interval (ms): 100
Up Delay (ms): 0
Down Delay (ms): 0

802.3ad info
LACP rate: fast
Min links: 0
Aggregator selection policy (ad_select): stable
System priority: 65535
System MAC address: bc:24:11:00:00:0a
bond bond0 has a active aggregator

Slave Interface: eno1
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 0
Permanent HW addr: bc:24:11:00:00:01
Slave queue ID: 0
Aggregator ID: 1
Actor Churn State: none
Partner Churn State: none
Actor Churned Count: 0
Partner Churned Count: 0
details actor lacp pdu:
    system priority: 65535
    system mac address: bc:24:11:00:00:0a
    port key: 15
    port priority: 255
    port number: 1
    port state: 63
details partner lacp pdu:
    system priority: 32768
    system mac address: 3c:8c:40:aa:bb:cc
    oper key: 15
    port priority: 255
    port number: 1
    port state: 63

Slave Interface: eno2
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 0
Permanent HW addr: bc:24:11:00:00:02
Slave queue ID: 0
Aggregator ID: 1
Actor Churn State: none
Partner Churn State: none
Actor Churned Count: 0
Partner Churned Count: 0
details actor lacp pdu:
    system priority: 65535
    system mac address: bc:24:11:00:00:0a
    port key: 15
    port priority: 255
    port number: 2
    port state: 63
details partner lacp pdu:
    system priority: 32768
    system mac address: 3c:8c:40:aa:bb:cc
    oper key: 15
    port priority: 255
    port number: 2
    port state: 63
`

// bondingLACPDesyncFixture is the "second sample with a desynced slave"
// AC1 calls for: eno2 has fallen off the aggregator (its partner system
// disagrees with eno1's — a real split-brain shape — and its actor port
// state (0x07: LACP_Activity|LACP_Timeout|Aggregation only) is missing the
// synchronized/collecting/distributing bits eno1 still has).
const bondingLACPDesyncFixture = `Ethernet Channel Bonding Driver: v6.6.0

Bonding Mode: IEEE 802.3ad Dynamic link aggregation
Transmit Hash Policy: layer3+4 (1)
MII Status: up
MII Polling Interval (ms): 100
Up Delay (ms): 0
Down Delay (ms): 0

802.3ad info
LACP rate: fast
Min links: 0
Aggregator selection policy (ad_select): stable
System priority: 65535
System MAC address: bc:24:11:00:00:0a
bond bond0 has a active aggregator

Slave Interface: eno1
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 0
Permanent HW addr: bc:24:11:00:00:01
Slave queue ID: 0
Aggregator ID: 1
Actor Churn State: none
Partner Churn State: none
Actor Churned Count: 0
Partner Churned Count: 0
details actor lacp pdu:
    system priority: 65535
    system mac address: bc:24:11:00:00:0a
    port key: 15
    port priority: 255
    port number: 1
    port state: 63
details partner lacp pdu:
    system priority: 32768
    system mac address: 3c:8c:40:aa:bb:cc
    oper key: 15
    port priority: 255
    port number: 1
    port state: 63

Slave Interface: eno2
MII Status: up
Speed: 1000 Mbps
Duplex: full
Link Failure Count: 0
Permanent HW addr: bc:24:11:00:00:02
Slave queue ID: 0
Aggregator ID: 2
Actor Churn State: churned
Partner Churn State: churned
Actor Churned Count: 3
Partner Churned Count: 3
details actor lacp pdu:
    system priority: 65535
    system mac address: bc:24:11:00:00:0a
    port key: 16
    port priority: 255
    port number: 2
    port state: 7
details partner lacp pdu:
    system priority: 32768
    system mac address: aa:bb:cc:dd:ee:ff
    oper key: 99
    port priority: 255
    port number: 2
    port state: 7
`

func TestParseBondingProc_LACPActorPartnerMatched(t *testing.T) {
	bd := parseBondingProc([]byte(bondingLACPMatchedFixture))
	if len(bd.Slaves) != 2 {
		t.Fatalf("Slaves = %+v, want 2 entries", bd.Slaves)
	}
	for _, s := range bd.Slaves {
		if !s.LACPDetailSet {
			t.Errorf("slave %s: LACPDetailSet = false, want true", s.Name)
		}
		if s.ActorSystemID != "bc:24:11:00:00:0a" {
			t.Errorf("slave %s: ActorSystemID = %q, want bc:24:11:00:00:0a", s.Name, s.ActorSystemID)
		}
		if s.ActorSystemPriority != 65535 {
			t.Errorf("slave %s: ActorSystemPriority = %d, want 65535", s.Name, s.ActorSystemPriority)
		}
		if s.ActorKey != 15 {
			t.Errorf("slave %s: ActorKey = %d, want 15", s.Name, s.ActorKey)
		}
		if !s.ActorSynchronized || !s.ActorCollecting || !s.ActorDistributing {
			t.Errorf("slave %s: actor state = sync=%v collecting=%v distributing=%v, want all true (port state 63)",
				s.Name, s.ActorSynchronized, s.ActorCollecting, s.ActorDistributing)
		}
		if s.PartnerSystemID != "3c:8c:40:aa:bb:cc" {
			t.Errorf("slave %s: PartnerSystemID = %q, want 3c:8c:40:aa:bb:cc", s.Name, s.PartnerSystemID)
		}
		if s.PartnerSystemPriority != 32768 {
			t.Errorf("slave %s: PartnerSystemPriority = %d, want 32768", s.Name, s.PartnerSystemPriority)
		}
		if s.PartnerKey != 15 {
			t.Errorf("slave %s: PartnerKey = %d, want 15", s.Name, s.PartnerKey)
		}
	}
}

func TestParseBondingProc_LACPDesyncedSlave(t *testing.T) {
	bd := parseBondingProc([]byte(bondingLACPDesyncFixture))
	if len(bd.Slaves) != 2 {
		t.Fatalf("Slaves = %+v, want 2 entries", bd.Slaves)
	}

	eno1, eno2 := bd.Slaves[0], bd.Slaves[1]
	if eno1.Name != "eno1" || eno2.Name != "eno2" {
		t.Fatalf("unexpected slave order: %+v", bd.Slaves)
	}

	if !eno1.ActorSynchronized || !eno1.ActorCollecting || !eno1.ActorDistributing {
		t.Errorf("eno1 actor state = sync=%v collecting=%v distributing=%v, want all true",
			eno1.ActorSynchronized, eno1.ActorCollecting, eno1.ActorDistributing)
	}
	if eno1.PartnerSystemID != "3c:8c:40:aa:bb:cc" || eno1.PartnerKey != 15 {
		t.Errorf("eno1 partner = %q/%d, want 3c:8c:40:aa:bb:cc/15", eno1.PartnerSystemID, eno1.PartnerKey)
	}

	// eno2 fell off the aggregator: its actor state is missing
	// sync/collecting/distributing (port state 7), and it disagrees with
	// eno1 on the partner system — exactly the split-brain shape
	// lacp_partner_mismatch (T-804) must catch.
	if eno2.ActorSynchronized || eno2.ActorCollecting || eno2.ActorDistributing {
		t.Errorf("eno2 actor state = sync=%v collecting=%v distributing=%v, want all false (port state 7)",
			eno2.ActorSynchronized, eno2.ActorCollecting, eno2.ActorDistributing)
	}
	if eno2.PartnerSystemID != "aa:bb:cc:dd:ee:ff" || eno2.PartnerKey != 99 {
		t.Errorf("eno2 partner = %q/%d, want aa:bb:cc:dd:ee:ff/99", eno2.PartnerSystemID, eno2.PartnerKey)
	}
	if eno1.PartnerSystemID == eno2.PartnerSystemID {
		t.Errorf("eno1/eno2 partner system IDs should differ in this desync fixture, both = %q", eno1.PartnerSystemID)
	}
}

func TestParseBondingProc_LACP(t *testing.T) {
	bd := parseBondingProc([]byte(bonding8023adFixture))
	if bd.Mode != "802.3ad" {
		t.Errorf("Mode = %q, want 802.3ad", bd.Mode)
	}
	if bd.LACPRate != "fast" {
		t.Errorf("LACPRate = %q, want fast", bd.LACPRate)
	}
	if bd.XmitHashPolicy != "layer3+4" {
		t.Errorf("XmitHashPolicy = %q, want layer3+4", bd.XmitHashPolicy)
	}
	if bd.MIIStatus != "up" {
		t.Errorf("MIIStatus = %q, want up", bd.MIIStatus)
	}
	if len(bd.Slaves) != 2 {
		t.Fatalf("Slaves = %+v, want 2 entries", bd.Slaves)
	}
	if bd.Slaves[0].Name != "eno1" || bd.Slaves[0].PermHWAddr != "bc:24:11:00:00:01" {
		t.Errorf("Slaves[0] = %+v", bd.Slaves[0])
	}
	if bd.Slaves[1].LinkFailureCount != 2 {
		t.Errorf("Slaves[1].LinkFailureCount = %d, want 2", bd.Slaves[1].LinkFailureCount)
	}
	// No single "Currently Active Slave" line in LACP mode: both up
	// slaves should be treated as active.
	if !bd.Slaves[0].Active || !bd.Slaves[1].Active {
		t.Errorf("Slaves = %+v, want both active in 802.3ad mode", bd.Slaves)
	}
}

func TestParseBondingProc_ActiveBackup(t *testing.T) {
	bd := parseBondingProc([]byte(bondingActiveBackupFixture))
	if bd.Mode != "active-backup" {
		t.Errorf("Mode = %q, want active-backup", bd.Mode)
	}
	if bd.ActiveSlave != "eno2" {
		t.Errorf("ActiveSlave = %q, want eno2", bd.ActiveSlave)
	}
	if len(bd.Slaves) != 2 {
		t.Fatalf("Slaves = %+v, want 2 entries", bd.Slaves)
	}
	if bd.Slaves[0].Active {
		t.Errorf("Slaves[0] (eno1) should not be active")
	}
	if !bd.Slaves[1].Active {
		t.Errorf("Slaves[1] (eno2) should be active")
	}
	if bd.Slaves[0].MIIStatus != "down" {
		t.Errorf("Slaves[0].MIIStatus = %q, want down", bd.Slaves[0].MIIStatus)
	}
}
