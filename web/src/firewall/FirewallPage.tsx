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
import { useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from "../components/Tabs";
import { FirewallBanners } from "./Banner";
import type { FocusRule } from "./focusRule";
import { matchesFocus, parseFirewallDeepLink } from "./focusRule";
import { GroupInspector } from "./GroupInspector";
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
  useVnetRulesetQuery,
  useVnetRulesetsQuery,
} from "./queries";
import { guestRefFromFwRulesetRef, type FwRulesetLocation } from "./refs";

// "group" (T-2002) is reached only via the Objects tab's per-group
// "Inspect" action, never a top-level tab (TABS below stays at five) — a
// security group isn't a hierarchy level the way Datacenter/Nodes/Guests/
// VNets are, it's an object drilled into from the Objects list.
type Scope = "cluster" | "node" | "guest" | "vnet" | "objects" | "group";

const TABS: { scope: Scope; label: string }[] = [
  { scope: "cluster", label: "Datacenter" },
  { scope: "node", label: "Nodes" },
  { scope: "guest", label: "Guests" },
  { scope: "vnet", label: "VNets" },
  { scope: "objects", label: "Objects" },
];

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
  // T-2002 AC1's graceful-degrade case: a deep link naming a rule that no
  // longer exists (deleted, reordered elsewhere, or the referencing group
  // was removed) must say so, not silently render an unfocused table —
  // "an empty highlight" is exactly the bug the source report (T-505)
  // flagged. Checked once `detail` (the guest's current resolved view) is
  // in hand, against the same identity triple ResolvedViewTable itself
  // matches on.
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

  const { data: detail, isLoading: detailLoading, error: detailError } = useGuestRulesetQuery(selected);

  // A deep link's guest may itself no longer exist (the guest was removed
  // since the link was created/shared) — degrade with a message rather
  // than silently showing nothing, the same honesty this task's AC1 asks
  // for one level down (the rule itself).
  const deepLinkGuestMissing = focusRule !== undefined && !detailLoading && Boolean(detailError) && !detail;
  const focusRuleMissing =
    focusRule !== undefined && detail !== undefined && !detail.resolved.rules.some((r) => matchesFocus(r, focusRule));

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
      {deepLinkGuestMissing && (
        <EmptyState
          title="Linked guest not found"
          description="This deep link points at a guest that no longer exists (or is no longer observed). Pick a guest above to continue."
        />
      )}
      {focusRuleMissing && (
        <div
          role="status"
          className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200"
        >
          The linked rule no longer exists — it may have been deleted, reordered, or its security group removed. Showing this guest's current firewall configuration instead.
        </div>
      )}
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

interface VNetPanelProps {
  selected: string | undefined;
  onSelect: (vnetRef: string) => void;
}

// VNetPanel is T-3103's fourth hierarchy tab: a vnet's forward-chain
// firewall ruleset. Addressed by ref (like GuestPanel — a vnet ruleset's id
// is a "<zone>/<vnet>" composite, not a plain name a `<select>` keyed by
// node name would work for) but, unlike GuestPanel, carries no resolved-
// view section: this scope has no hardware-confirmed cluster+group cascade
// model (see fw.Snapshot.VNets' Go doc comment), so only the raw editable
// rule table is shown — the same "raw only" treatment scope=node already
// gets.
function VNetPanel({ selected, onSelect }: VNetPanelProps) {
  const { data: list, isLoading: listLoading } = useVnetRulesetsQuery();
  const { data: objects } = useFirewallObjectsQuery();
  const vnets = useMemo(
    () => (list?.items ?? []).map((r) => ({ ref: r.vnet, label: vnetLabelFromRef(r.vnet) })).filter((v): v is { ref: string; label: string } => v.ref !== undefined),
    [list],
  );

  useEffect(() => {
    if (selected === undefined && vnets.length > 0 && vnets[0] !== undefined) {
      onSelect(vnets[0].ref);
    }
  }, [vnets, selected, onSelect]);

  const { data: ruleset, isLoading: rulesetLoading } = useVnetRulesetQuery(selected);

  if (listLoading) return <p className="text-sm text-slate-400">Loading vnets…</p>;
  if (vnets.length === 0) {
    return <EmptyState title="No vnet firewall data" description="No vnet firewall configuration has been observed yet." />;
  }
  return (
    <div className="flex flex-col gap-3">
      <select
        aria-label="Select vnet"
        className="w-fit rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
        value={selected ?? ""}
        onChange={(e) => { onSelect(e.target.value); }}
      >
        {vnets.map((v) => (
          <option key={v.ref} value={v.ref}>
            {v.label}
          </option>
        ))}
      </select>
      {rulesetLoading && <p className="text-sm text-slate-400">Loading…</p>}
      {ruleset && (
        <>
          <ScopeToggle scope="vnet" target={ruleset.ref} enabled={ruleset.enabled} vnetLabel={vnetLabelFromRef(ruleset.vnet) ?? ruleset.vnet} />
          <FirewallBanners banners={ruleset.banners} />
          <RuleEditor rules={ruleset.rules} target={ruleset.ref} objects={objects} />
        </>
      )}
    </div>
  );
}

