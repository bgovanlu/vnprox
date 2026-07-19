package change

// params_vf.go defines T-1506's SR-IOV VF provisioning op params. vf.* is
// an ordinary changeset op group — stage -> validate -> diff -> apply ->
// confirm/rollback exactly like every other mutation (CLAUDE.md's change-
// engine invariant) — applied through the existing node-file path
// (internal/change/ifaces, a post-up/post-down stanza on the PF's own
// existing iface stanza, docs/architecture.md §4), never a second mutation
// mechanism.

// VFProvisionParams is op "vf.provision". Target names the PF (a PhysNic
// Ref) whose VF pool is being configured. Exactly one of Count/VFs is set:
//   - Count provisions Count freshly-numbered VFs (ids 0..Count-1), sharing
//     VLAN/SpoofCheck/Trust; MacAddr is only applied when Count == 1 (a MAC
//     shared across more than one VF would be a duplicate-MAC fault a real
//     driver would itself reject, so a schema check rejects MacAddr set
//     with Count > 1 rather than silently dropping it — see
//     validate_schema.go).
//   - VFs gives each VF its own id/overrides (a VFSpec entry's own
//     Vlan/MacAddr/SpoofCheck/Trust take precedence over the corresponding
//     top-level default for that one VF; an unset VFSpec field falls back
//     to the top-level default).
//
// SpoofCheck/Trust are tri-state: nil means "use the driver/SR-IOV secure
// default" (spoof-check on, trust off — internal/host.ResolveVFPlan is the
// single place both this package's validation and internal/change/ifaces'
// file mutator resolve that default from, so they can never disagree).
type VFProvisionParams struct {
	SpoofCheck *bool    `json:"spoofCheck,omitempty"`
	Trust      *bool    `json:"trust,omitempty"`
	MacAddr    string   `json:"macAddr,omitempty"`
	VFs        []VFSpec `json:"vfs,omitempty"`
	VLAN       int      `json:"vlan,omitempty"`
	Count      int      `json:"count,omitempty"`
}

func (VFProvisionParams) isChangeParams() {}

// VFSpec is one caller-supplied, explicit VF override within a
// vf.provision op's VFs list — the wire counterpart of
// internal/host.VFSpec (JSON field names must match exactly for the
// change.Op -> ifaces.Op re-marshal adapter, changeOpsToIfaces, to carry
// the list through losslessly; kept as this package's own type rather than
// importing internal/host directly into a wire-params file, mirroring how
// this package's own VidRange is the wire counterpart of
// internal/inventory.VidRange elsewhere in params_common.go).
type VFSpec struct {
	SpoofCheck *bool  `json:"spoofCheck,omitempty"`
	Trust      *bool  `json:"trust,omitempty"`
	MacAddr    string `json:"macAddr,omitempty"`
	ID         int    `json:"id"`
	VLAN       int    `json:"vlan,omitempty"`
}
