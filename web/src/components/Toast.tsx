// SPDX-License-Identifier: Apache-2.0

import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import * as RadixToast from "@radix-ui/react-toast";
import clsx from "clsx";
import { DensityProvider, useDensity, type Density } from "./density";
import { useReducedMotion } from "../lib/useReducedMotion";

export type ToastVariant = "default" | "success" | "error";

export interface ToastInput {
  title: string;
  description?: string;
  variant?: ToastVariant;
  /** ms before auto-dismiss; Radix default is 5000. */
  durationMs?: number;
}

interface ToastRecord extends Required<Pick<ToastInput, "title" | "variant" | "durationMs">> {
  id: number;
  description?: string;
}

interface ToastContextValue {
  toast: (input: ToastInput) => void;
}

const ToastContext = createContext<ToastContextValue | undefined>(undefined);

// T-3405: hairline borders (default variant matches Dialog/Drawer's
// slate-200/slate-800 pairing); status variants keep their own hue but stay
// on the same hairline weight rather than a heavier border.
const variantClasses: Record<ToastVariant, string> = {
  default: "border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900",
  success: "border-emerald-300 bg-emerald-50 dark:border-emerald-700 dark:bg-emerald-950",
  error: "border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-950",
};

const DENSITY_PADDING: Record<Density, string> = { comfortable: "p-3", compact: "p-2" };

export interface ToastProviderProps {
  children: ReactNode;
  /** T-905: compact/comfortable padding (density.ts) for every toast card
   * this provider renders — "comfortable" is this component's original
   * `p-3`, so the prop is additive. Defaults to the ambient
   * `<DensityProvider>` in scope. */
  density?: Density;
}

/** App-wide toast host + `useToast()` API. Mount once near the app root
 * (see src/layout/AppShell.tsx); everything else calls `useToast().toast(...)`
 * without knowing Radix Toast exists underneath. */
export function ToastProvider({ children, density }: ToastProviderProps) {
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const nextId = useRef(0);
  const resolvedDensity = useDensity(density);
  // T-905: reduced motion drops the slide-in/fade-out animation classes —
  // the toast still appears/dismisses, just without the transition.
  const reducedMotion = useReducedMotion();

  const toast = useCallback((input: ToastInput) => {
    const id = nextId.current++;
    const record: ToastRecord = {
      id,
      title: input.title,
      description: input.description,
      variant: input.variant ?? "default",
      durationMs: input.durationMs ?? 5000,
    };
    setToasts((prev) => [...prev, record]);
  }, []);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const value = useMemo<ToastContextValue>(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      <DensityProvider density={resolvedDensity}>
        <RadixToast.Provider swipeDirection="right">
          {children}
          {toasts.map((t) => (
            <RadixToast.Root
              key={t.id}
              data-density={resolvedDensity}
              duration={t.durationMs}
              onOpenChange={(open) => {
                if (!open) dismiss(t.id);
              }}
              className={clsx(
                // T-3405: larger radius, subtle shadow (docs/development.md
                // "Visual language" — shadows reserved for floating layers,
                // which a toast is, so it keeps one, just a lighter one).
                "rounded-xl border shadow-md",
                DENSITY_PADDING[resolvedDensity],
                !reducedMotion &&
                  "data-[state=open]:animate-in data-[state=open]:slide-in-from-right data-[state=closed]:animate-out data-[state=closed]:fade-out",
                variantClasses[t.variant],
              )}
            >
              <RadixToast.Title className="text-sm font-medium text-slate-900 dark:text-slate-100">
                {t.title}
              </RadixToast.Title>
              {t.description ? (
                <RadixToast.Description className="mt-1 text-sm text-slate-600 dark:text-slate-300">
                  {t.description}
                </RadixToast.Description>
              ) : null}
            </RadixToast.Root>
          ))}
          {/* pointer-events-none on the whole toast layer: these toasts are
              purely informational (no buttons to click) and auto-dismiss, so
              they must never intercept clicks meant for the UI beneath them.
              The viewport sits bottom-right, directly over a right-side
              drawer's Apply/Back action bar — without this a visible toast
              silently ate those clicks (the "Apply does nothing" bug). Children
              inherit none, so the cards are click-through too. */}
          <RadixToast.Viewport className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2 outline-none" />
        </RadixToast.Provider>
      </DensityProvider>
    </ToastContext.Provider>
  );
}

// useToast is a hook, not a component — expected to trip
// only-export-components since ToastProvider (a component) is exported
// from the same file. Keeping them together is the point (see this
// file's top comment); disabled per-line rather than restructuring.
// eslint-disable-next-line react-refresh/only-export-components
export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error("useToast must be used within <ToastProvider>");
  }
  return ctx;
}
