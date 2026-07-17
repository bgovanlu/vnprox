import { forwardRef, useEffect, useState, type FormEvent } from "react";
import { Button } from "../components/Button";

export interface VlanFilterInputProps {
  value: number | undefined;
  onChange: (vlan: number | undefined) => void;
}

/** The VLAN filter box (`f` hotkey, docs/features/topology.md §2: "enter
 * VID(s) → the map dims everything not carrying that VLAN"). Single-VID for
 * now (the fixture-driven acceptance criterion is a single VLAN, e.g. 20);
 * multi-VID is a natural, additive follow-up. */
export const VlanFilterInput = forwardRef<HTMLInputElement, VlanFilterInputProps>(function VlanFilterInput(
  { value, onChange },
  ref,
) {
  const [draft, setDraft] = useState(value === undefined ? "" : String(value));

  // T-907: keep the visible draft in sync when `value` changes from outside
  // this component's own Apply/Clear handlers — e.g. loading a saved view
  // or a shareable-URL view sets vlanFilter programmatically (store.ts's
  // setVlanFilter), and the input must reflect that, not silently keep
  // showing whatever the user last typed/cleared. A no-op for the Apply/
  // Clear cases themselves (draft already matches by the time `value`
  // updates), so this never fights the user mid-keystroke.
  useEffect(() => {
    setDraft(value === undefined ? "" : String(value));
  }, [value]);

  function submit(e: FormEvent): void {
    e.preventDefault();
    const trimmed = draft.trim();
    if (trimmed === "") {
      onChange(undefined);
      return;
    }
    const parsed = Number(trimmed);
    if (Number.isInteger(parsed) && parsed > 0) {
      onChange(parsed);
    }
  }

  return (
    <form onSubmit={submit} className="flex items-center gap-1.5 rounded-md border border-slate-200 bg-white/90 px-2 py-1 shadow-sm dark:border-slate-700 dark:bg-slate-900/90">
      <label htmlFor="vlan-filter-input" className="text-xs text-slate-500 dark:text-slate-400">
        VLAN
      </label>
      <input
        ref={ref}
        id="vlan-filter-input"
        type="text"
        inputMode="numeric"
        placeholder="e.g. 20"
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value);
        }}
        className="w-16 rounded border border-slate-200 bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500 dark:border-slate-700"
      />
      <Button type="submit" size="sm" variant="ghost">
        Apply
      </Button>
      {value !== undefined && (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            setDraft("");
            onChange(undefined);
          }}
        >
          Clear
        </Button>
      )}
    </form>
  );
});
