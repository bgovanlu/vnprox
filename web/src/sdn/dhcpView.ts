// Pure logic backing DhcpView.tsx, split out for direct Vitest testing
// (mirroring evpnStatus.ts's own "pure derivation, no RTL" split).
import type { SdnTree } from "../api/types";

/** Every currently-configured zone id, for the DHCP view's zone filter
 * dropdown. */
export function zoneOptions(tree: SdnTree | undefined): string[] {
  if (!tree) return [];
  return tree.zones.map((z) => z.id);
}
