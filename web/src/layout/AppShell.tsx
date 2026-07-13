import { useState } from "react";
import { Outlet } from "react-router-dom";
import { NavRail } from "./NavRail";
import { TopBar } from "./TopBar";
import { useKeyboardShortcuts } from "../keyboard/useKeyboardShortcuts";
import { ShortcutHelpDialog } from "../keyboard/ShortcutHelpDialog";
import { ChangesetDrawer } from "../changesets/ChangesetDrawer";
import { OnboardingWalkthrough } from "../onboarding/OnboardingWalkthrough";
import { MgmtWizardHost } from "../mgmt/MgmtWizardHost";
import { MgmtProtectedRefreshPrompt } from "../mgmt/MgmtProtectedRefreshPrompt";

/** Top-level layout for every authenticated route: nav rail + top bar
 * around a routed <Outlet/>, with the keyboard-shortcut framework wired
 * up app-wide (see docs/user-guide.md §6). */
export function AppShell() {
  const [helpOpen, setHelpOpen] = useState(false);

  useKeyboardShortcuts({ onOpenHelp: () => { setHelpOpen(true); } });

  return (
    <div className="flex h-dvh w-full bg-slate-100 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
      <NavRail />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onOpenHelp={() => { setHelpOpen(true); }} />
        {/* Rendered in normal document flow, between TopBar and <main> —
         * not a fixed overlay — so it pushes page content down instead of
         * floating on top of it. Every page's own top-row controls (the
         * topology page's "New ▾"/"Search" toolbar, React Flow's bottom-
         * left zoom/fit-view Controls, ChangesetDrawer's bottom-right
         * corner) turned out to collide with every fixed-position corner
         * this was tried in (found via the Playwright e2e suite: it
         * intercepted pointer events on the map's own controls) — "never
         * blocks navigation" per the task card is satisfied by pushing
         * content down (still fully visible and clickable, one scroll or
         * an obviously-adjacent element) rather than by finding an
         * unoccupied floating corner, since this app's pages don't
         * reliably have one. */}
        <OnboardingWalkthrough />
        <main className="min-w-0 flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
      <ShortcutHelpDialog open={helpOpen} onOpenChange={setHelpOpen} />
      <ChangesetDrawer />
      <MgmtWizardHost />
      <MgmtProtectedRefreshPrompt />
    </div>
  );
}
