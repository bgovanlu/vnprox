// SPDX-License-Identifier: Apache-2.0

// T-4208, promoted from three independent `role="radiogroup"` hand-rolls
// that had already converged on the same shape without sharing code:
//
//   - topology/ViewModeToggle.tsx (Switch/Graph) — the canonical case
//     docs/development.md names explicitly: "Segmented controls stay
//     segmented where they select a MODE rather than a sub-page."
//   - simulator/EndpointPicker.tsx's per-endpoint kind picker.
//   - settings/SettingsPage.tsx's light/dark theme toggle.
//
// All three: a bordered/padded container, `role="radiogroup"`, one
// `role="radio"` button per option, solid-accent active state. None of the
// three implemented arrow-key navigation (the standard radiogroup
// interaction — WAI-ARIA APG "radio group": arrow keys move focus AND
// selection together via a roving tabindex), so this adds it rather than
// copying the gap forward a fourth time.
//
// What did NOT get promoted: flows/FlowExplorer.tsx's raw/conversations
// switcher looks identical at a glance but is deliberately `role="tablist"`
// with a wash-tinted active state, not a radiogroup — its own comment
// quotes the same design-language rule this component's header does, for
// the opposite conclusion. That is a Tabs-family control, not this one.
import { useRef } from "react";
import type { KeyboardEvent } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";

export interface SegmentedOption<T extends string> {
  value: T;
  label: string;
}

export interface SegmentedControlProps<T extends string> {
  options: readonly SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  ariaLabel: string;
  size?: "sm" | "md";
  density?: Density;
  className?: string;
}

const SIZE_CLASSES: Record<"sm" | "md", Record<Density, string>> = {
  sm: { comfortable: "px-2 py-0.5 text-xs", compact: "px-1.5 py-0.5 text-[11px]" },
  md: { comfortable: "px-3 py-1 text-sm", compact: "px-2.5 py-0.5 text-xs" },
};

/** A mode switch, never a sub-page nav (that's Tabs). One value selected
 * from a small closed set — Switch/Graph, a theme, an endpoint kind. */
export function SegmentedControl<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
  size = "md",
  density,
  className,
}: SegmentedControlProps<T>) {
  const resolvedDensity = useDensity(density);
  const containerRef = useRef<HTMLDivElement>(null);

  function moveTo(index: number): void {
    const next = options[((index % options.length) + options.length) % options.length];
    if (!next) return;
    onChange(next.value);
    // Roving tabindex (WAI-ARIA APG "radio group"): the newly-checked
    // option is the one arrow keys should now move focus AND selection
    // from, so move DOM focus to it once its tabIndex=0 is committed.
    requestAnimationFrame(() => {
      containerRef.current?.querySelector<HTMLButtonElement>(`[data-value="${next.value}"]`)?.focus();
    });
  }

  function onKeyDown(e: KeyboardEvent<HTMLButtonElement>, index: number): void {
    switch (e.key) {
      case "ArrowRight":
      case "ArrowDown":
        e.preventDefault();
        moveTo(index + 1);
        break;
      case "ArrowLeft":
      case "ArrowUp":
        e.preventDefault();
        moveTo(index - 1);
        break;
      case "Home":
        e.preventDefault();
        moveTo(0);
        break;
      case "End":
        e.preventDefault();
        moveTo(options.length - 1);
        break;
      default:
        break;
    }
  }

  return (
    <div
      ref={containerRef}
      role="radiogroup"
      aria-label={ariaLabel}
      data-density={resolvedDensity}
      className={clsx(
        "inline-flex gap-0.5 rounded-md border border-slate-200 bg-white/90 p-0.5 dark:border-slate-700 dark:bg-slate-900/90",
        className,
      )}
    >
      {options.map((option, index) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            data-value={option.value}
            tabIndex={active ? 0 : -1}
            onClick={() => {
              onChange(option.value);
            }}
            onKeyDown={(e) => {
              onKeyDown(e, index);
            }}
            className={clsx(
              "rounded font-medium transition-colors duration-[var(--motion-fast)] ease-standard",
              SIZE_CLASSES[size][resolvedDensity],
              active
                ? "bg-accent-600 text-white"
                : "text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800",
            )}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}
