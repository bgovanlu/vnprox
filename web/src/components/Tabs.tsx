// SPDX-License-Identifier: Apache-2.0

// T-3404's shared underlined-tabs wrapper over the already-approved
// @radix-ui/react-tabs dependency (docs/development.md "Visual language,
// Phase 34, T-3401": "Tabs are underlined, not boxed or pill-segmented:
// muted label, accent underline on the active one"). Re-exports Root/
// Content as-is (Radix's own primitives already have the right behavior —
// keyboard arrow-key navigation, roving tabindex, ARIA roles) and only
// restyles List/Trigger, so migrating a call site is typically a rename
// from `RadixTabs.X` to `TabsX` with no behavior change. Flat named exports
// (`TabsRoot`/`TabsList`/`TabsTrigger`/`TabsContent`) rather than a
// `Tabs.X` namespace object, matching this codebase's own Dialog.tsx/
// Drawer.tsx convention (`Dialog`/`DialogContent`/`DialogTitle`, all flat
// named exports) — a namespace object mixed into a component file also
// trips `react-refresh/only-export-components` for every export in the
// file, not just the object itself.
//
// Deliberately page-level sub-navigation only (SdnPage's view switch,
// FirewallPage's scope switch, GovernancePage's section switch, HubPage's
// artifact-type switch) — panel-internal tab strips that live inside a
// drawer or a side inspector (changesets/ReviewApplyScreen.tsx,
// topology/InspectorPanel.tsx, topology/InspectorCompareView.tsx) keep
// their own local Radix usage; they aren't a page's own navigation and
// migrating them wasn't this task's scope.
import type { ComponentPropsWithoutRef } from "react";
import * as RadixTabs from "@radix-ui/react-tabs";
import clsx from "clsx";

// eslint-disable-next-line react-refresh/only-export-components
export const TabsRoot = RadixTabs.Root;
// eslint-disable-next-line react-refresh/only-export-components
export const TabsContent = RadixTabs.Content;

export type TabsListProps = ComponentPropsWithoutRef<typeof RadixTabs.List>;
export type TabsTriggerProps = ComponentPropsWithoutRef<typeof RadixTabs.Trigger>;

export function TabsList({ className, ...props }: TabsListProps) {
  return (
    <RadixTabs.List
      className={clsx("flex items-center gap-1 border-b border-slate-200 dark:border-slate-800", className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: TabsTriggerProps) {
  return (
    <RadixTabs.Trigger
      className={clsx(
        // T-3406: text-slate-500 measures 4.34:1 wherever this renders
        // directly on AppShell's `bg-slate-100` canvas (every page's own
        // PageHeader `tabs` slot does exactly that — no white card behind
        // it), below the 4.5:1 AA floor. Found by the full-sweep axe run
        // on SdnPage/FirewallPage/GovernancePage/HubPage, the only four
        // consumers (see this file's own header comment). slate-600 clears
        // it; dark mode is unaffected.
        "-mb-px border-b-2 border-transparent px-3 py-1.5 text-sm font-medium text-fg-muted transition-colors",
        "hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200",
        "data-[state=active]:border-accent-600 data-[state=active]:text-accent-700",
        "dark:data-[state=active]:border-accent-500 dark:data-[state=active]:text-accent-400",
        "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
        className,
      )}
      {...props}
    />
  );
}
