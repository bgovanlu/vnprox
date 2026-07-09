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
