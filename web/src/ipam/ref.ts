// SPDX-License-Identifier: Apache-2.0

// Ref-string helpers for IPAM (docs/api.md's Ref triplet scheme,
// internal/inventory/ref.go's Ref.String: "kind:node:id", empty node for
// cluster-scoped kinds like sdn-subnet). GET /ipam/subnets doesn't carry a
// literal `ref` field (internal/ipam.Subnet is a view, not an inventory
// entity), so the frontend builds the target Ref for ipam.alloc.* ops
// itself from the subnet's cidr — the same construction
// internal/inventory.FromPVESDN uses server-side
// (Ref{Kind: KindSDNSubnet, ID: s.CIDR}).
export function subnetRef(cidr: string): string {
  return `sdn-subnet::${cidr}`;
}
