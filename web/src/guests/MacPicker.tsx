// Shared "pick a guest's MAC" dropdown (docs/features/sdn.md §5: "static
// reservations bound to guest MACs (picker)"), sourced from the same
// cluster-wide guest NIC list GuestsPage.tsx already renders
// (useAllGuestNicsQuery) — no dedicated "list every MAC" API route exists,
// this composes it client-side exactly like that hook's own doc comment
// already does for the guest NIC list itself. Consumed by the IPAM cell
// detail dialog's reservation flow (web/src/ipam/CellDetailDialog.tsx),
// alongside the existing free-text MAC field.
import { useMemo } from "react";
import { inputClass } from "../changesets/editors/EditorDialog";
import { useAllGuestNicsQuery } from "./queries";
import { macPickerOptions } from "./macPicker";

export interface MacPickerProps {
  /** Called with the picked MAC and its plain guest/nic label (e.g.
   * "web1/net0", undecorated — callers that want to prefill a hostname
   * field get a clean value, no display-string parsing required). */
  onPick: (mac: string, guestLabel: string) => void;
  className?: string;
}

export function MacPicker({ onPick, className }: MacPickerProps) {
  const { rows, isLoading } = useAllGuestNicsQuery();
  const options = useMemo(() => macPickerOptions(rows), [rows]);

  return (
    <select
      aria-label="Pick a guest MAC"
      className={className ?? inputClass}
      disabled={isLoading || options.length === 0}
      defaultValue=""
      onChange={(e) => {
        const mac = e.target.value;
        if (!mac) return;
        const opt = options.find((o) => o.mac === mac);
        onPick(mac, opt?.guestLabel ?? mac);
        e.target.value = "";
      }}
    >
      <option value="" disabled>
        {isLoading ? "Loading guest NICs…" : options.length === 0 ? "No guest NICs with a known MAC" : "Pick a guest NIC…"}
      </option>
      {options.map((o) => (
        <option key={o.mac} value={o.mac}>
          {o.optionLabel}
        </option>
      ))}
    </select>
  );
}