/** Extracts a vnet's bare display name from its ref ("sdn-vnet::zone1/vnet1"
 * -> "vnet1") for the tab's `<select>` options and the scope-toggle label —
 * showing the zone/vnet composite everywhere would be noisier than useful
 * once a cluster has more than a couple of zones. */
function vnetLabelFromRef(vnetRef: string | undefined): string | undefined {
  if (!vnetRef) return undefined;
  const id = vnetRef.split(":")[2];
  if (!id) return undefined;
  const parts = id.split("/");
  return parts[parts.length - 1];
}

interface ObjectsTabProps {
  onNavigate: (loc: FwRulesetLocation) => void;
  onInspectGroup: (name: string) => void;
}

function ObjectsTab({ onNavigate, onInspectGroup }: ObjectsTabProps) {
  const { data, isLoading, error } = useFirewallObjectsQuery();
  if (isLoading) return <p className="text-sm text-slate-400">Loading objects…</p>;
  if (error || !data) {
    return <EmptyState title="Could not load objects" description="Try again in a moment." />;
  }
  return <ObjectsPanel objects={data} onNavigate={onNavigate} onInspectGroup={onInspectGroup} />;
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
  const [selectedVnetRef, setSelectedVnetRef] = useState<string | undefined>(undefined);
  // T-2002: which security group the Objects tab's "Inspect" action last
  // opened. Cleared (not required to be) when navigating away — reopening
  // the same group re-fetches, which is fine at this data size/staleness.
  const [inspectedGroup, setInspectedGroup] = useState<string | undefined>(undefined);

  // Acceptance criterion 2's "deep-links work": jumping from an object's
  // "referenced by" list to the referencing rule's own scope/selection.
  // Position-level scroll/highlight is left to a future pass — landing on
  // the correct scope+node/guest tab is the load-bearing part of "deep
  // link" this task's tests exercise.
  function navigateToRuleset(loc: FwRulesetLocation): void {
    setScope(loc.scope);
    if (loc.scope === "node" && loc.node) setSelectedNode(loc.node);
    if (loc.scope === "guest" && loc.guestRef) setSelectedGuestRef(loc.guestRef);
    if (loc.scope === "vnet" && loc.vnetRef) setSelectedVnetRef(loc.vnetRef);
  }

  function inspectGroup(name: string): void {
    setInspectedGroup(name);
    setScope("group");
  }

  // T-2002's "group" scope is a drill-down from the Objects tab's own
  // "Inspect" action, not a tab of its own (TABS above stays at five — see
  // its doc comment) — so it maps onto the Objects trigger for Tabs.Root's
  // controlled `value` (keeping that tab visually active) while still
  // swapping in GroupInspector instead of ObjectsTab underneath.
  const activeTabValue = scope === "group" ? "objects" : scope;

  return (
    <TabsRoot
      value={activeTabValue}
      onValueChange={(v) => { setScope(v as Scope); }}
      className="flex h-full flex-col gap-3"
    >
      <PageHeader
        title="Firewall"
        tabs={
          <TabsList aria-label="Firewall hierarchy">
            {TABS.map((t) => (
              <TabsTrigger key={t.scope} value={t.scope}>
                {t.label}
              </TabsTrigger>
            ))}
          </TabsList>
        }
      />

      <TabsContent value="cluster">
        <ClusterPanel />
      </TabsContent>
      <TabsContent value="node">
        <NodePanel selected={selectedNode} onSelect={setSelectedNode} />
      </TabsContent>
      <TabsContent value="guest">
        <GuestPanel selected={selectedGuestRef} onSelect={setSelectedGuestRef} focusRule={deepLink.focusRule} />
      </TabsContent>
      <TabsContent value="vnet">
        <VNetPanel selected={selectedVnetRef} onSelect={setSelectedVnetRef} />
      </TabsContent>
      <TabsContent value="objects">
        {scope === "group" && inspectedGroup ? (
          <GroupInspector name={inspectedGroup} onBack={() => { setScope("objects"); }} />
        ) : (
          <ObjectsTab onNavigate={navigateToRuleset} onInspectGroup={inspectGroup} />
        )}
      </TabsContent>
    </TabsRoot>
  );
}
