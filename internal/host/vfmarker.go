// vfmarker.go backs T-1506's SR-IOV VF provisioning: the shared plan
// resolution (internal/change's schema/referential validators and
// internal/change/ifaces' file mutator must agree on exactly which VFs a
// given vf.provision op configures) and the post-up/post-down shell command
// rendering for the ordinary node-file changeset op path — a vf.provision
// op is applied exactly like a bond.create or a T-1506-era nat.* rule would
// be: a post-up/post-down stanza pair appended to an *existing* iface
// stanza (the PF's own), through NodeAgent's existing stage/reload path.
// There is no second mutation mechanism and no shadow store: the interfaces
// file is the only record of a vf.provision op's rendered commands (the
// VFs themselves, once actually configured on real hardware, are then
// independently observable via host-netlink — see internal/inventory's
// PhysNic.SRIOVVFs).

package host

import (
	"fmt"
	"strconv"
	"strings"
)

// vfMarkerPrefix tags every post-up/post-down line vf.provision generates,
// mirroring the "trailing shell comment names the vnprox-owned line"
// convention this package's other generated-line families use. Unlike a
// nat/route rule (whose *only* record is its generated line, decoded back
// apart on every read), a VF's state is independently observable via
// host-netlink once real hardware applies it, so this marker is
// documentation/provenance only — nothing in this codebase decodes it back
// apart.
const vfMarkerPrefix = "# vnprox-vf:"

// VFSpec is one caller-supplied, not-yet-defaulted virtual function
// override within a vf.provision op's explicit VF list — the wire
// counterpart of internal/change's VFSpec (JSON field names must match
// exactly; see ResolveVFPlan's doc comment for why the two packages carry
// independent-but-identical-shaped types rather than sharing one).
type VFSpec struct {
	SpoofCheck *bool  `json:"spoofCheck,omitempty"`
	Trust      *bool  `json:"trust,omitempty"`
	MacAddr    string `json:"macAddr,omitempty"`
	ID         int    `json:"id"`
	VLAN       int    `json:"vlan,omitempty"`
}

// VFEntry is one fully-resolved (every default applied) VF configuration,
// ResolveVFPlan's output — what actually gets rendered into shell commands.
type VFEntry struct {
	MacAddr    string
	ID         int
	VLAN       int
	SpoofCheck bool
	Trust      bool
}

// ResolveVFPlan expands a vf.provision op's Count/VFs + top-level defaults
// into a concrete, fully-resolved VF list, one VFEntry per VF the op
// configures. Exactly one of count/vfs is expected to be set (schema-
// validated by internal/change before this is ever called; a caller that
// passes both simply gets the explicit vfs list, ignoring count) — see
// internal/change/validate_schema.go's vf.provision case.
//
// Defaults: spoofCheck defaults to true (the common SR-IOV secure-default,
// matching real ip-link's own "spoofchk on" default for a newly-created
// VF) and trust defaults to false, both overridable per-VF (explicit list)
// or once for the whole batch (count mode's top-level spoofCheck/trust).
// macAddr in count mode is only ever applied when count==1 — assigning the
// same MAC to more than one VF would create a duplicate-MAC network fault
// that real driver validation would itself reject, so this function simply
// never renders it for count>1 rather than producing a plan a later apply
// step would fail on.
func ResolveVFPlan(count int, vfs []VFSpec, vlan int, macAddr string, spoofCheck, trust *bool) []VFEntry {
	defSpoof := true
	if spoofCheck != nil {
		defSpoof = *spoofCheck
	}
	defTrust := false
	if trust != nil {
		defTrust = *trust
	}

	if len(vfs) > 0 {
		out := make([]VFEntry, len(vfs))
		for i, v := range vfs {
			e := VFEntry{ID: v.ID, MacAddr: v.MacAddr, VLAN: v.VLAN, SpoofCheck: defSpoof, Trust: defTrust}
			if e.VLAN == 0 {
				e.VLAN = vlan
			}
			if e.MacAddr == "" {
				e.MacAddr = macAddr
			}
			if v.SpoofCheck != nil {
				e.SpoofCheck = *v.SpoofCheck
			}
			if v.Trust != nil {
				e.Trust = *v.Trust
			}
			out[i] = e
		}
		return out
	}

	out := make([]VFEntry, count)
	for i := 0; i < count; i++ {
		mac := ""
		if count == 1 {
			mac = macAddr
		}
		out[i] = VFEntry{ID: i, MacAddr: mac, VLAN: vlan, SpoofCheck: defSpoof, Trust: defTrust}
	}
	return out
}

// onOff renders a bool as `ip link set`'s own "on"/"off" vocabulary.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// VFProvisionCommands renders pf's vf.provision commands: one post-up/
// post-down pair setting the PF's total VF pool size via sysfs
// sriov_numvfs (needs-hardware-validation: the kernel re-numbers/tears down
// existing VFs when this is rewritten, real driver behavior this package
// cannot exercise against a mock — see
// planning/reports/needs-hardware-validation.md), followed by one
// post-up/post-down pair per resolved VFEntry applying its VLAN/MAC/
// spoof-check/trust via `ip link set`. post-down commands reverse each
// setting to its hardware/driver default — vnprox's own changeset rollback
// restores the pre-apply interfaces file wholesale and never actually
// executes these post-down lines itself (docs/architecture.md §4's
// rollback contract); they exist for the same documentation/manual-
// teardown value every other post-down line in this package carries.
func VFProvisionCommands(pf string, count int, vfs []VFEntry) (ups, downs []string) {
	marker := vfMarkerPrefix + pf
	ups = append(ups, fmt.Sprintf("echo %d > /sys/class/net/%s/device/sriov_numvfs %s", count, pf, marker))
	downs = append(downs, fmt.Sprintf("echo 0 > /sys/class/net/%s/device/sriov_numvfs %s", pf, marker))

	for _, vf := range vfs {
		up := []string{"ip", "link", "set", pf, "vf", strconv.Itoa(vf.ID)}
		down := []string{"ip", "link", "set", pf, "vf", strconv.Itoa(vf.ID)}
		if vf.MacAddr != "" {
			up = append(up, "mac", vf.MacAddr)
		}
		if vf.VLAN != 0 {
			up = append(up, "vlan", strconv.Itoa(vf.VLAN))
			down = append(down, "vlan", "0")
		}
		up = append(up, "spoofchk", onOff(vf.SpoofCheck))
		down = append(down, "spoofchk", "on")
		up = append(up, "trust", onOff(vf.Trust))
		down = append(down, "trust", "off")
		ups = append(ups, strings.Join(up, " ")+" "+marker+"/vf"+strconv.Itoa(vf.ID))
		downs = append(downs, strings.Join(down, " ")+" "+marker+"/vf"+strconv.Itoa(vf.ID))
	}
	return ups, downs
}
