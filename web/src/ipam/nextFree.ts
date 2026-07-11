// Pure "next free address" picker logic (docs/features/ipam.md §3:
// "'Next free address' picker exposed everywhere an IP is entered elsewhere
// in the UI"). Framework-free (no React import) so it's directly
// Vitest-able and shareable with any consumer that already has a resolved
// cell list, independent of how that consumer chose to fetch it.
import type { IpamCell } from "../api/types";

/** Returns the lowest-addressed free cell's IP, or undefined if none is
 * free (or `cells` is empty/undefined). `cells` is expected in ascending
 * address order — every backend response (internal/ipam's hostAddresses)
 * already is — so the first "free" match is the lowest free address,
 * skipping allocated/observed/reserved/gateway/conflict cells exactly as
 * T-405 acceptance criterion 4 requires. */
export function nextFreeAddress(cells: IpamCell[] | undefined): string | undefined {
  if (!cells) {
    return undefined;
  }
  return cells.find((c) => c.state === "free")?.ip;
}
