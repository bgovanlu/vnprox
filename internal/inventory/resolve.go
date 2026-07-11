package inventory

// resolved bundles a merged entity with its provenance.
type resolved struct {
	entity Entity
	prov   Provenance
}

// resolveEntity folds every source's contribution for one Ref into a single
// resolved entity plus provenance. Multi-source kinds go through the
// ownership table; single-source kinds are a clone-through with a
// provenance stamp naming the owning source.
func resolveEntity(ref Ref, parts map[Source]Entity) resolved {
	if multiSourceKinds[ref.Kind] {
		switch ref.Kind {
		case KindBond, KindOVSBond:
			out := map[Source]*Bond{}
			for s, e := range parts {
				if p, ok := e.(*Bond); ok {
					out[s] = p
				}
			}
			return resolveBond(ref, out)
		case KindBridge, KindOVSBridge:
			out := map[Source]*Bridge{}
			for s, e := range parts {
				if p, ok := e.(*Bridge); ok {
					out[s] = p
				}
			}
			return resolveBridge(ref, out)
		case KindVlan:
			out := map[Source]*VlanIface{}
			for s, e := range parts {
				if p, ok := e.(*VlanIface); ok {
					out[s] = p
				}
			}
			return resolveVlan(ref, out)
		default: // KindPhysNic
			out := map[Source]*PhysNic{}
			for s, e := range parts {
				if p, ok := e.(*PhysNic); ok {
					out[s] = p
				}
			}
			return resolvePhysNic(ref, out)
		}
	}
	return resolveSingle(ref, parts)
}

// resolveSingle handles kinds produced by exactly one source. If more than
// one source somehow contributed, the designated owner wins; otherwise the
// sole contribution is used. Provenance stamps every field with the owner.
func resolveSingle(ref Ref, parts map[Source]Entity) resolved {
	owner := singleSourceOwner[ref.Kind]
	chosen, chosenSrc := (Entity)(nil), owner
	if e, ok := parts[owner]; ok {
		chosen = e
	} else {
		for src, e := range parts {
			chosen, chosenSrc = e, src
			break
		}
	}
	prov := newProvenance()
	if chosen != nil {
		for f := range chosen.fieldMap() {
			prov.set(f, chosenSrc, nil)
		}
		chosen = chosen.clone()
	}
	return resolved{entity: chosen, prov: *prov}
}

func resolvePhysNic(ref Ref, parts map[Source]*PhysNic) resolved {
	out := &PhysNic{Ref: ref}
	prov := newProvenance()
	out.Name, _ = pick(prov, "name", ruleFor(ref.Kind, "name"), parts, func(p *PhysNic) string { return p.Name }, nonEmptyStr, keyStr)
	out.Mac, _ = pick(prov, "mac", ruleFor(ref.Kind, "mac"), parts, func(p *PhysNic) string { return p.Mac }, nonEmptyStr, keyStr)
	out.Driver, _ = pick(prov, "driver", ruleFor(ref.Kind, "driver"), parts, func(p *PhysNic) string { return p.Driver }, nonEmptyStr, keyStr)
	out.PCIAddr, _ = pick(prov, "pciAddr", ruleFor(ref.Kind, "pciAddr"), parts, func(p *PhysNic) string { return p.PCIAddr }, nonEmptyStr, keyStr)
	out.Duplex, _ = pick(prov, "duplex", ruleFor(ref.Kind, "duplex"), parts, func(p *PhysNic) string { return p.Duplex }, nonEmptyStr, keyStr)
	out.OperState, _ = pick(prov, "operState", ruleFor(ref.Kind, "operState"), parts, func(p *PhysNic) string { return p.OperState }, nonEmptyStr, keyStr)
	out.SpeedMbps, _ = pick(prov, "speedMbps", ruleFor(ref.Kind, "speedMbps"), parts, func(p *PhysNic) int { return p.SpeedMbps }, nonZeroInt, keyInt)
	out.SRIOVVFs, _ = pick(prov, "sriovVFs", ruleFor(ref.Kind, "sriovVFs"), parts, func(p *PhysNic) int { return p.SRIOVVFs }, nonZeroInt, keyInt)
	out.MTU, _ = pick(prov, "mtu", ruleFor(ref.Kind, "mtu"), parts, func(p *PhysNic) int { return p.MTU }, nonZeroInt, keyInt)
	out.MTUDeclared, _ = pick(prov, "mtuDeclared", ruleFor(ref.Kind, "mtuDeclared"), parts, func(p *PhysNic) int { return p.MTUDeclared }, nonZeroInt, keyInt)
	linkUp, linkUpOK := pick(prov, "linkUp", ruleFor(ref.Kind, "linkUp"), parts, func(p *PhysNic) boolOpt { return boolOpt{p.LinkUp, p.LinkUpSet} }, boolOptSet, boolOptKey)
	out.LinkUp, out.LinkUpSet = linkUp.v, linkUpOK
	out.Pending, _ = pick(prov, "pending", ruleFor(ref.Kind, "pending"), parts, func(p *PhysNic) string { return p.Pending }, nonEmptyStr, keyStr)
	return resolved{entity: out, prov: *prov}
}

