// T-909's route guard: every route outside the narrow-viewport reachable
// set (Dashboard, Findings/`/tools`, and the changeset confirm/rollback
// overlay — which is mounted app-wide in AppShell, not routed) is wrapped
// in this at the App.tsx route table, so navigating to it on a narrow
// viewport — including via a direct/bookmarked link, not just an in-app
// nav click — renders the explicit DesktopOnlyNotice instead of a broken or
// cramped attempt at the full page. NavRail additionally stops linking to
// these routes at narrow width (belt-and-suspenders: the guard is the
// actual enforcement, the nav change just avoids dangling the temptation).
import type { ReactNode } from "react";
import { useNarrowViewport } from "../lib/useNarrowViewport";
import { DesktopOnlyNotice } from "./DesktopOnlyNotice";

export interface DesktopOnlyRouteProps {
  /** The page's display name, used in the notice's copy. */
  pageLabel: string;
  /** Optional page-specific detail, e.g. naming a wizard by name. */
  detail?: string;
  children: ReactNode;
}

export function DesktopOnlyRoute({ pageLabel, detail, children }: DesktopOnlyRouteProps) {
  const narrow = useNarrowViewport();
  if (narrow) {
    return <DesktopOnlyNotice pageLabel={pageLabel} detail={detail} />;
  }
  return <>{children}</>;
}
