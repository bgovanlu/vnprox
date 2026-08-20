package inventory

import "fmt"

// Source identifies which collector produced a set of entities. Ownership of
// each entity field is assigned per Source (see ownershipRules); a poll from
// one Source never clobbers a field another Source owns.
type Source string

const (
	// SourceHostNetlink is the local host's netlink/procfs runtime view
	// (internal/host Links): what the kernel is actually running right now —
	// link up/down, speed, active bond slaves, programmed bridge VLAN table.
	SourceHostNetlink Source = "host-netlink"
	// SourceHostInterfaces is the parsed /etc/network/interfaces file
	// (internal/host ParseInterfaces): declared/intended config.
	SourceHostInterfaces Source = "host-interfaces"
	// SourceHostLLDP is lldpctl neighbor data (internal/host LLDP).
	SourceHostLLDP Source = "host-lldp"
	// SourcePVENetwork is PVE's GET /nodes/{node}/network: PVE's own parse of
	// the same interfaces file. It is a cross-check on the declared config,
	// never the authority when the host file itself is available.
	SourcePVENetwork Source = "pve-network"
	// SourcePVESDN is PVE's cluster SDN tree (zones/vnets/subnets/status).
	SourcePVESDN Source = "pve-sdn"
	// SourcePVEGuest is PVE guest configs (VM/CT NICs).
	SourcePVEGuest Source = "pve-guest"
	// SourcePVEFirewall is PVE firewall rulesets at any scope.
	SourcePVEFirewall Source = "pve-firewall"
	// SourcePVECluster is PVE /cluster/status (node membership).
	SourcePVECluster Source = "pve-cluster"
)

// Ownership is the merge rule for one field of one entity kind.
//
// Precedence lists the sources that may set the field, most authoritative
// first. The resolved value is taken from the highest-precedence source that
// actually reported the field this poll. When FlagConflict is true, any
// lower-precedence source that reported a *different* value is recorded as a
// provenance conflict rather than silently discarded — so the UI/drift
// checker can surface "PVE says MTU 1500, the interfaces file says 9000"
// instead of last-write-wins hiding the disagreement.
type Ownership struct {
	Precedence   []Source
	FlagConflict bool
}

