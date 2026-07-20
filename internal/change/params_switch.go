package change

// params_switch.go defines T-1205's switch.port.update op parameters. Every
// switch.port.* op flows through the ordinary
// stage→validate→diff→apply→confirm/rollback changeset lifecycle — there is
// deliberately no second mutation path for a switch (CLAUDE.md's change-engine
// invariant). The op is scoped to exactly three attribute groups (VLAN
// membership, description, LACP) and no other port operation is expressible.
//
// Target Ref: a switch-port Ref {Node: "", ID: "<switchID>/<port>"} — the
// app-store switch id plus the driver-native port name (docs/data-model.md §3).

// SwitchNeighbor is the LLDP-observed identity of the PVE node facing a switch
// port, recorded when the port was scoped as PVE-facing. The switch driver
// re-reads the live neighbor immediately before every write and hard-aborts on
// mismatch (T-1205 AC4) — this field is what "the last-known PVE-node neighbor"
// is compared against, carried in the op so the check is self-contained and
// cannot be bypassed by any op that omits it (an empty ExpectNeighbor fails the
// check closed).
type SwitchNeighbor struct {
	ChassisID string `json:"chassisId"`
	PortID    string `json:"portId,omitempty"`
}

// SwitchPortUpdateParams is op "switch.port.update". Pointer fields are set
// only for the attributes being changed (nil == leave unchanged), the same
// partial-update convention BondUpdateParams/WgTunnelUpdateParams use. Untagged
// is the port's native/PVID VLAN (0 for none); Tagged is the full trunk VID set
// the op sets (a non-nil empty slice means "no tagged VLANs"). LacpMode is one
// of "off"|"active"|"passive"; LacpRate is "slow"|"fast".
type SwitchPortUpdateParams struct {
	Untagged       *int           `json:"untagged,omitempty"`
	Tagged         *[]int         `json:"tagged,omitempty"`
	Description    *string        `json:"description,omitempty"`
	LacpMode       *string        `json:"lacpMode,omitempty"`
	LacpRate       *string        `json:"lacpRate,omitempty"`
	ExpectNeighbor SwitchNeighbor `json:"expectNeighbor"`
}

func (SwitchPortUpdateParams) isChangeParams() {}

// setsVLANMembership reports whether the op explicitly sets the port's VLAN
// membership (either the untagged/PVID or the tagged trunk set). An op that
// changes only description/LACP leaves VLAN membership untouched — it can never
// strip a management VLAN, so protectedSwitchPortFindings ignores it.
func (p SwitchPortUpdateParams) setsVLANMembership() bool {
	return p.Untagged != nil || p.Tagged != nil
}

// carriesVLAN reports whether the op's net-effect VLAN membership includes vid
// — as the untagged/PVID VLAN or a member of the tagged trunk set. Only
// meaningful when setsVLANMembership() is true (an op that sets neither field
// is not changing membership at all).
func (p SwitchPortUpdateParams) carriesVLAN(vid int) bool {
	if p.Untagged != nil && *p.Untagged == vid {
		return true
	}
	if p.Tagged != nil {
		for _, v := range *p.Tagged {
			if v == vid {
				return true
			}
		}
	}
	return false
}
