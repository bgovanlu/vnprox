// T-3402: replaces NavRail.tsx with the Stripe-dashboard idiom (see
// docs/development.md's "Visual language (Phase 34, T-3401)" section) — a
// light, grouped sidebar with muted section labels, collapsible groups,
// real icons, an instance-identity chip up top, and Settings pinned at the
// bottom, visually separated by a hairline border.
import { useEffect } from "react";
import { NavLink, useLocation } from "react-router-dom";
import clsx from "clsx";
import {
  Activity,
  AlertTriangle,
  Binary,
  Boxes,
  ChartLine,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  EthernetPort,
  FileCode,
  History,
  House,
  LayoutTemplate,
  Network,
  Route,
  Router,
  Scale,
  Server,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Waypoints,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import { useFindingsQuery } from "../findings/queries";
import { useNarrowViewport } from "../lib/useNarrowViewport";
import { useIsDemo } from "../demo/useDemoMode";
import {
  isSidebarGroupExpanded,
  useSidebarGroupsStore,
  type SidebarGroupId,
} from "./sidebarGroupsStore";

interface NavItem {
  path: string;
  label: string;
  icon: LucideIcon;
}

interface NavGroup {
  id: SidebarGroupId;
  label: string;
  items: NavItem[];
}

/** Flat primary items: no group label, rendered above every collapsible
 * section (Stripe's "Home / Balances / Payments" top block). */
const FLAT_ITEMS: NavItem[] = [
  // "end" on "/" is applied at render time (NavLink's own `end` prop) — "/"
  // is a prefix of every route, so without it this item would render active
  // on every page, not just the dashboard itself.
  { path: "/", label: "Home", icon: House },
  { path: "/topology", label: "Topology", icon: Waypoints },
  { path: "/guests", label: "Guests", icon: Server },
  { path: "/management", label: "Management", icon: SlidersHorizontal },
];

const NAV_GROUPS: NavGroup[] = [
  {
    id: "network",
    label: "Network",
    items: [
      { path: "/sdn", label: "SDN", icon: Network },
      { path: "/firewall", label: "Firewall", icon: ShieldCheck },
      { path: "/ipam", label: "IPAM", icon: Binary },
      { path: "/ports", label: "Ports", icon: EthernetPort },
      { path: "/edge", label: "Edge", icon: Router },
      { path: "/flows", label: "Flows", icon: Route },
      { path: "/conntrack", label: "Conntrack", icon: Activity },
    ],
  },
  {
    id: "operate",
    label: "Operate",
    items: [
      { path: "/history", label: "History", icon: History },
      { path: "/incidents", label: "Incidents", icon: AlertTriangle },
      { path: "/audit", label: "Audit", icon: ClipboardList },
      { path: "/analysis", label: "Analysis", icon: ChartLine },
      // T-602: keeps the findings-count badge (see FindingsCountBadge
      // below) — Findings itself lives at this path (ToolsPage.tsx).
      { path: "/tools", label: "Tools", icon: Wrench },
    ],
  },
  {
    id: "automate",
    label: "Automate",
    items: [
      { path: "/config-as-code", label: "Config as code", icon: FileCode },
      { path: "/governance", label: "Governance", icon: Scale },
      { path: "/blueprints", label: "Blueprints", icon: LayoutTemplate },
      { path: "/hub", label: "Hub", icon: Boxes },
    ],
  },
];

/** Settings, pinned at the bottom (Stripe's "Developers" slot) — never
 * inside a collapsible group. */
const SETTINGS_ITEM: NavItem = { path: "/settings", label: "Settings", icon: Settings };

/** Every item this sidebar can render, flattened once for the narrow-
 * viewport filter (T-909) below — grouping is a >=768px-only affordance;
 * at narrow width the reachable set is what it always was. */
const ALL_ITEMS: NavItem[] = [...FLAT_ITEMS, ...NAV_GROUPS.flatMap((g) => g.items), SETTINGS_ITEM];

/** T-909: the narrow-viewport reachable page set — everything else is
 * behind DesktopOnlyRoute (App.tsx), so linking to it from the sidebar at
 * narrow width would just dangle a route that immediately bounces to the
 * "desktop only" notice. Findings lives at the Tools path (label kept as
 * "Tools" here to match the one nav item both widths share; the page
 * itself renders a Findings-only view at narrow width — see
 * ToolsPage.tsx). */
const NARROW_REACHABLE_PATHS: ReadonlySet<string> = new Set(["/", "/tools"]);

/** True iff `pathname` is `itemPath` or a sub-route of it — used only to
 * decide whether a group containing the active route should auto-expand;
 * NavLink computes its own (equivalent) active styling independently. */
function isItemActive(itemPath: string, pathname: string): boolean {
  if (itemPath === "/") return pathname === "/";
  return pathname === itemPath || pathname.startsWith(`${itemPath}/`);
}

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
      // T-2004-fix: white on `bg-amber-500/90` measured 2.61:1 against the
      // sidebar's background — well below WCAG AA's 4.5:1 for this 10px
      // text, and it failed axe on every single page because the badge
      // lives in the chrome. Dark text on the same amber measures ~9.7:1,
      // which keeps the badge's warning colour and visual weight rather
      // than darkening it into something that reads as an error. The
      // opacity is dropped too: `/90` let the rail's background bleed
      // through and shift the effective ratio depending on theme.
      className="ml-auto hidden shrink-0 rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold text-slate-900 sm:inline-block"
    >
      {count}
    </span>
  );
}

