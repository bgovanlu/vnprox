//go:build linux

package host

import (
	"reflect"
	"testing"

	"github.com/vishvananda/netlink/nl"
)

func TestVlanSpans_RangeAndSingles(t *testing.T) {
	entries := []*nl.BridgeVlanInfo{
		{Vid: 1, Flags: nl.BRIDGE_VLAN_INFO_PVID | nl.BRIDGE_VLAN_INFO_UNTAGGED},
		{Vid: 2, Flags: nl.BRIDGE_VLAN_INFO_RANGE_BEGIN},
		{Vid: 4094, Flags: nl.BRIDGE_VLAN_INFO_RANGE_END},
		{Vid: 100, Flags: 0},
	}

	spans := vlanSpans(entries)
	want := []vlanSpan{
		{Range: VidRange{Low: 1, High: 1}, Flags: nl.BRIDGE_VLAN_INFO_PVID | nl.BRIDGE_VLAN_INFO_UNTAGGED},
		{Range: VidRange{Low: 2, High: 4094}, Flags: nl.BRIDGE_VLAN_INFO_RANGE_BEGIN | nl.BRIDGE_VLAN_INFO_RANGE_END},
		{Range: VidRange{Low: 100, High: 100}, Flags: 0},
	}
	if !reflect.DeepEqual(spans, want) {
		t.Fatalf("vlanSpans() = %+v, want %+v", spans, want)
	}

	self := selfVlanRanges(entries)
	wantSelf := []VidRange{{1, 1}, {2, 4094}, {100, 100}}
	if !reflect.DeepEqual(self, wantSelf) {
		t.Errorf("selfVlanRanges() = %v, want %v", self, wantSelf)
	}

	pvs := portVlans(entries)
	if len(pvs) != 3 {
		t.Fatalf("portVlans() = %+v, want 3 entries", pvs)
	}
	if !pvs[0].PVID || !pvs[0].Untagged {
		t.Errorf("pvs[0] = %+v, want PVID+Untagged", pvs[0])
	}
	if pvs[1].Vids != (VidRange{Low: 2, High: 4094}) {
		t.Errorf("pvs[1].Vids = %+v, want {2 4094}", pvs[1].Vids)
	}
	if pvs[1].PVID || pvs[1].Untagged {
		t.Errorf("pvs[1] = %+v, want neither PVID nor Untagged", pvs[1])
	}
}

func TestVlanSpans_Empty(t *testing.T) {
	if spans := vlanSpans(nil); spans != nil {
		t.Errorf("vlanSpans(nil) = %v, want nil", spans)
	}
	if r := selfVlanRanges(nil); r != nil {
		t.Errorf("selfVlanRanges(nil) = %v, want nil", r)
	}
	if p := portVlans(nil); p != nil {
		t.Errorf("portVlans(nil) = %v, want nil", p)
	}
}
