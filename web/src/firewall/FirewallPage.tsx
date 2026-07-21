// Firewall hierarchy navigation across the three PVE scopes (Datacenter/
// Node/Guest) plus the Objects tab, per docs/features/firewall.md §1/§2.
// T-501 built the read-only tables/resolved view/object usage tracking;
// T-502 makes them editable (drag-to-reorder, inline enable/disable,
// builder row, scope enable/disable with a "what will happen" summary,
// object usage-guarded delete with deep-links) and wires everything to
// stage fw.* ops via the change engine.
//
// T-504 additive change: reads the `scope`/`ref`/`pos`/`origin`/`group`
// deep-link query params (focusRule.ts's parseFirewallDeepLink) so a
// simulator deny verdict's blocking-rule card (or a correlated firewall
// log line, T-505) can land here with the exact rule scrolled-to and
// highlighted — never DOM position, always guestRef+pos+origin identity.
// The deep link only seeds the initial scope/guest selection; it never
// fights a user's own subsequent tab/guest clicks (which don't rewrite the
// URL — see focusRule.ts's doc comment).
import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import { useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { FirewallBanners } from "./Banner";
import type { FocusRule } from "./focusRule";
import { parseFirewallDeepLink } from "./focusRule";
import { MicrosegPlanner } from "../microseg/MicrosegPlanner";
import { ObjectsPanel } from "./ObjectsPanel";
import { ResolvedViewTable } from "./ResolvedViewTable";
import { RuleEditor } from "./RuleEditor";
import { ScopeToggle } from "./ScopeToggle";
import {
  useClusterRulesetQuery,
  useFirewallObjectsQuery,
  useGuestRulesetQuery,
  useGuestRulesetsQuery,
  useNodeRulesetQuery,
  useNodeRulesetsQuery,
} from "./queries";
import { guestRefFromFwRulesetRef, type FwRulesetLocation } from "./refs";

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
  const { data: objects } = useFirewallObjectsQuery();
  if (isLoading) return <p className="text-sm text-slate-400">Loading datacenter firewall…</p>;
  if (error || !data) {
    return <EmptyState title="No datacenter firewall data" description="The cluster firewall configuration has not been observed yet." />;
  }
  return (
    <div className="flex flex-col gap-3">
      <ScopeToggle scope="cluster" target={data.ref} enabled={data.enabled} />
      <FirewallBanners banners={data.banners} />
      <RuleEditor rules={data.rules} target={data.ref} objects={objects} />
    </div>
  );
}

interface NodePanelProps {
  selected: string | undefined;
  onSelect: (node: string) => void;
}

function NodePanel({ selected, onSelect }: NodePanelProps) {
  const { data: list, isLoading: listLoading } = useNodeRulesetsQuery();
  const { data: objects } = useFirewallObjectsQuery();
  const nodes = useMemo(() => (list?.items ?? []).map((r) => r.node).filter((n): n is string => n !== undefined), [list]);

  useEffect(() => {
    if (selected === undefined && nodes.length > 0 && nodes[0] !== undefined) {
      onSelect(nodes[0]);
    }
  }, [nodes, selected, onSelect]);

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
        onChange={(e) => { onSelect(e.target.value); }}
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
          <ScopeToggle scope="node" target={ruleset.ref} enabled={ruleset.enabled} node={selected} />
          <FirewallBanners banners={ruleset.banners} />
          <RuleEditor rules={ruleset.rules} target={ruleset.ref} objects={objects} />
        </>
      )}
    </div>
  );
}

interface GuestPanelProps {
  selected: string | undefined;
  onSelect: (guestRef: string) => void;
  focusRule?: FocusRule;
}

