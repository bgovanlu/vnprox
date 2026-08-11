package spec

// adopt.go is the "move the DOCUMENT to where the cluster already is"
// direction — T-2703's "adopt reality" half, and the companion delta.go
// deliberately does not have.
//
// # Why ApplyOps could not do this
//
// delta.go renders a changeset's ops into the document, and its op vocabulary
// has no delete: Import never emits one (an entity dropped from the document
// is REPORTED as notInSpec, never pruned), so there is no op whose rendering
// would round-trip. That is correct for the propose path and useless for this
// one: adopting reality for an entity the document declares and the cluster no
// longer has means REMOVING the declaration, which is precisely the expression
// ApplyOps refuses. RemoveEntities is that missing companion, and
// AdoptEntities is the whole "adopt" operation built on it plus Export.
//
// # "Reality" here means the fields Import compares against
//
// AdoptEntities renders each adopted entity through export.go's own
// exportBond/exportBridge/exportVLAN/exportZone/exportVnet/exportSubnet — the
// same functions Export uses, reading the same *declared* fields
// (DeclaredPortNames, DeclaredSlaves, MTUDeclared, ...). That is not an
// oversight about runtime state; it is the only rendering that satisfies what
// adopting is supposed to mean. Import diffs the document against those same
// declared fields, so Export's reconcile identity — Export(live) then
// Import(spec, live) is zero ops (T-1101 AC2) — is exactly the property
// "adopted" has to have, entity by entity (T-2703 AC1). A document that
// instead declared a netlink-only value would re-plan to a non-empty diff the
// moment it was written, which is the opposite of adoption.
//
// A divergence that exists ONLY between the interfaces file and the running
// kernel therefore cannot be adopted, and this file does not pretend
// otherwise: AdoptEntities returns the document unchanged, and the caller
// reports that adopting is not applicable rather than opening a pull request
// that changes nothing. internal/drift's three-position finding still names
// the live position, so the operator sees the divergence — it just is not one
// a spec commit can resolve.

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ErrKindNotAdoptable is returned for a ref whose kind the declarative
// document has no vocabulary for (a firewall ruleset, a guest, a physical NIC,
// an OVS bridge/bond/int-port). It names the ref.
var ErrKindNotAdoptable = errors.New("spec: entity kind is not in the spec's vocabulary")

// adoptableKinds is the closed set of kinds this file will write or remove. It
// is deliberately the same set import.go's managedKinds covers: adopting an
// entity Import does not reconcile would put a declaration in the document
// that nothing ever diffs.
//
//nolint:gochecknoglobals // a lookup set, read-only after init
var adoptableKinds = map[inventory.Kind]bool{
	inventory.KindBond:      true,
	inventory.KindBridge:    true,
	inventory.KindVlan:      true,
	inventory.KindSDNZone:   true,
	inventory.KindSDNVnet:   true,
	inventory.KindSDNSubnet: true,
}

// RemoveEntities returns a copy of base with each ref's declaration removed,
// leaving base untouched.
//
// Removing a declaration the document does not have is a no-op, not an error:
// the caller's post-condition is "the document does not declare this", and it
// already holds. A node left with no bonds, bridges or VLANs by a removal is
// dropped too, so the document does not accumulate empty stanzas — but only a
// node this call actually emptied, never one the base document already carried
// empty for its own reasons.
func RemoveEntities(base Spec, refs []inventory.Ref) (Spec, error) {
	out := cloneSpec(base)
	if out.SpecVersion == 0 {
		out.SpecVersion = Version
	}
	touched := map[string]bool{}
	for _, ref := range refs {
		if err := removeEntity(&out, ref); err != nil {
			return Spec{}, err
		}
		touched[ref.Node] = true
	}
	pruneEmptyNodes(&out, touched)
	sortSpec(&out)
	return out, nil
}

