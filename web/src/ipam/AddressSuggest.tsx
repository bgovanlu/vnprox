// SPDX-License-Identifier: Apache-2.0

// Shared "suggest next free address from an SDN subnet" affordance for any
// editor's Addresses field (T-3104, docs/features/ipam.md §3: "wiring
// [NextFreePicker] into every other IP-entry field in the UI ... is a known
// follow-up"). Extracted from BridgeEditor's original (private)
// BridgeAddressSuggest so VlanEditor/InterfaceEditor reuse the same
// subnet-picker + NextFreePicker pairing instead of each hand-rolling its
// own copy — the same "reuse, don't duplicate" discipline
// web/src/sdn/wizards/SubnetStep.tsx already follows for the gateway field
// (see that file's own doc comment).
import { useState } from "react";
import { inputClass } from "../changesets/editors/EditorDialog";
import { NextFreePicker } from "./NextFreePicker";
import { useIpamSubnetsQuery } from "./queries";

export interface AddressSuggestProps {
  /** Called with a CIDR (ip/prefix) to append to the caller's addresses
   * field. */
  onAppend: (cidr: string) => void;
}

/** A subnet picker + NextFreePicker pair: choose a known SDN subnet, then
 * suggest its lowest free address (with that subnet's own prefix length
 * attached) to append to an Addresses field. Renders nothing when there are
 * no SDN subnets to suggest from. */
export function AddressSuggest({ onAppend }: AddressSuggestProps) {
  const { data: subnets } = useIpamSubnetsQuery();
  const [subnetCidr, setSubnetCidr] = useState<string | undefined>(undefined);
  const options = subnets?.items.filter((s) => s.source === "sdn") ?? [];
  if (options.length === 0) {
    return null;
  }
  const prefix = subnetCidr?.split("/")[1];
  return (
    <div className="mt-1 flex items-center gap-2">
      <select
        className={inputClass}
        aria-label="Suggest address from subnet"
        value={subnetCidr ?? ""}
        onChange={(e) => {
          setSubnetCidr(e.target.value || undefined);
        }}
      >
        <option value="">Suggest from a subnet…</option>
        {options.map((s) => (
          <option key={s.cidr} value={s.cidr}>
            {s.cidr}
          </option>
        ))}
      </select>
      <NextFreePicker
        subnetCidr={subnetCidr}
        onPick={(ip) => {
          onAppend(prefix ? `${ip}/${prefix}` : ip);
        }}
      />
    </div>
  );
}
