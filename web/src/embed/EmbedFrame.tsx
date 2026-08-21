// EmbedFrame (T-1706): the boot wrapper every /embed/* route renders inside.
// It pulls the embed token from the `?token=` query string, stashes it via
// setEmbedToken so the shared apiFetch wrapper authenticates with it (and
// never the session cookie), and renders a chrome-free read-only shell around
// the requested view. There is deliberately NO AppShell, NO RequireAuth, and
// NO navigation/action chrome here: an embed is a read-only pane for a wiki,
// a NOC screen, or a status page, so every mutation entry point is absent at
// the component level (not hidden via CSS) — the security ceiling the task
// card requires.
import type { ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { setEmbedToken } from "./embedToken";
import { DemoBanner } from "../demo/DemoBanner";

interface EmbedFrameProps {
  title: string;
  children: ReactNode;
}

export function EmbedFrame({ title, children }: EmbedFrameProps) {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";

  // Set synchronously during render (before children issue any query) so the
  // first apiFetch already carries the bearer token. setEmbedToken is
  // idempotent and holds no React state, so this is safe to call on every
  // render.
  setEmbedToken(token);

  if (!token) {
    return (
      <div
        data-testid="embed-missing-token"
        className="flex h-full min-h-screen items-center justify-center bg-white p-6 text-sm text-slate-600 dark:bg-slate-950 dark:text-slate-300"
      >
        This embed link is missing its access token.
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-screen flex-col bg-white text-slate-900 dark:bg-slate-950 dark:text-slate-100">
      {/* T-2801: an embed is a screen someone put on a wall. If it is a
       * demo, the wall must say so. */}
      <DemoBanner />
      <header className="flex items-center justify-between border-b border-slate-200 px-4 py-2 dark:border-slate-800">
        <span className="text-sm font-semibold">{title}</span>
        <span className="text-xs uppercase tracking-wide text-slate-600 dark:text-slate-400" data-testid="embed-readonly-badge">
          Read-only
        </span>
      </header>
      <main className="min-h-0 flex-1 overflow-auto p-4">{children}</main>
    </div>
  );
}