/** One flat/grouped nav item. Shared between the full (>=768px, grouped)
 * render and the narrow (T-909, flat-filtered) render below. */
function NavItemLink({ item }: { item: NavItem }) {
  const Icon = item.icon;
  return (
    <NavLink
      to={item.path}
      end={item.path === "/"}
      // T-909: below `sm` (640px) the text label span is `hidden`
      // (icon-only rail) — without an explicit name here the link's
      // accessible name would compute to empty (the icon is `aria-hidden`,
      // and a `display:none` descendant is excluded from accessible-name
      // content), which matters more once this sidebar is one of the only
      // two ways to reach a page on a narrow viewport.
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
      <Icon aria-hidden className="h-4 w-4 shrink-0" />
      <span className="hidden truncate sm:inline">{item.label}</span>
      {item.path === "/tools" && <FindingsCountBadge />}
    </NavLink>
  );
}

/** One collapsible group: a disclosure button (muted uppercase label +
 * chevron, `aria-expanded`) followed by its items when expanded. Standard
 * disclosure semantics (WAI-ARIA APG): collapsed hides the panel from the
 * accessibility tree entirely rather than leaving it present-but-invisible
 * — a screen-reader/keyboard user gets the same "these items aren't
 * currently offered" signal a sighted user gets from the chevron. The
 * active-route auto-expand effect below is what keeps this from ever
 * hiding the page the user is actually on. */