func resolveBond(ref Ref, parts map[Source]*Bond) resolved {
	out := &Bond{Ref: ref}
	prov := newProvenance()
	out.Name, _ = pick(prov, "name", ruleFor(ref.Kind, "name"), parts, func(b *Bond) string { return b.Name }, nonEmptyStr, keyStr)
	out.Mode, _ = pick(prov, "mode", ruleFor(ref.Kind, "mode"), parts, func(b *Bond) string { return b.Mode }, nonEmptyStr, keyStr)
	out.LACPRate, _ = pick(prov, "lacpRate", ruleFor(ref.Kind, "lacpRate"), parts, func(b *Bond) string { return b.LACPRate }, nonEmptyStr, keyStr)
	out.XmitHashPolicy, _ = pick(prov, "xmitHashPolicy", ruleFor(ref.Kind, "xmitHashPolicy"), parts, func(b *Bond) string { return b.XmitHashPolicy }, nonEmptyStr, keyStr)
	out.MIIStatus, _ = pick(prov, "miiStatus", ruleFor(ref.Kind, "miiStatus"), parts, func(b *Bond) string { return b.MIIStatus }, nonEmptyStr, keyStr)
	out.ActiveSlave, _ = pick(prov, "activeSlave", ruleFor(ref.Kind, "activeSlave"), parts, func(b *Bond) string { return b.ActiveSlave }, nonEmptyStr, keyStr)
	out.MTU, _ = pick(prov, "mtu", ruleFor(ref.Kind, "mtu"), parts, func(b *Bond) int { return b.MTU }, nonZeroInt, keyInt)
	out.MTUDeclared, _ = pick(prov, "mtuDeclared", ruleFor(ref.Kind, "mtuDeclared"), parts, func(b *Bond) int { return b.MTUDeclared }, nonZeroInt, keyInt)
	out.Slaves, _ = pick(prov, "slaves", ruleFor(ref.Kind, "slaves"), parts, func(b *Bond) []string { return b.Slaves }, nonEmptySlc[string], sortedJoin)
	out.DeclaredSlaves, _ = pick(prov, "declaredSlaves", ruleFor(ref.Kind, "declaredSlaves"), parts, func(b *Bond) []string { return b.DeclaredSlaves }, nonEmptySlc[string], sortedJoin)
	out.SlaveDetail, _ = pick(prov, "slaveDetail", ruleFor(ref.Kind, "slaveDetail"), parts, func(b *Bond) []BondSlaveState { return b.SlaveDetail }, nonEmptySlc[BondSlaveState], slaveDetailKey)
	out.Pending, _ = pick(prov, "pending", ruleFor(ref.Kind, "pending"), parts, func(b *Bond) string { return b.Pending }, nonEmptyStr, keyStr)
	return resolved{entity: out, prov: *prov}
}

