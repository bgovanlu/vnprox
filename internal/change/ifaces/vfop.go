// SPDX-License-Identifier: Apache-2.0

// vfop.go implements T-1506's vf.provision mutator: a post-up/post-down
// stanza pair appended to the PF's own *existing* iface stanza (Target) —
// the same "vnprox writes the file it will re-read" pattern this package's
// other append-style mutators (bond.create, bridge.port.add, ...) already
// establish, never a second mutation path or a shadow store. Unlike a
// nat/route rule, a vf.provision op's rendered commands are not this
// codebase's *only* record of the resulting VF state — once real hardware
// applies them, the VFs themselves become independently observable via
// host-netlink (internal/inventory.PhysNic.SRIOVVFs) — so there is no
// decode-back-apart requirement here the way a generated marker comment
// meant for later re-parsing would need.

package ifaces

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// mutateVFProvision resolves o's Count/VFs + top-level defaults into a
// concrete VF plan (host.ResolveVFPlan — the identical resolution
// internal/change's schema/referential validators already ran against the
// same op, so what gets rendered here is exactly what was validated) and
// appends its rendered post-up/post-down command pairs to the PF's iface
// stanza.
func mutateVFProvision(f *host.File, o VFProvision, nl string) error {
	plan := host.ResolveVFPlan(o.Count, o.VFs, o.VLAN, o.MacAddr, o.SpoofCheck, o.Trust)
	count := o.Count
	if count == 0 {
		count = len(plan)
	}
	ups, downs := host.VFProvisionCommands(o.Target.ID, count, plan)
	for i := range ups {
		if err := appendVFCommandPair(f, o.Target.ID, ups[i], downs[i], nl); err != nil {
			return err
		}
	}
	return nil
}

// appendVFCommandPair inserts a post-up/post-down BodyOption pair into
// ifaceName's stanza, just before any trailing run of blank body lines —
// the same placement insertBeforeTrailingBlanks gives every other
// append-style mutator in this package.
func appendVFCommandPair(f *host.File, ifaceName, up, down, nl string) error {
	e, ok := f.Iface(ifaceName)
	if !ok {
		return fmt.Errorf("ifaces: vf.provision: iface %q: %w", ifaceName, ErrNotFound)
	}
	items := []host.BodyItem{optionItem("post-up", up, nl), optionItem("post-down", down, nl)}
	e.Body = insertBeforeTrailingBlanks(e.Body, items, nl)
	return nil
}
