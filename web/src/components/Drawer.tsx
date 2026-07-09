import type { ComponentPropsWithoutRef } from "react";
import * as RadixDialog from "@radix-ui/react-dialog";
import clsx from "clsx";

// Radix has no dedicated "drawer/sheet" primitive; a drawer is a Dialog
// anchored to an edge of the viewport instead of centered, so it's built
// on the same @radix-ui/react-dialog primitive as components/Dialog.tsx
// (same focus-trap/portal/escape-to-close behavior for free). Used for
// the change drawer described in docs/user-guide.md §3 ("Edits collect
// in the change drawer (bottom right)").
//
// react-refresh's only-export-components rule can't see that these
// re-exported references are themselves components (see the identical
// note in components/Dialog.tsx); disabled per-line, accepted tradeoff.
// eslint-disable-next-line react-refresh/only-export-components
export const Drawer = RadixDialog.Root;
// eslint-disable-next-line react-refresh/only-export-components
export const DrawerTrigger = RadixDialog.Trigger;
// eslint-disable-next-line react-refresh/only-export-components
export const DrawerClose = RadixDialog.Close;

export type DrawerSide = "right" | "left" | "bottom";

const sideClasses: Record<DrawerSide, string> = {
  right: "inset-y-0 right-0 h-full w-full max-w-md border-l",
  left: "inset-y-0 left-0 h-full w-full max-w-md border-r",
  bottom: "inset-x-0 bottom-0 max-h-[80vh] w-full border-t",
};

export interface DrawerContentProps extends ComponentPropsWithoutRef<typeof RadixDialog.Content> {
  side?: DrawerSide;
}

export function DrawerContent({ className, side = "right", children, ...props }: DrawerContentProps) {
  return (
    <RadixDialog.Portal>
      <RadixDialog.Overlay className="fixed inset-0 z-40 bg-black/50" />
      <RadixDialog.Content
        className={clsx(
          "fixed z-50 flex flex-col overflow-y-auto p-6 shadow-xl",
          "border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900",
          "focus:outline-none",
          sideClasses[side],
          className,
        )}
        {...props}
      >
        {children}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  );
}

// eslint-disable-next-line react-refresh/only-export-components
export const DrawerTitle = RadixDialog.Title;
// eslint-disable-next-line react-refresh/only-export-components
export const DrawerDescription = RadixDialog.Description;
