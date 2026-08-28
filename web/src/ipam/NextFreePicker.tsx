// SPDX-License-Identifier: Apache-2.0

// Shared "next free address" picker (docs/features/ipam.md §3: "'Next free
// address' picker exposed everywhere an IP is entered elsewhere in the
// UI"). This is T-405 acceptance criterion 4's reusable component: the
// IPAM reserve dialog is its primary consumer, but any other form that
// takes an address (e.g. BridgeEditor's Addresses field,
// changesets/editors/BridgeEditor.tsx) can drop it in unchanged — it only
// needs a subnet CIDR and an onPick callback, no IPAM-page-specific state.
import { Button } from "../components/Button";
import { useIpamAllocationsQuery } from "./queries";

export interface NextFreePickerProps {
  /** Which subnet to suggest a free address from. */
  subnetCidr: string | undefined;
  /** Called with the suggested bare IP address (no prefix) when clicked. */
  onPick: (ip: string) => void;
  className?: string;
}

/** A small button that suggests subnetCidr's lowest free address — the start
 * of the first collapsed free range the backend computes for the address
 * list, so the suggestion can never contradict what the list shows, and
 * (unlike the old grid-scan) works at any subnet size. */
export function NextFreePicker({ subnetCidr, onPick, className }: NextFreePickerProps) {
  const { data, isLoading } = useIpamAllocationsQuery(subnetCidr);
  const suggestion = data?.freeRanges[0]?.start;

  if (!subnetCidr) {
    return null;
  }

  return (
    <Button
      type="button"
      variant="secondary"
      size="sm"
      className={className}
      disabled={isLoading || !suggestion}
      onClick={() => {
        if (suggestion) {
          onPick(suggestion);
        }
      }}
    >
      {suggestion ? `Suggest ${suggestion}` : isLoading ? "Loading…" : "No free address"}
    </Button>
  );
}
