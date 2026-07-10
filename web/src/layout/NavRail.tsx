import { NavLink } from "react-router-dom";
import clsx from "clsx";

interface NavItem {
  path: string;
  label: string;
  /** Single-letter glyph — a stand-in for real icons (no icon library is
   * in docs/development.md's stack table, so this avoids adding one just
   * for placeholder pages). */
  glyph: string;
}

const NAV_ITEMS: NavItem[] = [
  { path: "/topology", label: "Topology", glyph: "T" },
  { path: "/guests", label: "Guests", glyph: "V" },
  { path: "/sdn", label: "SDN", glyph: "S" },
  { path: "/firewall", label: "Firewall", glyph: "F" },
  { path: "/ipam", label: "IPAM", glyph: "I" },
  { path: "/history", label: "History", glyph: "H" },
  { path: "/audit", label: "Audit", glyph: "A" },
  { path: "/tools", label: "Tools", glyph: "L" },
  { path: "/settings", label: "Settings", glyph: "G" },
];

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
        </NavLink>
      ))}
    </nav>
  );
}
