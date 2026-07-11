// Firewall read views (T-501): hierarchy navigation across the three PVE
// scopes (Datacenter/Node/Guest), read-only per-scope rule tables, the
// guest resolved view, and object usage tracking — per
// docs/features/firewall.md §1/§2. Writes (T-502) are a later task; every
// affordance here is view-only.
//
// T-504 additive change: reads the `scope`/`ref`/`pos`/`origin`/`group`
// deep-link query params (focusRule.ts's parseFirewallDeepLink) so a
// simulator deny verdict's blocking-rule card (or a correlated firewall
// log line, T-505) can land here with the exact rule scrolled-to and
// highlighted — never DOM position, always guestRef+pos+origin identity.
import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import { useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { FirewallBanners } from "./Banner";
import type { FocusRule } from "./focusRule";
import { parseFirewallDeepLink } from "./focusRule";
import { ObjectsPanel } from "./ObjectsPanel";
import { ResolvedViewTable } from "./ResolvedViewTable";
import { RuleTable } from "./RuleTable";
import {
  useClusterRulesetQuery,
  useFirewallObjectsQuery,
  useGuestRulesetQuery,
  useGuestRulesetsQuery,
  useNodeRulesetQuery,
  useNodeRulesetsQuery,
} from "./queries";

type Scope = "cluster" | "node" | "guest" | "objects";

const TABS: { scope: Scope; label: string }[] = [
  { scope: "cluster", label: "Datacenter" },
  { scope: "node", label: "Nodes" },
  { scope: "guest", label: "Guests" },
  { scope: "objects", label: "Objects" },
];

function TabButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={clsx(
        "rounded-md px-3 py-1.5 text-sm font-medium",
        active
          ? "bg-accent-600 text-white"
          : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800",
      )}
    >
      {label}
    </button>
  );
}

function ClusterPanel() {
  const { data, isLoading, error } = useClusterRulesetQuery();
  if (isLoading) return <p className="text-sm text-slate-400">Loading datacenter firewall…</p>;
  if (error || !data) {
    return <EmptyState title="No datacenter firewall data" description="The cluster firewall configuration has not been observed yet." />;
  }
  return (
    <div className="flex flex-col gap-3">
      <FirewallBanners banners={data.banners} />
      <RuleTable rules={data.rules} />
    </div>
  );
}

function NodePanel() {
  const { data: list, isLoading: listLoading } = useNodeRulesetsQuery();
  const [selected, setSelected] = useState<string | undefined>(undefined);
  const nodes = useMemo(() => (list?.items ?? []).map((r) => r.node).filter((n): n is string => n !== undefined), [list]);

  useEffect(() => {
    if (selected === undefined && nodes.length > 0) {
      setSelected(nodes[0]);
    }
  }, [nodes, selected]);

  const { data: ruleset, isLoading: rulesetLoading } = useNodeRulesetQuery(selected);

  if (listLoading) return <p className="text-sm text-slate-400">Loading nodes…</p>;
  if (nodes.length === 0) {
    return <EmptyState title="No node firewall data" description="No node firewall configuration has been observed yet." />;
  }
  return (
    <div className="flex flex-col gap-3">
      <select
        aria-label="Select node"
        className="w-fit rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
        value={selected ?? ""}
        onChange={(e) => { setSelected(e.target.value); }}
      >
        {nodes.map((n) => (
          <option key={n} value={n}>
            {n}
          </option>
        ))}
      </select>
      {rulesetLoading && <p className="text-sm text-slate-400">Loading…</p>}
      {ruleset && (
        <>
          <FirewallBanners banners={ruleset.banners} />
          <RuleTable rules={ruleset.rules} />
        </>
      )}
    </div>
  );
}

function GuestPanel({ initialRef, focusRule }: { initialRef?: string; focusRule?: FocusRule }) {
  const { data: list, isLoading: listLoading } = useGuestRulesetsQuery();
  const [selected, setSelected] = useState<string | undefined>(initialRef);
  const guests = useMemo(() => list?.items ?? [], [list]);

  useEffect(() => {
    if (selected === undefined && guests[0] !== undefined) {
      setSelected(guests[0].ref);
    }
  }, [guests, selected]);

  const { data: detail, isLoading: detailLoading } = useGuestRulesetQuery(selected);

  if (listLoading) return <p className="text-sm text-slate-400">Loading guests…</p>;
  if (guests.length === 0) {
    return <EmptyState title="No guest firewall data" description="No guest firewall configuration has been observed yet." />;
  }
  return (
    <div className="flex flex-col gap-3">
      <select
        aria-label="Select guest"
        className="w-fit rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
        value={selected ?? ""}
        onChange={(e) => { setSelected(e.target.value); }}
      >
        {guests.map((g) => (
          <option key={g.ref} value={g.ref}>
            {g.ref}
          </option>
        ))}
      </select>
      {detailLoading && <p className="text-sm text-slate-400">Loading…</p>}
      {detail && (
        <div className="flex flex-col gap-4">
          <div>
            <h3 className="mb-1 text-sm font-semibold">Enablement</h3>
            <FirewallBanners banners={detail.ruleset.banners} />
            {(!detail.ruleset.banners || detail.ruleset.banners.length === 0) && (
              <p className="text-sm text-emerald-700 dark:text-emerald-400">Active — this guest's firewall is enforcing rules.</p>
            )}
          </div>
          <div>
            <h3 className="mb-1 text-sm font-semibold">This guest's own rules</h3>
            <RuleTable rules={detail.ruleset.rules} focusPos={focusRule?.origin === "guest" ? focusRule.pos : undefined} />
          </div>
          <div>
            <h3 className="mb-1 text-sm font-semibold">Resolved evaluation order</h3>
            <ResolvedViewTable resolved={detail.resolved} focusRule={focusRule} />
          </div>
        </div>
      )}
    </div>
  );
}

function ObjectsTab() {
  const { data, isLoading, error } = useFirewallObjectsQuery();
  if (isLoading) return <p className="text-sm text-slate-400">Loading objects…</p>;
  if (error || !data) {
    return <EmptyState title="Could not load objects" description="Try again in a moment." />;
  }
  return <ObjectsPanel objects={data} />;
}

export function FirewallPage() {
  const [searchParams] = useSearchParams();
  // Parsed once, on mount: a deep link should pre-select the right scope
  // and guest, but must not fight a user's own subsequent tab/guest
  // clicks (which never rewrite the URL — see focusRule.ts's doc comment;
  // this page manages scope/selection as local state, matching T-501's
  // original design note in web/src/fwlog/deeplink.ts).
  const deepLink = useMemo(() => parseFirewallDeepLink(searchParams), []); // eslint-disable-line react-hooks/exhaustive-deps
  const [scope, setScope] = useState<Scope>(deepLink.scope === "guest" ? "guest" : "cluster");

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">Firewall</h1>
        <div className="flex gap-1">
          {TABS.map((t) => (
            <TabButton key={t.scope} active={scope === t.scope} label={t.label} onClick={() => { setScope(t.scope); }} />
          ))}
        </div>
      </div>

      {scope === "cluster" && <ClusterPanel />}
      {scope === "node" && <NodePanel />}
      {scope === "guest" && (
        <GuestPanel initialRef={deepLink.scope === "guest" ? deepLink.ref : undefined} focusRule={deepLink.focusRule} />
      )}
      {scope === "objects" && <ObjectsTab />}
    </div>
  );
}
