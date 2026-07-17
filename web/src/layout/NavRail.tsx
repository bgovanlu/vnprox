import { NavLink } from "react-router-dom";
import clsx from "clsx";
import { useFindingsQuery } from "../findings/queries";
import { useNarrowViewport } from "../lib/useNarrowViewport";

interface NavItem {
  path: string;
  label: string;
  /** Single-letter glyph — a stand-in for real icons (no icon library is
   * in docs/development.md's stack table, so this avoids adding one just
   * for placeholder pages). */
  glyph: string;
}

const NAV_ITEMS: NavItem[] = [
  // "⌂" rather than "H" (already used by History below) to avoid two nav
  // items sharing a glyph.
  { path: "/", label: "Home", glyph: "⌂" },
  { path: "/topology", label: "Topology", glyph: "T" },
  { path: "/management", label: "Management", glyph: "M" },
  { path: "/guests", label: "Guests", glyph: "V" },
  { path: "/sdn", label: "SDN", glyph: "S" },
  { path: "/firewall", label: "Firewall", glyph: "F" },
  { path: "/ipam", label: "IPAM", glyph: "I" },
  { path: "/ports", label: "Ports", glyph: "P" },
  { path: "/blueprints", label: "Blueprints", glyph: "B" },
  { path: "/history", label: "History", glyph: "H" },
  { path: "/audit", label: "Audit", glyph: "A" },
  { path: "/tools", label: "Tools", glyph: "L" },
  { path: "/settings", label: "Settings", glyph: "G" },
];

/** T-909: the narrow-viewport reachable page set — everything else is
 * behind DesktopOnlyRoute (App.tsx), so linking to it from the nav rail at
 * narrow width would just dangle a route that immediately bounces to the
 * "desktop only" notice. Findings lives at the Tools path (label kept as
 * "Tools" here to match the one nav item both widths share; the page
 * itself renders a Findings-only view at narrow width — see
 * ToolsPage.tsx). */
const NARROW_REACHABLE_PATHS: ReadonlySet<string> = new Set(["/", "/tools"]);

/** T-602: the current unified findings count (drift+lldp+ipam+health, not
 * just drift — see findings/queries.ts), shown as a small pill next to the
 * Tools nav item (where the findings stream itself lives — see
 * ToolsPage.tsx). Zero/loading/error all render as "no badge" rather than
 * a misleading "0" or an error state in the nav chrome. */
function FindingsCountBadge() {
  const { data } = useFindingsQuery();
  const count = data?.length ?? 0;
  if (count === 0) return null;
  return (
    <span
      aria-label={`${String(count)} finding${count === 1 ? "" : "s"}`}
      className="ml-auto hidden shrink-0 rounded-full bg-amber-500/90 px-1.5 py-0.5 text-[10px] font-semibold text-white sm:inline-block"
    >
      {count}
    </span>
  );
}

export function NavRail() {
  const narrow = useNarrowViewport();
  const items = narrow ? NAV_ITEMS.filter((item) => NARROW_REACHABLE_PATHS.has(item.path)) : NAV_ITEMS;

  return (
    <nav
      aria-label="Primary"
      // T-909: `relative z-50` — found via this task's own e2e run. The
      // changeset countdown/outcome banner (CountdownBanner.tsx) is a
      // `fixed inset-x-0 top-0 z-40` bar; with no z-index of its own
      // (position: static), NavRail painted *underneath* it wherever the
      // two visually overlapped, making early nav items unclickable while
      // a banner was showing. Harmless at every width (was already the
      // case whenever a countdown/outcome banner appeared, not just at
      // narrow width) but only surfaced once a real test combined "banner
      // showing" with "click a nav rail link" — at narrow width, the
      // reachable-set filter below clusters the remaining items (Home,
      // Tools) right under the banner's full height, where the previous
      // desktop-only 13-item list mostly didn't reach. z-50 matches this
      // app's dialog/drawer tier so modals (portaled after NavRail in the
      // DOM, so already painting on top regardless) are unaffected.
      className="relative z-50 flex w-16 shrink-0 flex-col items-center gap-1 border-r border-slate-200 bg-slate-50 py-3 dark:border-slate-800 dark:bg-slate-950 sm:w-48 sm:items-stretch sm:px-2"
    >
      <div className="mb-2 hidden px-2 text-sm font-semibold tracking-wide text-slate-500 dark:text-slate-400 sm:block">
        vnprox
      </div>
      {items.map((item) => (
        <NavLink
          key={item.path}
          to={item.path}
          // "/" is a prefix of every route, so without `end` this item
          // would render active on every page, not just the dashboard
          // itself (react-router's NavLink `end` prop docs).
          end={item.path === "/"}
          // T-909: below `sm` (640px) the text label span is `hidden`
          // (icon-only rail) — without an explicit name here the link's
          // accessible name would compute to empty (the glyph span is
          // `aria-hidden`, and a `display:none` descendant is excluded
          // from accessible-name content), which matters more once this
          // rail is one of the only two ways to reach a page on a narrow
          // viewport.
          aria-label={item.label}
          className={({ isActive }) =>
            clsx(
              "flex items-center gap-3 rounded-md px-2.5 py-2 text-sm font-medium transition-colors",
              isActive
                ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
                : "text-slate-600 hover:bg-slate-200/60 dark:text-slate-300 dark:hover:bg-slate-800/60",
            )
          }
        >
          <span
            aria-hidden
            className="flex h-6 w-6 shrink-0 items-center justify-center rounded bg-slate-200 text-xs font-semibold dark:bg-slate-800"
          >
            {item.glyph}
          </span>
          <span className="hidden sm:inline">{item.label}</span>
          {item.path === "/tools" && <FindingsCountBadge />}
        </NavLink>
      ))}
    </nav>
  );
}
