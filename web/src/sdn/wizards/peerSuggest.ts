// SPDX-License-Identifier: Apache-2.0

// Pure address-parsing logic for the VXLAN wizard's "peer address list
// auto-suggested from cluster node IPs" (docs/features/sdn.md §2). There is
// no dedicated "cluster node IP" API in this codebase (docs/architecture.md
// §5 only documents peer discovery via the PVE API's own /cluster/status,
// which isn't surfaced to the frontend) — the smallest reasonable read
// consistent with existing patterns is each member node's own bridge/VLAN
// interface address, already fetchable via GET /inventory/{ref}. Data
// fetching lives in useSuggestedPeers.ts.
//
// T-2108 correction. This file previously documented `fields.addresses` as
// "a comma-joined CIDR list", citing inventory.Bridge/VlanIface's `fieldMap`.
// That is the merge/provenance table, NOT the response shape:
// topology.Detail builds `fields` with `json.Marshal(entity)`, so the key is
// Go's own field name `Addresses` and the value is a JSON array of CIDR
// strings. The consumer therefore looked up a key that does not exist,
// type-guarded it to `string`, and silently suggested nothing — for every
// node, always. The wizard's Next button stayed disabled behind "An address
// is required." while its own copy promised "vnprox suggests each node's own
// address automatically", so the VXLAN zone wizard could not be completed at
// all. Both shapes are accepted below: the array the API really sends, and
// the comma-joined string, so a caller reading a fieldMap-derived value is
// still served correctly.

import { readStringList } from "../../api/entityFields";

/** Extracts the first host address (address without its CIDR prefix) from an
 * entity-detail `Addresses` value — either the API's array of CIDR strings
 * (`["10.10.0.11/24", "fd00::1/64"]` -> `"10.10.0.11"`) or a comma-joined
 * list in one string. Returns undefined for anything else, including an
 * empty list. */
export function firstHostAddress(addressesField: unknown): string | undefined {
  const candidates: string[] = Array.isArray(addressesField)
    ? addressesField.filter((v): v is string => typeof v === "string")
    : typeof addressesField === "string"
      ? addressesField.split(",")
      : [];
  const first = candidates.map((s) => s.trim()).find((s) => s.length > 0);
  if (first === undefined) return undefined;
  const host = first.split("/")[0];
  return host && host.length > 0 ? host : undefined;
}

/** Reads the `Addresses` field off a GET /inventory/{ref} `fields` map.
 * See api/entityFields.ts for why reading that map needs a dedicated,
 * tested helper rather than a property access and a type guard. */
export function addressesField(fields: Record<string, unknown>): string[] {
  return readStringList(fields, "Addresses");
}
