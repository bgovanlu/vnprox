import { useState } from "react";
import { Outlet } from "react-router-dom";
import { NavRail } from "./NavRail";
import { TopBar } from "./TopBar";
import { useKeyboardShortcuts } from "../keyboard/useKeyboardShortcuts";
import { ShortcutHelpDialog } from "../keyboard/ShortcutHelpDialog";

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
        <main className="min-w-0 flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
      <ShortcutHelpDialog open={helpOpen} onOpenChange={setHelpOpen} />
    </div>
  );
}
