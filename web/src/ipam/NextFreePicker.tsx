// Shared "next free address" picker (docs/features/ipam.md §3: "'Next free
// address' picker exposed everywhere an IP is entered elsewhere in the
// UI"). This is T-405 acceptance criterion 4's reusable component: the
// IPAM reserve dialog is its primary consumer, but any other form that
// takes an address (e.g. BridgeEditor's Addresses field,
// changesets/editors/BridgeEditor.tsx) can drop it in unchanged — it only
// needs a subnet CIDR and an onPick callback, no IPAM-page-specific state.
import { useMemo } from "react";
import { Button } from "../components/Button";
import { nextFreeAddress } from "./nextFree";
import { useIpamAllocationsQuery } from "./queries";

export interface NextFreePickerProps {
  /** Which subnet to suggest a free address from. */
  subnetCidr: string | undefined;
  /** Called with the suggested bare IP address (no prefix) when clicked. */
  onPick: (ip: string) => void;
  className?: string;
}

/** A small button that looks up subnetCidr's allocation grid and suggests
 * its lowest free address — skipping every allocated/observed/reserved/
 * gateway address (T-405 acceptance criterion 4), via the exact same
 * confidence-merged grid data the IPAM page's own grid renders, so the
 * suggestion can never contradict what the grid shows. */
export function NextFreePicker({ subnetCidr, onPick, className }: NextFreePickerProps) {
  const { data: grid, isLoading } = useIpamAllocationsQuery(subnetCidr);
  const suggestion = useMemo(() => nextFreeAddress(grid?.cells), [grid]);

  if (!subnetCidr) {
    return null;
  }

  if (grid?.paged) {
    return (
      <span className={className} title="This subnet is too large to auto-suggest — browse its allocation grid instead.">
        <Button variant="ghost" size="sm" disabled>
          Subnet too large to suggest
        </Button>
      </span>
    );
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