// ownershipRules is the authoritative per-field merge table for the four
// entity kinds that multiple collectors observe (physical NIC, bond,
// bridge, VLAN interface). Reviewers: this is the contract for acceptance
// criterion #2. Every field of these kinds appears exactly once.
//
// The guiding principle:
//   - RUNTIME facts (link state, speed, active slaves, programmed VLAN
//     table, effective MTU) are owned by host-netlink alone — only the
//     kernel knows what is actually running. No conflict flag: there is a
//     single runtime source.
//   - DECLARED facts (intended MTU, addresses, comments, configured
//     ports/slaves, autostart, gateway) are owned by host-interfaces (the
//     file is the source of truth) with pve-network as a cross-check;
//     disagreement flags a conflict (PVE parsed the file differently, or the
//     node's file drifted from the cluster's expectation).
//   - Fields that are simultaneously a config knob and a running attribute
//     (mode, vlanAware, stp, vids) are owned runtime-first (host-netlink),
//     with the declared sources as flagged cross-checks so config-vs-running
//     drift is visible.
//   - MTU is split into two fields on purpose: `mtu` (runtime, netlink) and
//     `mtuDeclared` (intent, interfaces/pve). They are exposed separately
//     rather than merged, exactly as the task requires.
//   - BOOLEAN fields (vlanAware, stp, linkUp) are optional: a source's
//     contribution only counts as "reported" when its companion *Set flag
//     (Bridge.VlanAwareSet, Bridge.STPSet, PhysNic.LinkUpSet) is true.
//     A partial that leaves the flag unset is treated exactly like a
//     source that never mentioned the field — it neither wins the merge
//     nor registers a provenance conflict — so e.g. pve-network (which
//     carries no STP information at all) cannot spuriously "disagree"
//     with the kernel's stp value by implicitly reporting false. On the
//     resolved entity the *Set flag is true iff any precedence source
//     reported the field; when none did, the field resolves to the zero
//     value, carries no provenance entry, and its *Set flag is false.
//
// Fields whose zero value already means "not reported" (empty strings,
// zero ints, empty slices) need no companion flag; the isSet predicates
// below (nonEmptyStr, nonZeroInt, nonEmptySlc) encode that.
var ownershipRules = map[Kind]map[string]Ownership{
	KindPhysNic: {
		"name":      {Precedence: []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork}},
		"mac":       {Precedence: []Source{SourceHostNetlink}},
		"driver":    {Precedence: []Source{SourceHostNetlink}},
		"pciAddr":   {Precedence: []Source{SourceHostNetlink}},
		"duplex":    {Precedence: []Source{SourceHostNetlink}},
		"mediaPort": {Precedence: []Source{SourceHostNetlink}},
		"operState": {Precedence: []Source{SourceHostNetlink}},
		"speedMbps": {Precedence: []Source{SourceHostNetlink}},
		// sriovVFs (T-1506) is deliberately absent here, like Bridge's "fdb"
		// — host-netlink-only with a single contributing source, copied
		// straight through in resolvePhysNic rather than merged via pick.
		"linkUp": {Precedence: []Source{SourceHostNetlink}},
		"mtu":    {Precedence: []Source{SourceHostNetlink}},
		"mtuDeclared": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		// pending is exclusively pve-network's own staging concept (PVE's
		// GET /nodes/{node}/network annotates the response with "pending"
		// when interfaces.new differs from the live file) — no other source
		// ever reports it, so there is nothing to conflict-flag against.
		"pending": {Precedence: []Source{SourcePVENetwork}},
	},
	KindBond: {
		"name":        {Precedence: []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork}},
		"miiStatus":   {Precedence: []Source{SourceHostNetlink}},
		"activeSlave": {Precedence: []Source{SourceHostNetlink}},
		"slaveDetail": {Precedence: []Source{SourceHostNetlink}},
		"slaves":      {Precedence: []Source{SourceHostNetlink}},
		"mtu":         {Precedence: []Source{SourceHostNetlink}},
		"mode": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"lacpRate": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"xmitHashPolicy": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"declaredSlaves": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"mtuDeclared": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"pending": {Precedence: []Source{SourcePVENetwork}},
	},
	KindBridge: {
		"name":      {Precedence: []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork}},
		"virt":      {Precedence: []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork}},
		"portNames": {Precedence: []Source{SourceHostNetlink}},
		"mtu":       {Precedence: []Source{SourceHostNetlink}},
		"vlanAware": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"vids": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"stp": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"declaredPortNames": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"addresses": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork, SourceHostNetlink},
			FlagConflict: true,
		},
		"gateway": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"comments": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"mtuDeclared": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"pending": {Precedence: []Source{SourcePVENetwork}},
	},
	KindVlan: {
		"name":       {Precedence: []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork}},
		"parentName": {Precedence: []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork}},
		"mtu":        {Precedence: []Source{SourceHostNetlink}},
		"vid": {
			Precedence:   []Source{SourceHostNetlink, SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"addresses": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork, SourceHostNetlink},
			FlagConflict: true,
		},
		"mtuDeclared": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		"pending": {Precedence: []Source{SourcePVENetwork}},
		// virt (T-407) distinguishes a plain 802.1q VLAN sub-interface from
		// an OVS Int Port, mirroring Bridge.virt's precedence exactly (both
		// declared sources agree in practice; flagged in case they don't).
		"virt": {
			Precedence:   []Source{SourceHostInterfaces, SourcePVENetwork},
			FlagConflict: true,
		},
		// trunks (T-407) is OVS-only and only ever declared by the
		// interfaces file today (pve-network's adapter carries no trunks
		// concept — see FromPVENetwork's OVSIntPort case) — no conflict to
		// flag against a source that never reports it.
		"trunks": {Precedence: []Source{SourceHostInterfaces}},
	},
}

