// SPDX-License-Identifier: Apache-2.0

// EVPN/BGP observability (docs/features/sdn.md §3): peering matrix (nodes
// x peers, state colors), session detail (prefixes, uptime, last error),
// VNI list, and exit-node health — GET /sdn/evpn/status. Lives alongside
// T-401's tree/detail panels in the same SDN cockpit page (SdnPage.tsx
// switches between the two via a tab), read-only like the rest of the
// cockpit's T-40x observability surfaces.
import { useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { FindingItem } from "../findings/FindingsList";
import { FindingsList } from "../findings/FindingsList";
import type { EvpnFinding, EvpnStatus } from "../api/types";
import { StatusDot } from "./StatusDot";
import {
  buildEvpnMatrix,
  evpnStateEntityStatus,
  formatEvpnUptime,
  resolveEvpnSelection,
  type EvpnSelection,
} from "./evpnStatus";
import { useEvpnStatusQuery } from "./queries";

function toFindingItem(f: EvpnFinding): FindingItem {
  return { id: f.id, severity: f.severity, detail: f.detail, nodes: [f.node], refs: [f.peerAddr], fixable: false, category: f.code };
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-4 border-b border-slate-100 py-1 text-sm last:border-0 dark:border-slate-800">
      <dt className="text-fg-subtle">{label}</dt>
      <dd className="text-right text-slate-800 dark:text-slate-100">{value || "—"}</dd>
    </div>
  );
}

