# ADR-0008: Support Linux bridges, OVS, and the full PVE SDN stack

**D-number:** D8 (`docs/architecture.md` §10)
**Status:** Accepted

## Context

Proxmox networking spans several genuinely different subsystems: native Linux bridges/bonds under
`ifupdown2`, Open vSwitch as an alternative bridging implementation, and the PVE SDN stack (zones →
VNets → subnets, with zone types Simple, VLAN, QinQ, VXLAN, EVPN, plus Fabrics as of PVE 9.0). A
product positioned as "all networking in Proxmox, visualized" (`docs/features/sdn.md`) has to
decide whether it covers all of that or only a subset.

## Decision

Support the full breadth: native Linux bridges, OVS, and the complete PVE SDN object model —
guided wizards per zone type, a topology-map overlay showing VNets as colored planes and
EVPN/VXLAN as a VTEP mesh, and staged-vs-running diffing for PVE's own SDN apply mechanism — rather
than shipping bridges-only or SDN-only and telling operators to drop into the stock PVE UI for the
rest.

## Consequences

**What this enables.** vnprox can be a genuine substitute for the stock PVE networking UI rather
than a partial one with gaps an operator has to route around. The guided wizards translate PVE's
own opaque config surface into plain-English steps with a live preview before anything is created;
the map overlay makes zone/VNet relationships and BGP/EVPN topology visible in a way raw
`/etc/pve/sdn/*.cfg` never is.

**What this costs / forecloses.** Every one of these surfaces is a distinct object model with its
own PVE-specific quirks vnprox has to track independently — and getting that wrong is not
hypothetical: Phase 31's own scoping work found that `internal/pvemock`, `docs/`, and Proxmox's
release notes all agreed SDN Fabrics were a *zone type* — and all four sources were wrong, because
each was written from the last rather than observed against a running node (`CLAUDE.md`'s standing
rule this incident produced: "Never model a PVE object from `internal/pvemock/`, from `docs/`, or
from Proxmox release notes... A type enum read off a running node is worth more than any amount of
documentation"). OVS support in particular means carrying two independent bridging implementations
with different write mechanisms side by side, which roughly doubles the validation and test surface
for anything bridge-related. Combined with ADR-0009's forward-tracking PVE-version commitment, this
breadth is a standing, compounding maintenance cost that a small — in practice, solo (see
`docs/adr/governance.md`) — maintainer team has to keep paying every time PVE ships a networking
change, not a one-time build cost.

## See also

- `docs/features/sdn.md` (the feature spec this decision produces).
- `internal/pve/sdn_fabric.go`, `internal/pvemock/sdn_fabric.go` (the corrected Fabrics model).
- `planning/tasks/phase-31.md` and `planning/reports/` for the Fabrics scoping incident.