// AdoptEntities returns a copy of base in which every ref in refs is declared
// exactly as the live snapshot declares it — or, for a ref live no longer has,
// is not declared at all.
//
// It is the document-side inverse of Import for those refs: after this call,
// Import(result, live) emits no op targeting any of them. That property is
// checked by the caller before anything is committed (internal/gitsync's
// adoption round-trip), not merely intended here.
//
// Entities NOT named in refs are left exactly as the base document has them.
// Adoption is per-entity and explicit: a drift finding about one bridge must
// never quietly rewrite the rest of the cluster's intent.
func AdoptEntities(base Spec, refs []inventory.Ref, live inventory.Snapshot) (Spec, error) {
	out := cloneSpec(base)
	if out.SpecVersion == 0 {
		out.SpecVersion = Version
	}
	emptied := map[string]bool{}
	for _, ref := range refs {
		if !adoptableKinds[ref.Kind] {
			return Spec{}, fmt.Errorf("%w: %s", ErrKindNotAdoptable, refLabel(ref))
		}
		e, found := live.Get(ref)
		if !found {
			// Live does not have it any more. Adopting reality means the
			// document stops claiming it — the delete ApplyOps cannot express.
			if err := removeEntity(&out, ref); err != nil {
				return Spec{}, err
			}
			emptied[ref.Node] = true
			continue
		}
		if err := adoptEntity(&out, ref, e); err != nil {
			return Spec{}, err
		}
	}
	pruneEmptyNodes(&out, emptied)
	sortSpec(&out)
	return out, nil
}

// SameIntent reports whether two documents declare the same thing.
//
// The comparison is SEMANTIC, not textual: both sides are canonicalised —
// every entity list sorted by identity and every set-valued field (ports,
// slaves, vids, addresses, dhcp ranges, zone node lists) sorted — before they
// are compared. Two documents that differ only in the order an operator
// happened to write a port list in are the same intent, and a caller that
// treated them as different would offer to open a pull request with an empty
// diff. That is exactly the state T-2703 AC5 forbids, which is why this is a
// function rather than a bytes.Equal at each call site.
func SameIntent(a, b Spec) (bool, error) {
	ca, err := Marshal(canonicalized(a))
	if err != nil {
		return false, fmt.Errorf("spec: canonicalising a document for comparison: %w", err)
	}
	cb, err := Marshal(canonicalized(b))
	if err != nil {
		return false, fmt.Errorf("spec: canonicalising a document for comparison: %w", err)
	}
	return bytes.Equal(ca, cb), nil
}

// canonicalized returns a copy of s in a canonical form: default specVersion,
// Export's entity ordering, and every set-valued field sorted.
func canonicalized(s Spec) Spec {
	out := cloneSpec(s)
	if out.SpecVersion == 0 {
		out.SpecVersion = Version
	}
	for i := range out.Nodes {
		n := &out.Nodes[i]
		for j := range n.Bonds {
			n.Bonds[j].Slaves = sortedCopy(n.Bonds[j].Slaves)
		}
		for j := range n.Bridges {
			n.Bridges[j].Ports = sortedCopy(n.Bridges[j].Ports)
			n.Bridges[j].Vids = sortedCopy(n.Bridges[j].Vids)
			n.Bridges[j].Addresses = sortedCopy(n.Bridges[j].Addresses)
		}
		for j := range n.VLANs {
			n.VLANs[j].Addresses = sortedCopy(n.VLANs[j].Addresses)
		}
	}
	if out.SDN != nil {
		for i := range out.SDN.Zones {
			z := &out.SDN.Zones[i]
			z.Nodes = sortedCopy(z.Nodes)
			z.ExitNodes = sortedCopy(z.ExitNodes)
			z.Peers = sortedCopy(z.Peers)
		}
		for i := range out.SDN.Subnets {
			out.SDN.Subnets[i].DHCPRanges = sortedCopy(out.SDN.Subnets[i].DHCPRanges)
		}
	}
	sortSpec(&out)
	sort.SliceStable(out.Nodes, func(i, j int) bool { return out.Nodes[i].Name < out.Nodes[j].Name })
	return out
}

