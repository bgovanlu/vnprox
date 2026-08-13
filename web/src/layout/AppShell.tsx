import { useState } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { NavRail } from "./NavRail";
import { TopBar } from "./TopBar";
import { useKeyboardShortcuts } from "../keyboard/useKeyboardShortcuts";
import { ShortcutHelpDialog } from "../keyboard/ShortcutHelpDialog";
import { CommandPalette } from "../keyboard/CommandPalette";
import { ChangesetDrawer } from "../changesets/ChangesetDrawer";
import { OnboardingWalkthrough } from "../onboarding/OnboardingWalkthrough";
import { MgmtWizardHost } from "../mgmt/MgmtWizardHost";
import { MgmtProtectedRefreshPrompt } from "../mgmt/MgmtProtectedRefreshPrompt";
import { ConnectClustersWizardHost } from "../wireguard/ConnectClustersWizardHost";
import { HelpPanel } from "../help/HelpPanel";
import { useHelpForRoute } from "../help/useHelpForRoute";
import { DemoBanner } from "../demo/DemoBanner";

/** Top-level layout for every authenticated route: nav rail + top bar
 * around a routed <Outlet/>, with the keyboard-shortcut framework wired
 * up app-wide (see docs/user-guide.md §6). */
export function AppShell() {
  const [helpOpen, setHelpOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const location = useLocation();
  // T-2204: contextual online help for whatever screen is routed right now.
  // Separate from `helpOpen`/ShortcutHelpDialog above, which stays exactly
  // what it was — the `?` keyboard-shortcut list.
  const openPageHelp = useHelpForRoute();

  useKeyboardShortcuts({
    onOpenHelp: () => { setHelpOpen(true); },
    onOpenPalette: () => { setPaletteOpen(true); },
    onOpenPageHelp: openPageHelp,
  });

  return (
    <div className="flex h-dvh w-full bg-slate-100 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
      <NavRail />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onOpenHelp={() => { setHelpOpen(true); }} onOpenPageHelp={openPageHelp} />
        {/* T-2801: on EVERY authenticated screen, because every authenticated
         * screen is routed through this shell. Renders nothing outside demo
         * mode. Placed directly under the TopBar and in normal flow for the
         * same reason OnboardingWalkthrough below is — see its comment. */}
        <DemoBanner />
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
        {/* T-909: tighter padding below `sm` (640px) — p-6 (24px) eats
         * noticeably into a 375-414px phone viewport's usable width; p-3
         * keeps content from feeling cramped against the screen edge
         * without changing anything at `sm` and up. */}
        <main className="min-w-0 flex-1 overflow-auto p-3 sm:p-6">
          {/* A page-level boundary so one view's render crash degrades to a
              recoverable message instead of blanking the whole app. Keyed on
              the path so navigating to another page resets it. */}
          <ErrorBoundary
            key={location.pathname}
            label={`page:${location.pathname}`}
            fallback={
              <div className="mx-auto max-w-md py-16 text-center">
                <h2 className="text-lg font-semibold">This page hit an error</h2>
                <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
                  Something went wrong rendering this view. Other pages in the nav still work; reload to try again.
                </p>
                <button
                  type="button"
                  onClick={() => { window.location.reload(); }}
                  className="mt-4 rounded-md border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800"
                >
                  Reload
                </button>
              </div>
            }
          >
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>
      <ShortcutHelpDialog open={helpOpen} onOpenChange={setHelpOpen} />
      <HelpPanel />
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
      <ChangesetDrawer />
      <MgmtWizardHost />
      <ConnectClustersWizardHost />
      <MgmtProtectedRefreshPrompt />
    </div>
  );
}
