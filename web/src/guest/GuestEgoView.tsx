// SPDX-License-Identifier: Apache-2.0

// T-3906's guest ego view: one guest's whole network story on one screen —
// composed entirely from existing reads, no new collector anywhere.
//
// Reuse map (every data source below already has a service/query/route;
// this file's own contribution is the composition and the degrade-vs-empty
// distinctions, not new collection):
//   - identity + fields:  topology/queries.ts's useInventoryDetailQuery
//     (GET /inventory/{ref}) — the same read InspectorPanel.tsx's Fields
//     tab already uses for a "guest"-kind ref.
//   - NICs + bridge/VNet attachment: guests/queries.ts's
//     useAllGuestNicsQuery (GET /topology, already cached cluster-wide),
//     filtered client-side to this guest (guestEgo.ts's nicsForGuest) — no
//     second topology fetch.
//   - path evaluation: simulator/queries.ts's useSimulateQuery
//     (POST /simulate/path), defaulted to this guest's first NIC -> the
//     "external" endpoint (mirrors simulator/traceLink.ts's own
//     traceToExternalPath pre-fill), rendered with simulator/
//     ResultPanel.tsx's own VerdictBanner — the identical verdict styling
//     the full Path simulator page uses, not a re-implementation.
//   - firewall verdict summary: firewall/queries.ts's useGuestRulesetQuery
//     (GET /firewall/rulesets?scope=guest) — the resolved (group-expanded)
//     cascade PVE would actually enforce, including its own `gates`
//     explanation for why (T-3103's ResolvedView).
//   - recent flows: flows/flowsQueries.ts's useFlowsQuery, composed here
//     (guestEgoQueries.ts) into one query per this guest's resolved
//     bridge/VNet target plus the same cluster-wide "any flows at all"
//     probe flows/FlowExplorer.tsx already infers "ingestion not
//     configured" from.
//   - live connections: conntrack/conntrackQueries.ts's useConntrackQuery
//     (GET /conntrack?guest=), which resolves the guest ref to its known
//     IPs server-side — no IP lookup of this view's own.
//   - open findings: findings/queries.ts's useFindingsQuery, filtered
//     client-side (guestEgo.ts's findingsForGuest) to this guest's own
//     refs — the same "fetch once, filter client-side" convention
//     FindingsSeverityTile/FindingsStreamPanel already use.
//   - guest interior (opt-in) and recent history: topology/InteriorTab.tsx
//     and topology/EntityHistoryTab.tsx, mounted directly — both already
//     implement their own toggle/loading/error/empty states (T-1304/
//     T-2403), so this view reuses the components themselves, not just
//     their queries.
//
// Section order (an incident-triage order, not a "what's easiest to
// render" order): identity -> NICs/attachment (what is this thing plugged
// into) -> path evaluation + firewall verdict (can traffic get in/out, and
// why/why not) -> recent flows + live connections (is there evidence of
// traffic) -> open findings (known problems) -> interior + history
// (opt-in deep-dive, least urgent during a live incident).
import { useMemo } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import { DashboardTile } from "../dashboard/DashboardTile";
import { useAllGuestNicsQuery } from "../guests/queries";
import { targetLabel } from "../guests/guestNics";
import { useFindingsQuery } from "../findings/queries";
import { useGuestRulesetQuery } from "../firewall/queries";
import { useSimulateQuery } from "../simulator/queries";
import { VerdictBanner } from "../simulator/ResultPanel";
import { traceFromPath, traceToExternalPath } from "../simulator/traceLink";
import { diagnosePath } from "../diagnose/diagnosePath";
import { protoName } from "../flows/proto";
import { useInventoryDetailQuery } from "../topology/queries";
import { useTopologyStore } from "../topology/store";
import { InteriorTab } from "../topology/InteriorTab";
import { EntityHistoryTab } from "../topology/EntityHistoryTab";
import { useClusterHasAnyFlowsProbe, useGuestConntrack, useGuestFlows } from "./guestEgoQueries";
import {
  bridgeTargetsForGuest,
  defaultEgoSimulateRequest,
  deriveConntrackPanelState,
  deriveFlowsPanelState,
  findingsForGuest,
  formatByteCount,
  guestsFromNicRows,
  isGuestRef,
  nicsForGuest,
  primaryNic,
  summarizeGuestRuleset,
} from "./guestEgo";