// adoptEntity writes one live entity's Export rendering into the document,
// replacing any existing declaration of the same ref.
func adoptEntity(s *Spec, ref inventory.Ref, e inventory.Entity) error {
	switch v := e.(type) {
	case *inventory.Bond:
		if ref.Kind != inventory.KindBond {
			return fmt.Errorf("%w: %s", ErrKindNotAdoptable, refLabel(ref))
		}
		n := nodeFor(s, ref.Node)
		if existing := findBond(n, ref.ID); existing != nil {
			*existing = exportBond(v)
			return nil
		}
		n.Bonds = append(n.Bonds, exportBond(v))
	case *inventory.Bridge:
		if ref.Kind != inventory.KindBridge {
			return fmt.Errorf("%w: %s", ErrKindNotAdoptable, refLabel(ref))
		}
		n := nodeFor(s, ref.Node)
		if existing := findBridge(n, ref.ID); existing != nil {
			*existing = exportBridge(v)
			return nil
		}
		n.Bridges = append(n.Bridges, exportBridge(v))
	case *inventory.VlanIface:
		if ref.Kind != inventory.KindVlan || v.Virt == "ovs" {
			return fmt.Errorf("%w: %s (an OVS int port is not in the spec's vocabulary)", ErrKindNotAdoptable, refLabel(ref))
		}
		n := nodeFor(s, ref.Node)
		if existing := findVLAN(n, ref.ID); existing != nil {
			*existing = exportVLAN(v)
			return nil
		}
		n.VLANs = append(n.VLANs, exportVLAN(v))
	case *inventory.SdnZone:
		sdn := sdnFor(s)
		for i := range sdn.Zones {
			if sdn.Zones[i].ID == ref.ID {
				sdn.Zones[i] = exportZone(v)
				return nil
			}
		}
		sdn.Zones = append(sdn.Zones, exportZone(v))
	case *inventory.SdnVnet:
		sdn := sdnFor(s)
		for i := range sdn.Vnets {
			if sdn.Vnets[i].ID == ref.ID {
				sdn.Vnets[i] = exportVnet(v)
				return nil
			}
		}
		sdn.Vnets = append(sdn.Vnets, exportVnet(v))
	case *inventory.SdnSubnet:
		sdn := sdnFor(s)
		for i := range sdn.Subnets {
			if sdn.Subnets[i].ID == ref.ID {
				sdn.Subnets[i] = exportSubnet(v)
				return nil
			}
		}
		sdn.Subnets = append(sdn.Subnets, exportSubnet(v))
	default:
		return fmt.Errorf("%w: %s", ErrKindNotAdoptable, refLabel(ref))
	}
	return nil
}

// removeEntity drops one ref's declaration from the document.
func removeEntity(s *Spec, ref inventory.Ref) error {
	if !adoptableKinds[ref.Kind] {
		return fmt.Errorf("%w: %s", ErrKindNotAdoptable, refLabel(ref))
	}
	switch ref.Kind {
	case inventory.KindBond:
		if n := findNode(s, ref.Node); n != nil {
			n.Bonds = filterBonds(n.Bonds, ref.ID)
		}
	case inventory.KindBridge:
		if n := findNode(s, ref.Node); n != nil {
			n.Bridges = filterBridges(n.Bridges, ref.ID)
		}
	case inventory.KindVlan:
		if n := findNode(s, ref.Node); n != nil {
			n.VLANs = filterVLANs(n.VLANs, ref.ID)
		}
	case inventory.KindSDNZone:
		if s.SDN != nil {
			s.SDN.Zones = filterZones(s.SDN.Zones, ref.ID)
		}
	case inventory.KindSDNVnet:
		if s.SDN != nil {
			s.SDN.Vnets = filterVnets(s.SDN.Vnets, ref.ID)
		}
	case inventory.KindSDNSubnet:
		if s.SDN != nil {
			s.SDN.Subnets = filterSubnets(s.SDN.Subnets, ref.ID)
		}
	default:
		return fmt.Errorf("%w: %s", ErrKindNotAdoptable, refLabel(ref))
	}
	return nil
}

// pruneEmptyNodes drops the node stanzas this call emptied. Only names in
// touched are considered, so a base document that carries an entity-less node
// on purpose keeps it.
func pruneEmptyNodes(s *Spec, touched map[string]bool) {
	if len(touched) == 0 {
		return
	}
	out := s.Nodes[:0]
	for _, n := range s.Nodes {
		if touched[n.Name] && len(n.Bonds) == 0 && len(n.Bridges) == 0 && len(n.VLANs) == 0 {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		s.Nodes = nil
		return
	}
	s.Nodes = out
}

func filterBonds(in []BondSpec, name string) []BondSpec {
	out := in[:0]
	for _, b := range in {
		if b.Name != name {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterBridges(in []BridgeSpec, name string) []BridgeSpec {
	out := in[:0]
	for _, b := range in {
		if b.Name != name {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterVLANs(in []VLANSpec, name string) []VLANSpec {
	out := in[:0]
	for _, v := range in {
		if v.Name != name {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterZones(in []ZoneSpec, id string) []ZoneSpec {
	out := in[:0]
	for _, z := range in {
		if z.ID != id {
			out = append(out, z)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterVnets(in []VnetSpec, id string) []VnetSpec {
	out := in[:0]
	for _, v := range in {
		if v.ID != id {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterSubnets(in []SubnetSpec, id string) []SubnetSpec {
	out := in[:0]
	for _, sn := range in {
		if sn.ID != id {
			out = append(out, sn)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
