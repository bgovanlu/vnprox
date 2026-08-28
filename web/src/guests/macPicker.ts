// SPDX-License-Identifier: Apache-2.0

// Pure logic backing MacPicker.tsx (T-406: "static reservations bound to
// guest MACs (picker)", docs/features/sdn.md §5) — framework-free,
// directly Vitest-able, mirroring web/src/ipam/nextFree.ts's split between
// pure derivation and the thin component that renders it.
import type { GuestNicRow } from "./guestNics";

export interface MacPickerOption {
  mac: string;
  /** The NIC's plain guest/nic label (e.g. "web1/net0"), undecorated —
   * kept separate from `optionLabel` so a caller wiring this into another
   * field (e.g. a hostname prefill) never has to parse it back out of a
   * combined display string. */
  guestLabel: string;
  /** The dropdown option's rendered text ("guestLabel (mac)"). */
  optionLabel: string;
  ref: string;
}

/** Derives the MAC picker's option list from the cluster-wide guest NIC
 * rows: only NICs with a known MAC (T-405's guest-agent-observation-style
 * "nothing to show" degradation for the rest), deduplicated by MAC
 * (a MAC should be unique per NIC, but a stale/duplicated topology
 * snapshot must never render two identical options), sorted by label for
 * a stable, scannable dropdown. */
export function macPickerOptions(rows: GuestNicRow[]): MacPickerOption[] {
  const seen = new Set<string>();
  const out: MacPickerOption[] = [];
  for (const r of rows) {
    if (!r.mac || seen.has(r.mac)) continue;
    seen.add(r.mac);
    out.push({ mac: r.mac, guestLabel: r.label, optionLabel: `${r.label} (${r.mac})`, ref: r.ref });
  }
  out.sort((a, b) => a.optionLabel.localeCompare(b.optionLabel));
  return out;
}
