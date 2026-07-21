package tenant

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// GraphSource yields the live inventory snapshot the GraphExpander resolves
// coarse tenant scopes against. The daemon's *inventory.Graph satisfies it.
type GraphSource interface {
	Snapshot() inventory.Snapshot
}

// GraphExpander expands a coarse tenant scope Ref (a VNet or bridge the admin
// scoped a tenant to) into the concrete member Refs it implies, live against
// the inventory graph (T-1703 AC1). It is deliberately conservative and
// fail-closed: a scope Ref that resolves to nothing extra contributes only
// itself, and an unparseable Ref contributes only its literal string — the
// expansion can only ever ADD explicitly-related members, never widen to an
// unrelated resource.
//
// Supported coarse scopes and what they expand to:
//   - sdn-vnet: the VNet itself, every guest-NIC attached to it plus the owning
//     guest, and every subnet whose Vnet is that VNet.
//   - bridge / ovs-bridge: the bridge itself, every guest-NIC attached to it
//     plus the owning guest.
//   - guest: the guest itself plus its own guest-NICs.
//
// Any other Ref (a concrete guest-NIC, subnet, or an unrecognized kind) maps to
// itself only. Because expansion runs against the live snapshot every request,
// a guest moving onto/off a scoped VNet is reflected immediately — the tenant's
// visible set is never frozen into the store.
type GraphExpander struct {
	src GraphSource
}

// NewGraphExpander constructs a GraphExpander over src.
func NewGraphExpander(src GraphSource) *GraphExpander { return &GraphExpander{src: src} }

// ExpandScopeRefs implements Expander.
func (e *GraphExpander) ExpandScopeRefs(_ context.Context, scopeRefs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(scopeRefs)*4)
	for _, raw := range scopeRefs {
		if raw != "" {
			out[raw] = true
		}
	}
	if e == nil || e.src == nil {
		return out, nil
	}
	snap := e.src.Snapshot()

	for _, raw := range scopeRefs {
		ref, err := inventory.ParseRef(raw)
		if err != nil {
			continue // keep the literal ref only; never widen on a parse failure
		}
		switch ref.Kind {
		case inventory.KindSDNVnet:
			e.expandVnet(snap, ref, out)
		case inventory.KindBridge, inventory.KindOVSBridge:
			e.expandAttachTarget(snap, ref, out)
		case inventory.KindGuest:
			e.expandGuest(snap, ref, out)
		}
	}
	return out, nil
}

// expandVnet adds a VNet's attached guests/NICs and its subnets.
func (e *GraphExpander) expandVnet(snap inventory.Snapshot, vnet inventory.Ref, out map[string]bool) {
	e.expandAttachTarget(snap, vnet, out)
	for _, ent := range snap.All() {
		if sub, ok := ent.(*inventory.SdnSubnet); ok && sub.Vnet == vnet.ID {
			out[sub.GetRef().String()] = true
		}
	}
}

// expandAttachTarget adds every guest-NIC attached to target (a bridge or
// VNet) plus each such NIC's owning guest.
func (e *GraphExpander) expandAttachTarget(snap inventory.Snapshot, target inventory.Ref, out map[string]bool) {
	for _, ent := range snap.All() {
		nic, ok := ent.(*inventory.GuestNic)
		if !ok || nic.BridgeOrVnet != target {
			continue
		}
		out[nic.GetRef().String()] = true
		if !nic.Guest.IsZero() {
			out[nic.Guest.String()] = true
		}
	}
}

// expandGuest adds a guest's own guest-NICs.
func (e *GraphExpander) expandGuest(snap inventory.Snapshot, guest inventory.Ref, out map[string]bool) {
	for _, ent := range snap.All() {
		if nic, ok := ent.(*inventory.GuestNic); ok && nic.Guest == guest {
			out[nic.GetRef().String()] = true
		}
	}
}
