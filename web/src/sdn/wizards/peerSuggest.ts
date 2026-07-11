// Pure address-parsing logic for the VXLAN wizard's "peer address list
// auto-suggested from cluster node IPs" (docs/features/sdn.md §2). There is
// no dedicated "cluster node IP" API in this codebase (docs/architecture.md
// §5 only documents peer discovery via the PVE API's own /cluster/status,
// which isn't surfaced to the frontend) — the smallest reasonable read
// consistent with existing patterns is each member node's own bridge/VLAN
// interface address, already fetchable via GET /inventory/{ref}'s `fields.
// addresses` (internal/inventory.Bridge/VlanIface's fieldMap: a comma-
// joined CIDR list). Data fetching lives in useSuggestedPeers.ts.

/** Extracts the first host address (address without its CIDR prefix) from
 * a fieldMap-style comma-joined CIDR list, e.g. "10.10.0.11/24,10.10.0.11/64"
 * -> "10.10.0.11". Returns undefined for an empty/unparseable field. */
export function firstHostAddress(addressesField: string | undefined): string | undefined {
  if (!addressesField) return undefined;
  const first = addressesField
    .split(",")
    .map((s) => s.trim())
    .find((s) => s.length > 0);
  if (!first) return undefined;
  const host = first.split("/")[0];
  return host && host.length > 0 ? host : undefined;
}