function NavGroupSection({ group, currentPath }: { group: NavGroup; currentPath: string }) {
  const expanded = useSidebarGroupsStore((s) => s.expanded);
  const setExpanded = useSidebarGroupsStore((s) => s.setExpanded);
  const isExpanded = isSidebarGroupExpanded(expanded, group.id);
  const panelId = `sidebar-group-${group.id}`;

  // A group containing the currently active route auto-expands on
  // navigation — a collapsed group must never be the thing hiding the page
  // the user is already on.
  useEffect(() => {
    const hasActiveItem = group.items.some((item) => isItemActive(item.path, currentPath));
    if (hasActiveItem && !isExpanded) {
      setExpanded(group.id, true);
    }
    // Only re-run when the route or this group's own identity changes —
    // `isExpanded`/`setExpanded` intentionally excluded so a user-initiated
    // collapse (while already on a route in this group) isn't immediately
    // reverted by this same effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentPath, group.id]);

  return (
    <div className="mt-3 first:mt-0">
      <button
        type="button"
        aria-expanded={isExpanded}
        aria-controls={panelId}
        onClick={() => {
          setExpanded(group.id, !isExpanded);
        }}
        className="flex w-full items-center justify-between gap-1 rounded px-2.5 py-1 text-left text-[11px] font-semibold tracking-wide text-slate-500 uppercase hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200"
      >
        <span>{group.label}</span>
        {isExpanded ? (
          <ChevronDown aria-hidden className="h-3.5 w-3.5 shrink-0" />
        ) : (
          <ChevronRight aria-hidden className="h-3.5 w-3.5 shrink-0" />
        )}
      </button>
      {isExpanded && (
        <div id={panelId} className="flex flex-col gap-1">
          {group.items.map((item) => (
            <NavItemLink key={item.path} item={item} />
          ))}
        </div>
      )}
    </div>
  );
}

/** Stripe's account-switcher slot, minus the switching: a single instance
 * has nothing to switch between, so this renders identity only (T-3402 —
 * "do not build a fake switcher"). No cluster/instance name is available
 * client-side today: `GET /auth/me` (useSession) carries only the logged-
 * in user's username/realm, and `GET /health` (useIsDemo, T-2801) carries
 * only status/version/demo — neither the daemon nor any node has a
 * user-facing name in this API yet. This renders the product name plus
 * the demo state instead of fabricating a source; if a real
 * cluster/instance name is added to one of those responses later, this is
 * the one place to wire it in. */
function IdentityChip() {
  const isDemo = useIsDemo();
  return (
    <div
      aria-label={isDemo ? "vnprox — demo mode" : "vnprox"}
      className="mb-2 flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-2 py-2 dark:border-slate-800 dark:bg-slate-900 sm:px-2.5"
    >
      <span
        aria-hidden
        className={clsx(
          "flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
          isDemo
            ? "bg-amber-500 text-amber-950"
            : "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300",
        )}
      >
        V
      </span>
      <div className="hidden min-w-0 flex-col sm:flex">
        <span className="truncate text-sm font-semibold text-slate-800 dark:text-slate-100">vnprox</span>
        {isDemo && (
          <span className="w-fit rounded-full bg-amber-500 px-1.5 py-0 text-[10px] font-semibold tracking-wide text-amber-950 uppercase">
            Demo
          </span>
        )}
      </div>
    </div>
  );
}

export function Sidebar() {
  const narrow = useNarrowViewport();
  const location = useLocation();
  const narrowItems = narrow ? ALL_ITEMS.filter((item) => NARROW_REACHABLE_PATHS.has(item.path)) : [];

  return (
    <nav
      aria-label="Primary"
      // T-909: `relative z-50` — found via that task's own e2e run. The
      // changeset countdown/outcome banner (CountdownBanner.tsx) is a
      // `fixed inset-x-0 top-0 z-40` bar; with no z-index of its own
      // (position: static), the sidebar painted *underneath* it wherever
      // the two visually overlapped, making early nav items unclickable
      // while a banner was showing. Harmless at every width (was already
      // the case whenever a countdown/outcome banner appeared, not just at
      // narrow width) but only surfaced once a real test combined "banner
      // showing" with "click a sidebar link" — at narrow width, the
      // reachable-set filter below clusters the remaining items (Home,
      // Tools) right under the banner's full height, where the previous
      // desktop-only full list mostly didn't reach. z-50 matches this
      // app's dialog/drawer tier so modals (portaled after this element in
      // the DOM, so already painting on top regardless) are unaffected.
      className="relative z-50 flex w-16 shrink-0 flex-col border-r border-slate-200 bg-slate-50 py-3 dark:border-slate-800 dark:bg-slate-950 sm:w-56 sm:items-stretch sm:px-2"
    >
      <div className="px-1 sm:px-0">
        <IdentityChip />
      </div>

      <div className="flex flex-1 flex-col gap-1 overflow-y-auto px-1 sm:px-0">
        {narrow
          ? narrowItems.map((item) => <NavItemLink key={item.path} item={item} />)
          : (
            <>
              {FLAT_ITEMS.map((item) => (
                <NavItemLink key={item.path} item={item} />
              ))}
              {NAV_GROUPS.map((group) => (
                <NavGroupSection key={group.id} group={group} currentPath={location.pathname} />
              ))}
            </>
          )}
      </div>

      {/* Settings, pinned at the bottom and visually separated by a
       * hairline border (Stripe's "Developers" slot) — never offered at
       * narrow width, matching T-909's reachable-set filter exactly as
       * NavRail.tsx did (Settings is not in NARROW_REACHABLE_PATHS). */}
      {!narrow && (
        <div className="mt-2 border-t border-slate-200 px-1 pt-2 dark:border-slate-800 sm:px-0">
          <NavItemLink item={SETTINGS_ITEM} />
        </div>
      )}
    </nav>
  );
}