/** `/guest` with no `?ref=` yet: a picker over every guest the cluster-wide
 * NIC list already knows about — so the nav entry lands somewhere useful
 * without requiring a ref in hand first. */
function GuestPicker() {
  const { rows, isLoading } = useAllGuestNicsQuery();
  const guests = useMemo(() => guestsFromNicRows(rows), [rows]);

  return (
    <div className="flex h-full flex-col gap-3">
      <PageHeader
        title={
          <>
            Guest view <HelpAnchor topic="guest-ego-page" />
          </>
        }
        description="Pick a guest to see its whole network story — NICs, path evaluation, firewall verdict, flows, and findings — on one screen."
      />
      {isLoading && <p className="text-sm text-fg-muted">Loading guests…</p>}
      {!isLoading && guests.length === 0 && (
        <EmptyState
          icon="guest"
          variant="empty"
          title="No guests found"
          description="No guest NICs are known to this cluster yet."
        />
      )}
      {!isLoading && guests.length > 0 && (
        <ul className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {guests.map((g) => (
            <li key={g.ref}>
              <Link
                to={`/guest?ref=${encodeURIComponent(g.ref)}`}
                className="block rounded-lg border border-border bg-white p-3 text-sm hover:border-accent-400 dark:bg-slate-900 dark:hover:border-accent-600"
              >
                <span className="block font-medium text-slate-800 dark:text-slate-100">{g.label}</span>
                <span className="block text-fg-muted">
                  {g.node} · {g.nicCount} NIC{g.nicCount === 1 ? "" : "s"}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function NicsPanel({ guestRef }: { guestRef: string }) {
  const { rows, isLoading } = useAllGuestNicsQuery();
  const nics = useMemo(() => nicsForGuest(rows, guestRef), [rows, guestRef]);
  const navigate = useNavigate();
  const select = useTopologyStore((s) => s.select);

  return (
    <DashboardTile
      title="NICs & bridge/VLAN attachment"
      description="What this guest is plugged into right now."
      isLoading={isLoading}
      empty={nics.length === 0 ? { title: "No NICs found", description: "vnprox has no NIC recorded for this guest." } : undefined}
      onOpen={() => { void navigate("/guests"); }}
      openLabel="Open in Guests"
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>NIC</TableHead>
            <TableHead>Bridge/VNet</TableHead>
            <TableHead>VLAN</TableHead>
            <TableHead>Link</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {nics.map((n) => (
            <TableRow key={n.ref}>
              <TableCell>{n.label}</TableCell>
              <TableCell>
                {n.bridgeOrVnet ? (
                  <button
                    type="button"
                    className="text-accent-fg underline hover:no-underline"
                    onClick={() => {
                      select(n.bridgeOrVnet);
                      void navigate("/topology");
                    }}
                  >
                    {targetLabel(n.bridgeOrVnet)}
                  </button>
                ) : (
                  "(unattached)"
                )}
              </TableCell>
              <TableCell>{n.vid ?? "—"}</TableCell>
              <TableCell>{n.linkDown ? "down" : "up"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </DashboardTile>
  );
}

function PathPanel({ primaryNicRef }: { primaryNicRef: string | undefined }) {
  const navigate = useNavigate();
  const request = primaryNicRef ? defaultEgoSimulateRequest(primaryNicRef) : undefined;
  const { data, isLoading, isError } = useSimulateQuery(request);

  return (
    <DashboardTile
      title="Path evaluation (outbound)"
      description="Default check: this guest's first NIC -> external. Open the simulator for any other pair."
      isLoading={isLoading}
      error={isError ? "Could not evaluate a path for this guest." : undefined}
      empty={
        !primaryNicRef
          ? { title: "No NIC to evaluate from", description: "This guest has no NIC vnprox can start a path evaluation from." }
          : undefined
      }
      onOpen={
        primaryNicRef
          ? () => {
              const path = traceToExternalPath("guest-nic", primaryNicRef);
              if (path) void navigate(path);
            }
          : undefined
      }
      openLabel={primaryNicRef ? "Open in Path simulator" : undefined}
    >
      {data && (
        <div className="space-y-2">
          <VerdictBanner result={data} />
          <p className="text-xs text-fg-muted">
            {data.hops.length} hop{data.hops.length === 1 ? "" : "s"} evaluated
            {data.blockingRule ? ` · blocked by rule at position ${String(data.blockingRule.pos)}` : ""}
          </p>
        </div>
      )}
    </DashboardTile>
  );
}

function FirewallPanel({ guestRef }: { guestRef: string }) {
  const navigate = useNavigate();
  const { data, isLoading, isError } = useGuestRulesetQuery(guestRef);
  const summary = data ? summarizeGuestRuleset(data.resolved) : undefined;

  return (
    <DashboardTile
      title="Firewall verdict summary"
      description="This guest's resolved (group-expanded) firewall cascade — what PVE would actually enforce."
      isLoading={isLoading}
      error={isError ? "Could not read this guest's firewall configuration — it may not have been observed yet." : undefined}
      onOpen={() => { void navigate(`/firewall?scope=guest&ref=${encodeURIComponent(guestRef)}`); }}
      openLabel="Open in Firewall"
    >
      {summary && (
        <div className="space-y-2 text-sm">
          <p>
            <span className={summary.active ? "text-emerald-600 dark:text-emerald-400" : "text-amber-600 dark:text-amber-400"}>
              {summary.active ? "Active" : "Not active"}
            </span>{" "}
            · default in <span className="font-mono">{summary.defaultIn}</span> · default out{" "}
            <span className="font-mono">{summary.defaultOut}</span>
          </p>
          <p className="text-fg-muted">
            {summary.enabledRules} of {summary.totalRules} rule{summary.totalRules === 1 ? "" : "s"} enabled ·{" "}
            {summary.allowCount} allow · {summary.denyCount} deny
          </p>
          {summary.gateMessages.map((m, i) => (
            <p key={i} className="rounded border border-amber-300 bg-amber-50 px-2 py-1 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
              {m}
            </p>
          ))}
        </div>
      )}
    </DashboardTile>
  );
}

function FlowsPanel({ targets }: { targets: string[] }) {
  const navigate = useNavigate();
  const probe = useClusterHasAnyFlowsProbe();
  const guest = useGuestFlows(targets);
  const state = deriveFlowsPanelState({
    targets,
    clusterHasAnyFlows: probe.hasAny,
    clusterProbeLoading: probe.isLoading,
    clusterProbeError: probe.isError,
    guestItems: guest.items,
    guestLoading: guest.isLoading,
    guestError: guest.isError,
  });
  const primaryTarget = targets[0];

  return (
    <DashboardTile
      title="Recent flows"
      description="Traffic on the bridge/VNet(s) this guest attaches to — flow records resolve to a bridge/VNet, never a specific guest NIC, so this includes every guest on the same bridge/VNet, not only this one."
      isLoading={state.kind === "loading"}
      error={state.kind === "error" ? "Could not load flow records." : undefined}
      empty={
        state.kind === "no-targets"
          ? { title: "No bridge/VNet to query", description: "This guest has no resolved bridge/VNet attachment to scope a flow query to." }
          : state.kind === "ingestion-disabled"
            ? {
                title: "Flow ingestion is not enabled on this cluster",
                description: "No sFlow/NetFlow/IPFIX exporter or host-local sampler has ever reported a flow record. Configure one in vnprox.toml's [flows] section, then this panel will show real traffic.",
              }
            : state.kind === "empty"
              ? { title: "No recent flows for this guest's network", description: "Flow ingestion is on elsewhere in the cluster, but nothing has been recorded for this guest's bridge/VNet recently." }
              : undefined
      }
      onOpen={primaryTarget ? () => { void navigate(`/flows?guest=${encodeURIComponent(primaryTarget)}`); } : undefined}
      openLabel={primaryTarget ? "Open in Flows" : undefined}
    >
      {state.kind === "data" && (
        <ul className="space-y-1 text-xs">
          {state.items.slice(0, 8).map((f, i) => (
            <li key={`${String(f.at)}-${String(i)}`} className="flex items-center justify-between gap-2 rounded border border-border px-2 py-1">
              <span className="font-mono text-fg-body">
                {f.srcIp} → {f.dstIp} ({protoName(f.proto)})
              </span>
              <span className="text-fg-muted">{formatByteCount(f.bytes)}</span>
            </li>
          ))}
        </ul>
      )}
    </DashboardTile>
  );
}

function ConntrackPanel({ guestRef, guestNode }: { guestRef: string; guestNode: string }) {
  const navigate = useNavigate();
  const { data, isLoading, isError } = useGuestConntrack(guestRef);
  const state = deriveConntrackPanelState({
    isLoading,
    isError,
    items: data?.items,
    unavailableNodes: data?.unavailableNodes,
    guestNode,
  });

  return (
    <DashboardTile
      title="Live connections"
      description="This guest's current conntrack/NAT entries."
      isLoading={state.kind === "loading"}
      error={state.kind === "error" ? "Could not load conntrack data." : undefined}
      empty={
        state.kind === "unavailable"
          ? {
              title: "Conntrack is not available on this guest's node",
              description: "The node can't provide a conntrack read (missing capability, or no netlink conntrack support) — this is a node limitation, not evidence of no traffic.",
            }
          : state.kind === "empty"
            ? { title: "No active connections right now", description: "The node can read conntrack; this guest simply has none open at the moment." }
            : undefined
      }
      onOpen={() => { void navigate(`/conntrack?guest=${encodeURIComponent(guestRef)}`); }}
      openLabel="Open in Conntrack"
    >
      {state.kind === "data" && (
        <ul className="space-y-1 text-xs">
          {state.items.slice(0, 8).map((e, i) => (
            <li key={i} className="flex items-center justify-between gap-2 rounded border border-border px-2 py-1">
              <span className="font-mono text-fg-body">
                {e.srcIp}{e.srcPort ? `:${String(e.srcPort)}` : ""} → {e.dstIp}{e.dstPort ? `:${String(e.dstPort)}` : ""}
              </span>
              <span className="text-fg-muted">{e.state ?? "—"}</span>
            </li>
          ))}
        </ul>
      )}
    </DashboardTile>
  );
}

function FindingsPanel({ guestRef, nicRefs }: { guestRef: string; nicRefs: string[] }) {
  const navigate = useNavigate();
  const { data, isLoading, error } = useFindingsQuery();
  const findings = useMemo(() => findingsForGuest(data ?? [], guestRef, nicRefs), [data, guestRef, nicRefs]);

  return (
    <DashboardTile
      title="Open findings"
      description="Drift, health, and other checks affecting this guest or one of its NICs."
      isLoading={isLoading}
      error={error ? "Could not load findings." : undefined}
      empty={!isLoading && !error && findings.length === 0 ? { title: "No open findings", description: "Nothing currently flagged for this guest." } : undefined}
      onOpen={() => { void navigate("/tools"); }}
      openLabel="Open in Findings"
    >
      <ul className="space-y-1 text-xs">
        {findings.map((f) => (
          <li key={f.id} className="rounded border border-border px-2 py-1">
            <span className="font-medium text-fg-body">{f.severity}</span>{" "}
            <span className="text-fg-muted">{f.check}</span>
            <p className="text-fg-body">{f.detail}</p>
          </li>
        ))}
      </ul>
    </DashboardTile>
  );
}

/** The composed view for one guest — everything below the header is
 * assembled from the panels above, each independently degrading. */
function GuestEgoContent({ guestRef }: { guestRef: string }) {
  const navigate = useNavigate();
  const select = useTopologyStore((s) => s.select);
  const {
    data: detail,
    isLoading: detailLoading,
    isError: detailError,
    refetch: refetchDetail,
  } = useInventoryDetailQuery(guestRef);
  const { rows } = useAllGuestNicsQuery();
  const nics = useMemo(() => nicsForGuest(rows, guestRef), [rows, guestRef]);
  const targets = useMemo(() => bridgeTargetsForGuest(nics), [nics]);
  const nicRefs = useMemo(() => nics.map((n) => n.ref), [nics]);
  const first = primaryNic(nics);
  const node = detail?.node ?? nics[0]?.node ?? "";
  const diagnoseUrl = diagnosePath("guest", guestRef);
  const traceUrl = first ? traceFromPath("guest-nic", first.ref) : undefined;

  return (
    <div className="flex h-full flex-col gap-4">
      <PageHeader
        title={
          <>
            {detail?.label ?? guestRef} <HelpAnchor topic="guest-ego-page" />
          </>
        }
        description={detail ? `${detail.kind} on ${detail.node || "cluster"} · ${guestRef}` : guestRef}
        actions={
          <div className="flex flex-wrap gap-2 text-sm">
            <button
              type="button"
              className="rounded-md border border-border-strong px-3 py-1.5 font-medium hover:bg-slate-50 dark:hover:bg-slate-800"
              onClick={() => {
                select(guestRef);
                void navigate("/topology");
              }}
            >
              Open in inspector
            </button>
            {diagnoseUrl && (
              <button
                type="button"
                className="rounded-md border border-border-strong px-3 py-1.5 font-medium hover:bg-slate-50 dark:hover:bg-slate-800"
                onClick={() => { void navigate(diagnoseUrl); }}
              >
                Diagnose
              </button>
            )}
            {traceUrl && (
              <button
                type="button"
                className="rounded-md border border-border-strong px-3 py-1.5 font-medium hover:bg-slate-50 dark:hover:bg-slate-800"
                onClick={() => { void navigate(traceUrl); }}
              >
                Trace path from here
              </button>
            )}
          </div>
        }
      />

      {detailLoading && <p className="text-sm text-fg-muted">Loading guest…</p>}
      {detailError && (
        <EmptyState
          icon="guest"
          variant="failed"
          title="Could not load this guest"
          description="It may have just been removed, or the ref is invalid."
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetchDetail()}>
              Retry
            </Button>
          }
        />
      )}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <NicsPanel guestRef={guestRef} />
        <PathPanel primaryNicRef={first?.ref} />
        <FirewallPanel guestRef={guestRef} />
        <FlowsPanel targets={targets} />
        <ConntrackPanel guestRef={guestRef} guestNode={node} />
        <FindingsPanel guestRef={guestRef} nicRefs={nicRefs} />
      </div>

      <DashboardTile title="Guest interior (opt-in)" description="This guest's own view of its network — off by default.">
        <InteriorTab entityRef={guestRef} />
      </DashboardTile>

      <DashboardTile title="Recent history" description="Changesets, audit entries, and snapshots touching this guest.">
        <EntityHistoryTab entityRef={guestRef} />
      </DashboardTile>
    </div>
  );
}

/** `/guest?ref=<guest ref>` — T-3906's guest ego view. With no `ref`, shows
 * a picker instead of a broken/blank screen. */
export function GuestEgoView() {
  const [searchParams] = useSearchParams();
  const ref = searchParams.get("ref");

  if (!ref) {
    return <GuestPicker />;
  }

  if (!isGuestRef(ref)) {
    return (
      <div className="flex h-full flex-col gap-3">
        <PageHeader
          title={
            <>
              Guest view <HelpAnchor topic="guest-ego-page" />
            </>
          }
        />
        <EmptyState title="Not a guest reference" description={`"${ref}" is not a guest ref (expected "guest:<node>:<vmid>").`} />
      </div>
    );
  }

  return <GuestEgoContent guestRef={ref} />;
}
