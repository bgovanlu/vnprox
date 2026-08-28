// SPDX-License-Identifier: Apache-2.0

// T-2005's "offline shell that fails honestly": renders nothing while
// online, and while offline says so explicitly and states the age of
// whatever data is on screen — a stale topology silently presented as
// current would be dangerous (the task card's own words), so this is the
// one place in the app that turns "the network request failed" into
// something a reader can't miss and can't misread as live.
//
// Deliberately in NORMAL DOCUMENT FLOW (not a fixed overlay) — mounted in
// AppShell right where DemoBanner already is, for the identical reason
// that component's own doc comment gives: a fixed banner collided with
// this app's own per-page toolbars and controls in T-2801's testing, and
// pushing content down instead of floating over it avoids the same class
// of bug here.
//
// T-3403: restyled to match DemoBanner's "banner family" shape — same
// pill-badge-plus-message layout, same padding/text scale — but kept on its
// own amber-warning surface (light amber wash / dark amber-950, unchanged
// colours) rather than DemoBanner's dark-navy one, so the two stay visually
// distinct when both are showing at once (a demo instance can absolutely
// go offline). The badge is new; the colour pairing it and the message
// text sit on is the same one this component shipped with, so no new
// contrast claim is being made here beyond the existing amber-on-amber
// pairing.
import { formatAge, useLastSuccessAt, useOnlineStatus } from "../lib/freshness";

export function OfflineShellBanner() {
  const online = useOnlineStatus();
  const lastSuccessAt = useLastSuccessAt();

  if (online) return null;

  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-amber-300 bg-amber-50 px-3 py-1.5 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200"
    >
      <span className="rounded-full bg-amber-500 px-2 py-0.5 text-xs font-semibold tracking-wide text-amber-950 uppercase">
        Offline
      </span>
      <span>
        {lastSuccessAt === null
          ? "No data has loaded yet — nothing on screen can be trusted as current."
          : `Showing data from ${formatAge(lastSuccessAt)}. Topology, findings, and every other live view may be out of date — do not treat them as current.`}
      </span>
    </div>
  );
}