function PeeringMatrix({
  status,
  selection,
  onSelect,
}: {
  status: EvpnStatus;
  selection: EvpnSelection | undefined;
  onSelect: (s: EvpnSelection) => void;
}) {
  const matrix = buildEvpnMatrix(status);
  const nodeByName = new Map(status.nodes.map((n) => [n.node, n]));

  if (matrix.peerAddrs.length === 0) {
    return (
      <EmptyState
        icon="vxlan"
        variant="empty"
        title="No BGP/EVPN sessions observed"
        description="No node in this cluster reports an active FRR peering session."
      />
    );
  }

  return (
    <Table aria-label="EVPN peering matrix">
      <TableHeader>
        <TableRow>
          <TableHead>Node</TableHead>
          {matrix.peerAddrs.map((addr) => (
            <TableHead key={addr} className="text-center">
              {addr}
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {matrix.nodes.map((node) => {
          const ns = nodeByName.get(node);
          return (
            <TableRow key={node}>
              <TableCell className="font-medium">
                {node}
                {/* T-4215: only the TEXT moved to a role token below. The
                    chip's own `bg-slate-100 dark:bg-slate-800` is deliberately
                    left alone — it sits *below* the page in light mode and
                    *above* it in dark, which is the ordinary subtle-chip
                    idiom, and `bg-surface-sunken` would have made it darker
                    than the page in both: a visible change to fix a contrast
                    failure that was never in the background. */}
                {ns && !ns.frrInstalled && (
                  <span className="ml-2 rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-fg-subtle dark:bg-slate-800">
                    no EVPN
                  </span>
                )}
                {ns?.error && (
                  <span className="ml-2 rounded bg-red-100 px-1.5 py-0.5 text-[10px] text-red-700 dark:bg-red-950 dark:text-red-300">
                    error
                  </span>
                )}
              </TableCell>
              {matrix.peerAddrs.map((addr) => {
                const peer = matrix.cellFor(node, addr);
                const selected = selection?.node === node && selection.peerAddr === addr;
                return (
                  <TableCell key={addr} className="text-center">
                    {peer ? (
                      <button
                        type="button"
                        title={`${node} -> ${addr}: ${peer.state}${peer.stateReason ? ` (${peer.stateReason})` : ""}`}
                        onClick={() => {
                          onSelect({ node, peerAddr: addr });
                        }}
                        className={clsx(
                          "inline-flex items-center gap-1.5 rounded px-1.5 py-0.5",
                          selected && "bg-accent-600/10 dark:bg-accent-500/15",
                        )}
                      >
                        <StatusDot status={evpnStateEntityStatus(peer.state)} />
                        <span className="text-xs text-slate-600 dark:text-slate-300">{peer.state}</span>
                      </button>
                    ) : (
                      <span className="text-fg-subtle">—</span>
                    )}
                  </TableCell>
                );
              })}
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

function SessionDetail({ status, selection }: { status: EvpnStatus; selection: EvpnSelection | undefined }) {
  const resolved = resolveEvpnSelection(status, selection);
  if (!resolved) {
    return <EmptyState title="No session selected" description="Click a cell in the peering matrix to see its detail." />;
  }
  const { node, peer } = resolved;
  return (
    <div className="flex flex-col gap-3">
      <div>
        <h3 className="text-base font-semibold">
          {node.node} ↔ {peer.peerAddr}
        </h3>
        <p className="text-sm text-fg-subtle">
          {peer.peerNode ? `Peer node: ${peer.peerNode}` : "Peer node unresolved"}
          {peer.addressFamily ? ` · ${peer.addressFamily}` : ""}
        </p>
      </div>
      <div className="flex items-center gap-2">
        <StatusDot status={evpnStateEntityStatus(peer.state)} />
        <span className="text-sm font-medium">{peer.state}</span>
        {peer.stateReason && <span className="text-sm text-fg-subtle">({peer.stateReason})</span>}
      </div>
      <dl>
        <Field label="Remote AS" value={peer.remoteAs ? String(peer.remoteAs) : ""} />
        <Field label="Prefixes received" value={peer.pfxRcd !== undefined ? String(peer.pfxRcd) : ""} />
        <Field label="Prefixes sent" value={peer.pfxSnt !== undefined ? String(peer.pfxSnt) : ""} />
        <Field label="Uptime" value={formatEvpnUptime(peer.uptimeSecs)} />
        <Field label="Last error" value={peer.stateReason ?? ""} />
        <Field
          label="Recent flaps"
          value={peer.flapTransitions !== undefined ? `${String(peer.flapTransitions)} state change(s)` : ""}
        />
      </dl>
    </div>
  );
}

function VniList({ status }: { status: EvpnStatus }) {
  const rows = status.nodes.flatMap((n) => n.vnis.map((v) => ({ node: n.node, ...v })));
  if (rows.length === 0) {
    return <EmptyState icon="vxlan" variant="unconfigured" title="No EVPN VNIs observed" />;
  }
  return (
    <Table aria-label="EVPN VNI list">
      <TableHeader>
        <TableRow>
          <TableHead>Node</TableHead>
          <TableHead>VNI</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>VXLAN interface</TableHead>
          <TableHead>Tenant VRF</TableHead>
          <TableHead>MACs</TableHead>
          <TableHead>ARP/ND</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={`${r.node}-${String(r.vni)}`}>
            <TableCell>{r.node}</TableCell>
            <TableCell className="font-mono">{r.vni}</TableCell>
            <TableCell>{r.type}</TableCell>
            <TableCell>{r.vxlanIf ?? "—"}</TableCell>
            <TableCell>{r.tenantVrf ?? "—"}</TableCell>
            <TableCell>{r.numMacs ?? 0}</TableCell>
            <TableCell>{r.numArpNd ?? 0}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function ExitNodeHealth({ status }: { status: EvpnStatus }) {
  if (status.exitNodes.length === 0) {
    return <EmptyState icon="vxlan" variant="unconfigured" title="No EVPN exit nodes configured" />;
  }
  return (
    <ul className="flex flex-col gap-1">
      {status.exitNodes.map((en) => (
        <li key={`${en.zone}-${en.node}`} className="flex items-center gap-2 text-sm">
          <StatusDot status={en.healthy ? "ok" : "down"} />
          <span className="font-medium">{en.node}</span>
          <span className="text-fg-subtle">zone {en.zone}</span>
          <span className={en.healthy ? "text-emerald-600 dark:text-emerald-400" : "text-red-600 dark:text-red-400"}>
            {en.healthy ? "healthy" : "unhealthy"}
          </span>
          {en.detail && <span className="text-xs text-slate-600 dark:text-slate-400">— {en.detail}</span>}
        </li>
      ))}
    </ul>
  );
}

export function EvpnView({ enabled = true }: { enabled?: boolean }) {
  const { data: status, isLoading, isError, refetch } = useEvpnStatusQuery({ enabled });
  const [selection, setSelection] = useState<EvpnSelection | undefined>(undefined);

  if (isLoading) {
    return <p className="text-sm text-slate-600 dark:text-slate-400">Loading EVPN/BGP status…</p>;
  }
  if (isError || !status) {
    return (
      <EmptyState
        icon="vxlan"
        variant="failed"
        title="Could not load EVPN/BGP status"
        description="Check that vnproxd can reach FRR on the cluster's nodes, then reload."
        action={
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        }
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {status.partial && status.failedNodes && status.failedNodes.length > 0 && (
        <div className="rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
          Partial results — could not reach: {status.failedNodes.join(", ")}
        </div>
      )}

      <FindingsList
        findings={status.findings.map(toFindingItem)}
        emptyTitle="No flapping sessions"
        emptyDescription="Every observed BGP/EVPN session has been stable."
      />

      <div>
        <h2 className="mb-2 flex items-center gap-1.5 text-base font-semibold">
          Peering matrix
          <HelpAnchor topic="sdn-evpn" />
        </h2>
        <div className="grid min-h-0 grid-cols-1 gap-3 lg:grid-cols-[1fr_320px]">
          <PeeringMatrix status={status} selection={selection} onSelect={setSelection} />
          <div className="rounded-lg border border-slate-200 p-4 dark:border-slate-800">
            <SessionDetail status={status} selection={selection} />
          </div>
        </div>
      </div>

      <div>
        <h2 className="mb-2 text-base font-semibold">EVPN VNIs</h2>
        <VniList status={status} />
      </div>

      <div>
        <h2 className="mb-2 text-base font-semibold">Exit-node health</h2>
        <ExitNodeHealth status={status} />
      </div>
    </div>
  );
}
