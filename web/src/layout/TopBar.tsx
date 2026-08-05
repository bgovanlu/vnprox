import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { ThemeToggle } from "./ThemeToggle";
import { Button } from "../components/Button";
import { useSession, SESSION_QUERY_KEY } from "../api/useSession";
import { useDemoSessionStore } from "../store/authStub";
import { logout } from "../api/auth";
import { useTopologyStore } from "../topology/store";

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

  const displayName = demoSession ? "demo" : (session?.user.username ?? "");

  // Open the real spotlight search (GET /inventory/search — fuzzy across
  // names/MACs/IPs/VMIDs/comments). The search dialog lives on the topology
  // page (where selecting a result reveals the entity on the map), so from
  // any other page this navigates there first; the store flag is read on
  // mount, so setting it before navigating is enough.
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
    <header className="flex h-14 shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-4 dark:border-slate-800 dark:bg-slate-950">
      <button
        type="button"
        onClick={openSearch}
        aria-label="Search"
        // T-905: text-slate-400/dark:text-slate-500 (the original pairing)
        // failed axe's color-contrast check in dark mode (4.23:1 measured
        // against the header's dark:bg-slate-950, below WCAG AA's 4.5:1
        // minimum for this ~14px text) — swapped so each mode gets the
        // shade with adequate contrast against ITS background (slate-500
        // reads clearly on the light header's white; slate-400 reads
        // clearly on the dark header's near-black), rather than the same
        // shade doing double duty across both.
        className="flex h-9 w-full max-w-sm items-center gap-2 rounded-md border border-slate-300 px-3 text-left text-sm text-slate-500 hover:border-slate-400 dark:border-slate-700 dark:text-slate-400 dark:hover:border-slate-600"
      >
        <span aria-hidden>⌕</span>
        <span>Search VMs, MACs, IPs…</span>
        <kbd className="ml-auto rounded border border-slate-300 px-1 text-xs dark:border-slate-700">/</kbd>
      </button>

      <div className="ml-auto flex items-center gap-2">
        {/* T-2204: two distinct affordances, deliberately not merged into
         * one menu — "what does this screen do" and "what are the keys"
         * are different questions, and burying the first behind a dropdown
         * is how help ends up unused. */}
        <Button variant="ghost" size="sm" onClick={onOpenPageHelp} aria-label="Help" title="Help for this screen (F1)">
          Help
        </Button>
        <Button variant="ghost" size="sm" onClick={onOpenHelp} aria-label="Keyboard shortcuts" title="Keyboard shortcuts (?)">
          ?
        </Button>
        <ThemeToggle />
        <RadixDropdown.Root>
          <RadixDropdown.Trigger asChild>
            <Button variant="ghost" size="sm">
              {displayName || "account"}
            </Button>
          </RadixDropdown.Trigger>
          <RadixDropdown.Portal>
            <RadixDropdown.Content
              align="end"
              sideOffset={6}
              className="z-50 min-w-[10rem] rounded-md border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900"
            >
              {demoSession ? (
                <div className="px-2 py-1.5 text-xs text-slate-500 dark:text-slate-400">demo mode</div>
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