// singleSourceOwner names the sole authoritative source for entity kinds
// that only one collector ever produces. Their resolution is a passthrough
// (no field merge), but provenance still records the owning source so the
// UI can show where the data came from and the API stays uniform.
var singleSourceOwner = map[Kind]Source{
	KindSDNZone:      SourcePVESDN,
	KindSDNVnet:      SourcePVESDN,
	KindSDNSubnet:    SourcePVESDN,
	KindSDNDnsZone:   SourcePVESDN,
	KindSDNDnsRecord: SourcePVESDN,
	KindGuest:        SourcePVEGuest,
	KindGuestNic:     SourcePVEGuest,
	KindLldpNeighbor: SourceHostLLDP,
	KindFwRuleset:    SourcePVEFirewall,
	KindNode:         SourcePVECluster,
}

// multiSourceKinds is the set of kinds resolved through ownershipRules.
var multiSourceKinds = map[Kind]bool{
	KindPhysNic: true, KindBond: true, KindBridge: true, KindVlan: true,
	KindOVSBridge: true, KindOVSBond: true,
}

// --- provenance ----------------------------------------------------------

// SourceValue is a (source, stringified value) pair recording a dissenting
// report during merge.
type SourceValue struct {
	Source Source
	Value  string
}

// FieldProv is the provenance of one resolved field: which source's value
// won, and any conflicting values other sources reported.
type FieldProv struct {
	Owner     Source
	Conflicts []SourceValue
}

// Provenance records, per resolved field, which source won and what other
// sources disagreed. It is attached to every resolved entity so drift
// detection and the inspector can show multi-source disagreement instead of
// a silently-picked value.
type Provenance struct {
	Fields map[string]FieldProv
}

func newProvenance() *Provenance { return &Provenance{Fields: map[string]FieldProv{}} }

func (p *Provenance) set(field string, owner Source, conflicts []SourceValue) {
	p.Fields[field] = FieldProv{Owner: owner, Conflicts: conflicts}
}

// HasConflicts reports whether any resolved field carries a cross-source
// disagreement.
func (p Provenance) HasConflicts() bool {
	for _, fp := range p.Fields {
		if len(fp.Conflicts) > 0 {
			return true
		}
	}
	return false
}

// --- merge helpers -------------------------------------------------------

// pick is the merge core: it walks a field's precedence, takes the value
// from the first source that reported it (per isSet), records provenance,
// and flags conflicts. key canonicalizes a value for equality/conflict
// display.
func pick[E, V any](
	prov *Provenance, field string, rule Ownership,
	parts map[Source]*E, ex func(*E) V, isSet func(V) bool, key func(V) string,
) (V, bool) {
	var zero V
	type rep struct {
		src Source
		val V
		k   string
	}
	var reps []rep
	for _, src := range rule.Precedence {
		p, ok := parts[src]
		if !ok {
			continue
		}
		v := ex(p)
		if !isSet(v) {
			continue
		}
		reps = append(reps, rep{src, v, key(v)})
	}
	if len(reps) == 0 {
		return zero, false
	}
	winner := reps[0]
	var conflicts []SourceValue
	if rule.FlagConflict {
		for _, r := range reps[1:] {
			if r.k != winner.k {
				conflicts = append(conflicts, SourceValue{Source: r.src, Value: r.k})
			}
		}
	}
	prov.set(field, winner.src, conflicts)
	return winner.val, true
}

func ruleFor(kind Kind, field string) Ownership {
	// OVS variants reuse the linux bridge/bond rule tables.
	switch kind {
	case KindOVSBridge:
		kind = KindBridge
	case KindOVSBond:
		kind = KindBond
	}
	r, ok := ownershipRules[kind][field]
	if !ok {
		panic(fmt.Sprintf("inventory: no ownership rule for %s.%s", kind, field))
	}
	return r
}

func nonEmptyStr(s string) bool     { return s != "" }
func nonZeroInt(i int) bool         { return i != 0 }
func nonEmptySlc[T any](s []T) bool { return len(s) > 0 }

func keyStr(s string) string { return s }
func keyInt(i int) string    { return fmt.Sprint(i) }

// boolOpt pairs an optional boolean field's value with its "was actually
// reported" flag (the entity's companion *Set field), so pick can
// distinguish a genuine false report from a field the source never set.
type boolOpt struct{ v, set bool }

func boolOptSet(o boolOpt) bool   { return o.set }
func boolOptKey(o boolOpt) string { return boolStr(o.v) }
