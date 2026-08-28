// SPDX-License-Identifier: Apache-2.0

// A small, generic debounced-value hook — the wizard preview pane's "live
// but not janky" update (T-403 acceptance criterion 4: "Preview pane
// updates live as parameters change (<100ms perceived; debounced)").
// Mirrors web/src/changesets/rawEditor/useRawEditor.ts's own manual
// setTimeout-ref debounce pattern (the only other debounce in this
// codebase) rather than pulling in a new "use-debounce"-style dependency.
import { useEffect, useRef, useState } from "react";

/** Comfortably under the acceptance criterion's <100ms perceived-latency
 * budget: the debounce delay itself is the dominant, deterministic part of
 * that budget (elkjs layout + React Flow's own re-render are the rest, and
 * are not delayed by this hook at all — they run immediately once the
 * debounce fires). See WizardPreviewPane.test.tsx for the timing-aware
 * test proving a rapid burst of param changes settles to exactly one
 * recompute, `PREVIEW_DEBOUNCE_MS` after the last change. */
export const PREVIEW_DEBOUNCE_MS = 80;

export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  const timer = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      setDebounced(value);
    }, delayMs);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
    // Re-runs whenever the input value (by reference) or the delay changes.
  }, [value, delayMs]);

  return debounced;
}