func resolveBridge(ref Ref, parts map[Source]*Bridge) resolved {
	out := &Bridge{Ref: ref}
	prov := newProvenance()
	out.Name, _ = pick(prov, "name", ruleFor(ref.Kind, "name"), parts, func(b *Bridge) string { return b.Name }, nonEmptyStr, keyStr)
	virt, ok := pick(prov, "virt", ruleFor(ref.Kind, "virt"), parts, func(b *Bridge) BridgeVirt { return b.Virt }, func(v BridgeVirt) bool { return v != "" }, func(v BridgeVirt) string { return string(v) })
	if ok {
		out.Virt = virt
	}
	out.PortNames, _ = pick(prov, "portNames", ruleFor(ref.Kind, "portNames"), parts, func(b *Bridge) []string { return b.PortNames }, nonEmptySlc[string], sortedJoin)
	out.DeclaredPortNames, _ = pick(prov, "declaredPortNames", ruleFor(ref.Kind, "declaredPortNames"), parts, func(b *Bridge) []string { return b.DeclaredPortNames }, nonEmptySlc[string], sortedJoin)
	out.Vids, _ = pick(prov, "vids", ruleFor(ref.Kind, "vids"), parts, func(b *Bridge) []VidRange { return b.Vids }, nonEmptySlc[VidRange], vidsString)
	out.Addresses, _ = pick(prov, "addresses", ruleFor(ref.Kind, "addresses"), parts, func(b *Bridge) []string { return b.Addresses }, nonEmptySlc[string], sortedJoin)
	out.Gateway, _ = pick(prov, "gateway", ruleFor(ref.Kind, "gateway"), parts, func(b *Bridge) string { return b.Gateway }, nonEmptyStr, keyStr)
	out.Comments, _ = pick(prov, "comments", ruleFor(ref.Kind, "comments"), parts, func(b *Bridge) string { return b.Comments }, nonEmptyStr, keyStr)
	out.MTU, _ = pick(prov, "mtu", ruleFor(ref.Kind, "mtu"), parts, func(b *Bridge) int { return b.MTU }, nonZeroInt, keyInt)
	out.MTUDeclared, _ = pick(prov, "mtuDeclared", ruleFor(ref.Kind, "mtuDeclared"), parts, func(b *Bridge) int { return b.MTUDeclared }, nonZeroInt, keyInt)
	vlanAware, vaOK := pick(prov, "vlanAware", ruleFor(ref.Kind, "vlanAware"), parts, func(b *Bridge) boolOpt { return boolOpt{b.VlanAware, b.VlanAwareSet} }, boolOptSet, boolOptKey)
	out.VlanAware, out.VlanAwareSet = vlanAware.v, vaOK
	stp, stpOK := pick(prov, "stp", ruleFor(ref.Kind, "stp"), parts, func(b *Bridge) boolOpt { return boolOpt{b.STP, b.STPSet} }, boolOptSet, boolOptKey)
	out.STP, out.STPSet = stp.v, stpOK
	out.Pending, _ = pick(prov, "pending", ruleFor(ref.Kind, "pending"), parts, func(b *Bridge) string { return b.Pending }, nonEmptyStr, keyStr)
	return resolved{entity: out, prov: *prov}
}

func resolveVlan(ref Ref, parts map[Source]*VlanIface) resolved {
	out := &VlanIface{Ref: ref}
	prov := newProvenance()
	out.Name, _ = pick(prov, "name", ruleFor(ref.Kind, "name"), parts, func(v *VlanIface) string { return v.Name }, nonEmptyStr, keyStr)
	out.ParentName, _ = pick(prov, "parentName", ruleFor(ref.Kind, "parentName"), parts, func(v *VlanIface) string { return v.ParentName }, nonEmptyStr, keyStr)
	out.Vid, _ = pick(prov, "vid", ruleFor(ref.Kind, "vid"), parts, func(v *VlanIface) int { return v.Vid }, nonZeroInt, keyInt)
	out.Addresses, _ = pick(prov, "addresses", ruleFor(ref.Kind, "addresses"), parts, func(v *VlanIface) []string { return v.Addresses }, nonEmptySlc[string], sortedJoin)
	out.MTU, _ = pick(prov, "mtu", ruleFor(ref.Kind, "mtu"), parts, func(v *VlanIface) int { return v.MTU }, nonZeroInt, keyInt)
	out.MTUDeclared, _ = pick(prov, "mtuDeclared", ruleFor(ref.Kind, "mtuDeclared"), parts, func(v *VlanIface) int { return v.MTUDeclared }, nonZeroInt, keyInt)
	out.Pending, _ = pick(prov, "pending", ruleFor(ref.Kind, "pending"), parts, func(v *VlanIface) string { return v.Pending }, nonEmptyStr, keyStr)
	return resolved{entity: out, prov: *prov}
}

func slaveDetailKey(sd []BondSlaveState) string {
	e := &Bond{SlaveDetail: sd}
	return e.fieldMap()["slaveDetail"]
}
