// SPDX-License-Identifier: Apache-2.0

import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { ChevronDown, CircleHelp, Keyboard, Search, Sparkles } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { ThemeToggle } from "./ThemeToggle";
import { Button } from "../components/Button";
import { Tooltip } from "../components/Tooltip";
import { useSession, SESSION_QUERY_KEY } from "../api/useSession";
import { useDemoSessionStore } from "../store/authStub";
import { logout } from "../api/auth";
import { useTopologyStore } from "../topology/store";
import { useAssistantStore } from "../assistant/store";

export interface TopBarProps {
  /** The `?` keyboard-shortcut list (ShortcutHelpDialog). */
  onOpenHelp: () => void;
  /** T-2204: contextual online help for the current screen (F1). */
  onOpenPageHelp: () => void;
}

export function TopBar({ onOpenHelp, onOpenPageHelp }: TopBarProps) {
  const { data: session } = useSession();
  const demoSession = useDemoSessionStore((s) => s.demoSession);
  const exitDemoMode = useDemoSessionStore((s) => s.exitDemoMode);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const setSpotlightOpen = useTopologyStore((s) => s.setSpotlightOpen);
  // T-2808: the assistant panel reads its own store (like the help panel),
  // so opening it needs no new prop on this component.
  const openAssistant = useAssistantStore((s) => s.openPanel);

  const displayName = demoSession ? "demo" : (session?.user.username ?? "");
  const chipLabel = displayName || "account";
  const chipInitial = chipLabel.charAt(0).toUpperCase();

  // Open the real spotlight search (GET /inventory/search — fuzzy across
  // names/MACs/IPs/VMIDs/comments). The search dialog lives on the topology
  // page (where selecting a result reveals the entity on the map), so from
  // any other page this navigates there first; the store flag is read on
  // mount, so setting it before navigating is enough. T-3403: this is also
  // exactly what the global "/" keyboard shortcut does off Topology now
  // (src/keyboard/useKeyboardShortcuts.ts) — the click trigger and the key
  // are two doors into the same behaviour.
  function openSearch(): void {
    setSpotlightOpen(true);
    void navigate("/topology");
  }

  async function handleLogout(): Promise<void> {
    if (demoSession) {
      exitDemoMode();
    } else {
      try {
        await logout();
      } catch {
        // Best-effort: even if the backend doesn't implement /auth/logout
        // yet, still drop the client-side session state below.
      }
    }
    await queryClient.invalidateQueries({ queryKey: SESSION_QUERY_KEY });
    void navigate("/login", { replace: true });
  }

  return (
    // T-4203: chrome framing the page, one level up from `surface-page` —
    // `surface-raised`, matching Sidebar.tsx. Previously `dark:bg-slate-950`
    // (darker than the page's `dark:bg-slate-900`), which is exactly the
    // "elevation by darkening" anti-pattern index.css's T-4203 comment
    // argues against — with no shadow to read in dark mode, a chrome bar
    // darker than the page it frames doesn't land as elevated at all.
    <header className="flex h-14 shrink-0 items-center gap-3 border-b border-border bg-surface-raised px-4">
      <button
        type="button"
        onClick={openSearch}
        aria-label="Search"
        // T-905: text-slate-400/dark:text-slate-500 (the original pairing)
        // failed axe's color-contrast check in dark mode (4.23:1 measured
        // against the header's then-background dark:bg-slate-950, below
        // WCAG AA's 4.5:1 minimum for this ~14px text) — swapped so each
        // mode gets the shade with adequate contrast against ITS
        // background (slate-500 reads clearly on the light header's white;
        // slate-400 reads clearly on the dark header), rather than the
        // same shade doing double duty across both. T-3403: kept
        // byte-for-byte — only the shape (rounded-md -> rounded-full) and
        // the glyph (a plain-text "⌕" -> a lucide Search icon) changed, so
        // this reasoning still applies unmodified. T-4203: the header's
        // dark-mode background moved from `dark:bg-slate-950` to the
        // unprefixed `bg-surface-raised`, whose dark value is #182133 and
        // so is lighter — re-measured, dark:text-slate-400 against it
        // is still 6.28:1, comfortably above the 4.5:1 floor this comment
        // is about.
        className="flex h-9 w-full max-w-sm items-center gap-2 rounded-full border border-border-strong px-3.5 text-left text-sm text-fg-subtle hover:border-slate-400 dark:hover:border-slate-600"
      >
        <Search aria-hidden className="h-4 w-4 shrink-0" />
        <span className="truncate">Search VMs, MACs, IPs…</span>
        <kbd className="ml-auto shrink-0 rounded border border-border-strong px-1 text-xs">/</kbd>
      </button>

      <div className="ml-auto flex items-center gap-1">
        {/* T-2204: two distinct affordances, deliberately not merged into
         * one menu — "what does this screen do" and "what are the keys"
         * are different questions, and burying the first behind a dropdown
         * is how help ends up unused. T-3403: icon-only ghost buttons with
         * a Tooltip carrying the explanation the visible label used to —
         * the aria-label (unchanged) still names each control for
         * assistive tech. */}
        <Tooltip content="Ask the assistant (read-only tools, your own permissions)">
          <Button variant="ghost" size="sm" onClick={openAssistant} aria-label="Assistant">
            <Sparkles aria-hidden className="h-4 w-4" />
          </Button>
        </Tooltip>
        <Tooltip content="Help for this screen (F1)">
          <Button variant="ghost" size="sm" onClick={onOpenPageHelp} aria-label="Help">
            <CircleHelp aria-hidden className="h-4 w-4" />
          </Button>
        </Tooltip>
        <Tooltip content="Keyboard shortcuts (?)">
          <Button variant="ghost" size="sm" onClick={onOpenHelp} aria-label="Keyboard shortcuts">
            <Keyboard aria-hidden className="h-4 w-4" />
          </Button>
        </Tooltip>
        <ThemeToggle />
        <div className="mx-1 h-6 w-px shrink-0 bg-slate-200 dark:bg-slate-800" aria-hidden />
        <RadixDropdown.Root>
          <RadixDropdown.Trigger asChild>
            <Button
              variant="secondary"
              size="sm"
              className="gap-1.5 rounded-full pl-1.5 pr-2.5"
              aria-label={`Account menu (${chipLabel})`}
            >
              <span
                aria-hidden
                className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent-600 text-[10px] font-semibold text-white"
              >
                {chipInitial}
              </span>
              <span className="max-w-[8rem] truncate">{chipLabel}</span>
              <ChevronDown aria-hidden className="h-3.5 w-3.5 shrink-0 text-fg-subtle" />
            </Button>
          </RadixDropdown.Trigger>
          <RadixDropdown.Portal>
            <RadixDropdown.Content
              align="end"
              sideOffset={6}
              // T-4203: a floating menu above the chrome — `surface-overlay`.
              className="z-50 min-w-[10rem] rounded-lg border border-border bg-surface-overlay p-1 shadow-lg"
            >
              {demoSession ? (
                <div className="px-2 py-1.5 text-xs text-fg-subtle">demo mode</div>
              ) : null}
              <RadixDropdown.Item
                onSelect={() => {
                  void handleLogout();
                }}
                className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                Log out
              </RadixDropdown.Item>
            </RadixDropdown.Content>
          </RadixDropdown.Portal>
        </RadixDropdown.Root>
      </div>
    </header>
  );
}
