import type { ComponentPropsWithoutRef } from "react";
import * as RadixDialog from "@radix-ui/react-dialog";
import clsx from "clsx";

// react-refresh's only-export-components rule can't see that these are
// components — they're re-exported references to Radix's, not functions
// defined in this file — and flags the whole compound-component-module
// pattern (Dialog/DialogTrigger/DialogClose alongside the styled
// DialogContent below) as a Fast Refresh boundary risk. Accepted
// tradeoff for this pattern; disabled per-line rather than for the file
// so a genuinely non-component export here would still be caught.
// eslint-disable-next-line react-refresh/only-export-components
export const Dialog = RadixDialog.Root;
// eslint-disable-next-line react-refresh/only-export-components
export const DialogTrigger = RadixDialog.Trigger;
// eslint-disable-next-line react-refresh/only-export-components
export const DialogClose = RadixDialog.Close;

export function DialogContent({
  className,
  children,
  ...props
}: ComponentPropsWithoutRef<typeof RadixDialog.Content>) {
  return (
    <RadixDialog.Portal>
      <RadixDialog.Overlay className="fixed inset-0 z-40 bg-black/50 data-[state=open]:animate-in data-[state=open]:fade-in data-[state=closed]:animate-out data-[state=closed]:fade-out" />
      <RadixDialog.Content
        className={clsx(
          "fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-lg border p-6 shadow-xl",
          "border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900",
          "focus:outline-none",
          className,
        )}
        {...props}
      >
        {children}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  );
}

export function DialogTitle({ className, ...props }: ComponentPropsWithoutRef<typeof RadixDialog.Title>) {
  return (
    <RadixDialog.Title
      className={clsx("text-base font-semibold text-slate-900 dark:text-slate-100", className)}
      {...props}
    />
  );
}

export function DialogDescription({
  className,
  ...props
}: ComponentPropsWithoutRef<typeof RadixDialog.Description>) {
  return (
    <RadixDialog.Description
      className={clsx("mt-1 text-sm text-slate-500 dark:text-slate-400", className)}
      {...props}
    />
  );
}