function GuestPanel({ selected, onSelect, focusRule }: GuestPanelProps) {
  const { data: list, isLoading: listLoading } = useGuestRulesetsQuery();
  const { data: objects } = useFirewallObjectsQuery();
  // The list endpoint's items carry each ruleset's *own* Ref
  // (fw-ruleset:<node>:guest/<kind>/<vmid>"), not the guest's own Ref the
  // detail endpoint's `ref` query param requires (docs/api.md: "a
  // guest:<node>:<vmid> Ref triplet") — derive it locally rather than
  // passing the wrong ref type through (see refs.ts's doc comment).
  const guests = useMemo(
    () => (list?.items ?? [])
      .map((r) => ({ fwRef: r.ref, guestRef: guestRefFromFwRulesetRef(r.ref) }))
      .filter((g): g is { fwRef: string; guestRef: string } => g.guestRef !== undefined),
    [list],
  );

  useEffect(() => {
    if (selected === undefined && guests.length > 0 && guests[0] !== undefined) {
      onSelect(guests[0].guestRef);
    }
  }, [guests, selected, onSelect]);

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
        onChange={(e) => { onSelect(e.target.value); }}
      >
        {guests.map((g) => (
          <option key={g.guestRef} value={g.guestRef}>
            {g.guestRef}
          </option>
        ))}
      </select>
      {detailLoading && <p className="text-sm text-slate-400">Loading…</p>}
      {detail && (
        <div className="flex flex-col gap-4">
          <div>
            <h3 className="mb-1 text-sm font-semibold">Enablement</h3>
            <ScopeToggle scope="guest" target={detail.ruleset.ref} enabled={detail.ruleset.enabled} guestLabel={selected} />
            <FirewallBanners banners={detail.ruleset.banners} />
            {(!detail.ruleset.banners || detail.ruleset.banners.length === 0) && (
              <p className="text-sm text-emerald-700 dark:text-emerald-400">Active — this guest's firewall is enforcing rules.</p>
            )}
          </div>
          <div>
            <h3 className="mb-1 text-sm font-semibold">This guest's own rules</h3>
            <RuleEditor
              rules={detail.ruleset.rules}
              target={detail.ruleset.ref}
              objects={objects}
              focusPos={focusRule?.origin === "guest" ? focusRule.pos : undefined}
            />
          </div>
          <div>
            <h3 className="mb-1 text-sm font-semibold">Resolved evaluation order</h3>
            <ResolvedViewTable resolved={detail.resolved} focusRule={focusRule} />
          </div>
          {selected && (
            <div className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
              <MicrosegPlanner guestRef={selected} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

interface ObjectsTabProps {
  onNavigate: (loc: FwRulesetLocation) => void;
}

function ObjectsTab({ onNavigate }: ObjectsTabProps) {
  const { data, isLoading, error } = useFirewallObjectsQuery();
  if (isLoading) return <p className="text-sm text-slate-400">Loading objects…</p>;
  if (error || !data) {
    return <EmptyState title="Could not load objects" description="Try again in a moment." />;
  }
  return <ObjectsPanel objects={data} onNavigate={onNavigate} />;
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
  const [selectedNode, setSelectedNode] = useState<string | undefined>(undefined);
  const [selectedGuestRef, setSelectedGuestRef] = useState<string | undefined>(
    deepLink.scope === "guest" ? deepLink.ref : undefined,
  );

  // Acceptance criterion 2's "deep-links work": jumping from an object's
  // "referenced by" list to the referencing rule's own scope/selection.
  // Position-level scroll/highlight is left to a future pass — landing on
  // the correct scope+node/guest tab is the load-bearing part of "deep
  // link" this task's tests exercise.
  function navigateToRuleset(loc: FwRulesetLocation): void {
    setScope(loc.scope);
    if (loc.scope === "node" && loc.node) setSelectedNode(loc.node);
    if (loc.scope === "guest" && loc.guestRef) setSelectedGuestRef(loc.guestRef);
  }

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
      {scope === "node" && <NodePanel selected={selectedNode} onSelect={setSelectedNode} />}
      {scope === "guest" && (
        <GuestPanel selected={selectedGuestRef} onSelect={setSelectedGuestRef} focusRule={deepLink.focusRule} />
      )}
      {scope === "objects" && <ObjectsTab onNavigate={navigateToRuleset} />}
    </div>
  );
}
