import { NavLink } from "react-router-dom";
import clsx from "clsx";
import { useFindingsQuery } from "../findings/queries";

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
  return (
    <nav
      aria-label="Primary"
      className="flex w-16 shrink-0 flex-col items-center gap-1 border-r border-slate-200 bg-slate-50 py-3 dark:border-slate-800 dark:bg-slate-950 sm:w-48 sm:items-stretch sm:px-2"
    >
      <div className="mb-2 hidden px-2 text-sm font-semibold tracking-wide text-slate-500 dark:text-slate-400 sm:block">
        vnprox
      </div>
      {NAV_ITEMS.map((item) => (
        <NavLink
          key={item.path}
          to={item.path}
          // "/" is a prefix of every route, so without `end` this item
          // would render active on every page, not just the dashboard
          // itself (react-router's NavLink `end` prop docs).
          end={item.path === "/"}
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
