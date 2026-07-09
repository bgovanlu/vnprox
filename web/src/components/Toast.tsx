import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import * as RadixToast from "@radix-ui/react-toast";
import clsx from "clsx";

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

const variantClasses: Record<ToastVariant, string> = {
  default: "border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900",
  success: "border-emerald-300 bg-emerald-50 dark:border-emerald-700 dark:bg-emerald-950",
  error: "border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-950",
};

/** App-wide toast host + `useToast()` API. Mount once near the app root
 * (see src/layout/AppShell.tsx); everything else calls `useToast().toast(...)`
 * without knowing Radix Toast exists underneath. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const nextId = useRef(0);

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
      <RadixToast.Provider swipeDirection="right">
        {children}
        {toasts.map((t) => (
          <RadixToast.Root
            key={t.id}
            duration={t.durationMs}
            onOpenChange={(open) => {
              if (!open) dismiss(t.id);
            }}
            className={clsx(
              "rounded-md border p-3 shadow-lg data-[state=open]:animate-in data-[state=open]:slide-in-from-right",
              "data-[state=closed]:animate-out data-[state=closed]:fade-out",
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
        <RadixToast.Viewport className="fixed bottom-4 right-4 z-50 flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2 outline-none" />
      </RadixToast.Provider>
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
