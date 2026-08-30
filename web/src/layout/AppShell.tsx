// SPDX-License-Identifier: Apache-2.0

import { useState } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { Sidebar } from "./Sidebar";
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
import { AssistantPanel } from "../assistant/AssistantPanel";
import { useHelpForRoute } from "../help/useHelpForRoute";
import { DemoBanner } from "../demo/DemoBanner";
import { GuidedTour } from "../tour/GuidedTour";
import { OfflineShellBanner } from "./OfflineShellBanner";
import { PushNavigationBridge } from "../push/PushNavigationBridge";
import { GuestEgoPaletteHost } from "../guest/GuestEgoPaletteHost";

/** Top-level layout for every authenticated route: Sidebar + top bar
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
    // T-4203: the app's page-level ground — the sidebar/top bar chrome sit
    // above it at `surface-raised` (Sidebar.tsx/TopBar.tsx); floating
    // layers (dialogs, drawers, the inspector stack) sit above that at
    // `surface-overlay`. Correct in both themes with no `dark:` prefix.
    <div className="flex h-dvh w-full bg-surface-page text-fg">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onOpenHelp={() => { setHelpOpen(true); }} onOpenPageHelp={openPageHelp} />
        {/* T-2801: on EVERY authenticated screen, because every authenticated
         * screen is routed through this shell. Renders nothing outside demo
         * mode. Placed directly under the TopBar and in normal flow for the
         * same reason OnboardingWalkthrough below is — see its comment. */}
        <DemoBanner />
        {/* T-2005: offline + stale-data labeling, in normal flow next to
         * DemoBanner for the same collision-avoidance reason (see this
         * component's own doc comment). Renders nothing while online. */}
        <OfflineShellBanner />
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
        {/* T-2802: the hosted demo's guided tour. Renders nothing on any
         * instance that is not the public demo — including a local
         * `vnproxd --demo`, which has no edge in front of it. Placed next
         * to OnboardingWalkthrough because it is the same kind of thing in
         * the same flow position; the two are mutually exclusive in
         * practice (the walkthrough's steps write, so a read-only public
         * instance never gets past its first). */}
        <GuidedTour />
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
                <p className="mt-2 text-sm text-fg-subtle">
                  Something went wrong rendering this view. Other pages in the nav still work; reload to try again.
                </p>
                <button
                  type="button"
                  onClick={() => { window.location.reload(); }}
                  className="mt-4 rounded-md border border-border-strong px-3 py-1.5 text-sm hover:bg-slate-50 dark:hover:bg-slate-800"
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
      {/* T-2808: the in-app assistant over the MCP read tools. Mounted once,
       * app-wide, like HelpPanel — it is opened from the top bar and holds
       * no conversation state anywhere but its own component state. */}
      <AssistantPanel />
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
      <ChangesetDrawer />
      <MgmtWizardHost />
      <ConnectClustersWizardHost />
      <MgmtProtectedRefreshPrompt />
      {/* T-2005: relays a push notification's deep link (posted by
       * web/public/sw.js's notificationclick handler to an already-open
       * tab) into an in-app navigation, rather than a full page reload. */}
      <PushNavigationBridge />
      {/* T-3906: registers "Open guest view for <ref>" in the command
       * palette whenever a guest is the map's current selection — see this
       * component's own doc comment for why the palette, not an
       * InspectorPanel button, is this task's map entry point. */}
      <GuestEgoPaletteHost />
    </div>
  );
}
