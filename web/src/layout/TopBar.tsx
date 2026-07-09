import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { ThemeToggle } from "./ThemeToggle";
import { Button } from "../components/Button";
import { useSession, SESSION_QUERY_KEY } from "../api/useSession";
import { useDemoSessionStore } from "../store/authStub";
import { logout } from "../api/auth";
import { useToast } from "../components/Toast";

export interface TopBarProps {
  onOpenHelp: () => void;
}

export function TopBar({ onOpenHelp }: TopBarProps) {
  const { data: session } = useSession();
  const demoSession = useDemoSessionStore((s) => s.demoSession);
  const exitDemoMode = useDemoSessionStore((s) => s.exitDemoMode);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { toast } = useToast();

  const displayName = demoSession ? "demo" : (session?.user.username ?? "");

  function handleSearchFocus(): void {
    toast({ title: "Search — not yet implemented", description: 'Press "/" or click here — the topology search index lands in a later task.' });
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
        onClick={handleSearchFocus}
        className="flex h-9 w-full max-w-sm items-center gap-2 rounded-md border border-slate-300 px-3 text-left text-sm text-slate-400 dark:border-slate-700 dark:text-slate-500"
      >
        <span aria-hidden>⌕</span>
        <span>Search VMs, MACs, IPs…</span>
        <kbd className="ml-auto rounded border border-slate-300 px-1 text-xs dark:border-slate-700">/</kbd>
      </button>

      <div className="ml-auto flex items-center gap-2">
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
