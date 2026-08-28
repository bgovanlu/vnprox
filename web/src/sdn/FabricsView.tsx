// SPDX-License-Identifier: Apache-2.0

// SDN Fabrics view (T-3101, docs/features/sdn.md §6): fabric list with
// protocol badge + per-node membership status, a create/edit form whose
// fields reveal per the selected protocol, and read-only tables for the
// two BGP route-policy families the same capture exposed (prefix-lists,
// route-maps). Lives alongside T-401's tree/detail panels, T-404's
// EvpnView, and T-406's DhcpView in the same SDN cockpit page (SdnPage.tsx
// switches between them via a tab) — a dedicated status view, not new
// topology-map geometry (EVPN is the precedent for that choice: a complex
// SDN L3 concept gets its own panel, not new map edges).
//
// WireGuard separation (see this task's report for the full statement):
// this file imports nothing from web/src/wireguard/ (T-1401's tunnel UI)
// and is imported by nothing there. A `protocol: "wireguard"` fabric gets
// its own badge text ("WireGuard fabric") distinct from a tunnel's — no
// shared component renders both, and StatusDot (the one component this
// file does share with the rest of the SDN cockpit) carries no WireGuard-
// specific meaning at all, it is a plain ok/down/degraded/unknown dot.
import { useState } from "react";
import { useSession } from "../api/useSession";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import type { SdnFabric, SdnPrefixList, SdnRouteMap } from "../api/types";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import {
  buildSdnFabricCreateOp,
  buildSdnFabricDeleteOp,
  buildSdnFabricUpdateOp,
  type SdnFabricFormValues,
} from "../changesets/opBuilders";
import { Field, inputClass } from "../changesets/editors/EditorDialog";
import { EditorDialog } from "../changesets/editors/EditorDialog";
import { useEditorSubmit } from "../changesets/editors/useEditorSubmit";
import { SdnConfirmDeleteDialog } from "./editors/SdnConfirmDeleteDialog";
import { StatusDot } from "./StatusDot";
import { useSdnQuery } from "./queries";

const FABRIC_PROTOCOLS = [
  { value: "bgp", label: "BGP" },
  { value: "openfabric", label: "OpenFabric" },
  { value: "ospf", label: "OSPF" },
  { value: "wireguard", label: "WireGuard" },
];

function protocolLabel(protocol: string): string {
  return FABRIC_PROTOCOLS.find((p) => p.value === protocol)?.label ?? protocol;
}

function emptyFabricForm(): SdnFabricFormValues {
  return {
    protocol: "bgp",
    ipPrefix: "",
    ip6Prefix: "",
    csnpInterval: 0,
    helloInterval: 0,
    routeFilter: "",
    area: "",
    redistribute: [],
    persistentKeepalive: 0,
  };
}

function fabricToForm(fabric: SdnFabric): SdnFabricFormValues {
  return {
    protocol: fabric.protocol,
    ipPrefix: fabric.ipPrefix ?? "",
    ip6Prefix: fabric.ip6Prefix ?? "",
    csnpInterval: fabric.csnpInterval ?? 0,
    helloInterval: fabric.helloInterval ?? 0,
    routeFilter: fabric.routeFilter ?? "",
    area: fabric.area ?? "",
    redistribute: fabric.redistribute ?? [],
    persistentKeepalive: fabric.persistentKeepalive ?? 0,
  };
}

function parseRedistribute(text: string): string[] {
  return text
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

interface FabricFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Existing fabric — undefined for create. */
  existing?: SdnFabric;
}

// The create/edit form (a single form, not a multi-step wizard — a fabric
// has no multi-object graph to build the way a zone+vnet+subnet does).
// Fields reveal per the selected protocol, mirroring SdnZoneEditor's own
// type-conditional field convention.
function FabricFormDialog({ open, onOpenChange, existing }: FabricFormDialogProps) {
  const { data: session } = useSession();
  const { findings, submit } = useEditorSubmit();

  const initial = existing ? fabricToForm(existing) : emptyFabricForm();
  const [fabricId, setFabricId] = useState("");
  const [protocol, setProtocol] = useState(initial.protocol);
  const [ipPrefix, setIpPrefix] = useState(initial.ipPrefix);
  const [ip6Prefix, setIp6Prefix] = useState(initial.ip6Prefix);
  const [csnpInterval, setCsnpInterval] = useState(initial.csnpInterval);
  const [helloInterval, setHelloInterval] = useState(initial.helloInterval);
  const [routeFilter, setRouteFilter] = useState(initial.routeFilter);
  const [area, setArea] = useState(initial.area);
  const [redistributeText, setRedistributeText] = useState(initial.redistribute.join(", "));
  const [persistentKeepalive, setPersistentKeepalive] = useState(initial.persistentKeepalive);

  const isCreate = !existing;
  const target = existing ? `sdn-fabric::${existing.id}` : `sdn-fabric::${fabricId}`;
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");

  function handleSubmit(): void {
    const form: SdnFabricFormValues = {
      protocol,
      ipPrefix,
      ip6Prefix,
      csnpInterval,
      helloInterval,
      routeFilter,
      area,
      redistribute: parseRedistribute(redistributeText),
      persistentKeepalive,
    };
    const op = isCreate ? buildSdnFabricCreateOp(target, form) : buildSdnFabricUpdateOp(target, initial, form);
    const label = isCreate ? fabricId : existing.id;
    submit([op], `Edit sdn fabric ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? "Create SDN fabric" : `Edit SDN fabric ${existing.id}`}
      description="A fabric configures underlay routing (BGP, OpenFabric, OSPF, or WireGuard) that a vxlan/evpn zone can ride on via its own fabric field."
      onSubmit={handleSubmit}
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="ID" help="2-8 characters, alphanumeric with interior hyphens (e.g. fab-01). Proxmox rejects other shapes.">
          <input className={inputClass} value={fabricId} onChange={(e) => { setFabricId(e.target.value); }} placeholder="fab-01" />
        </Field>
      )}

      {isCreate && (
        <Field label="Protocol" help="Not editable after creation — changing protocol is a delete+create.">
          <select className={inputClass} value={protocol} onChange={(e) => { setProtocol(e.target.value); }}>
            {FABRIC_PROTOCOLS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        </Field>
      )}

      <Field label="IPv4 prefix" help="The IP prefix used to allocate each member node's underlay address.">
        <input className={inputClass} value={ipPrefix} onChange={(e) => { setIpPrefix(e.target.value); }} placeholder="10.255.0.0/24" />
      </Field>
      <Field label="IPv6 prefix" help="Optional — the IPv6 equivalent of the prefix above.">
        <input className={inputClass} value={ip6Prefix} onChange={(e) => { setIp6Prefix(e.target.value); }} />
      </Field>

      {protocol === "openfabric" && (
        <>
          <Field label="CSNP interval (seconds)" help="1-600. Leave 0 to use PVE's default.">
            <input type="number" className={inputClass} value={csnpInterval} onChange={(e) => { setCsnpInterval(Number(e.target.value)); }} />
          </Field>
          <Field label="Hello interval (seconds)" help="1-600. Leave 0 to use PVE's default.">
            <input type="number" className={inputClass} value={helloInterval} onChange={(e) => { setHelloInterval(Number(e.target.value)); }} />
          </Field>
          <Field label="Route filter" help="A prefix-list name to filter routes installed into the kernel routing table.">
            <input className={inputClass} value={routeFilter} onChange={(e) => { setRouteFilter(e.target.value); }} />
          </Field>
        </>
      )}

      {protocol === "ospf" && (
        <>
          <Field label="Area" help="OSPF area — an IPv4 address or a 32-bit number.">
            <input className={inputClass} value={area} onChange={(e) => { setArea(e.target.value); }} placeholder="0.0.0.0" />
          </Field>
          <Field label="Redistribute" help="Comma-separated route sources to redistribute (e.g. connected, static).">
            <input className={inputClass} value={redistributeText} onChange={(e) => { setRedistributeText(e.target.value); }} />
          </Field>
          <Field label="Route filter" help="A prefix-list name to filter routes installed into the kernel routing table.">
            <input className={inputClass} value={routeFilter} onChange={(e) => { setRouteFilter(e.target.value); }} />
          </Field>
        </>
      )}

      {protocol === "bgp" && (
        <Field label="Redistribute" help="Comma-separated route sources to redistribute (e.g. connected, static).">
          <input className={inputClass} value={redistributeText} onChange={(e) => { setRedistributeText(e.target.value); }} />
        </Field>
      )}

      {protocol === "wireguard" && (
        <Field
          label="Persistent keepalive (seconds)"
          help="0-65535. How often to send a keepalive packet — useful behind NAT/stateful firewalls. 0 turns it off."
        >
          <input type="number" className={inputClass} value={persistentKeepalive} onChange={(e) => { setPersistentKeepalive(Number(e.target.value)); }} />
        </Field>
      )}
    </EditorDialog>
  );
}

function FabricRow({
  fabric,
  gateDisabled,
  gateTitle,
  onEdit,
  onDelete,
}: {
  fabric: SdnFabric;
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <TableRow>
      <TableCell className="font-medium">{fabric.id}</TableCell>
      <TableCell>{protocolLabel(fabric.protocol)}</TableCell>
      <TableCell>{fabric.pending ?? "—"}</TableCell>
      <TableCell>
        {fabric.nodeStatus.length === 0 ? (
          <span className="text-fg-subtle">no member nodes reported</span>
        ) : (
          <div className="flex flex-wrap gap-2">
            {fabric.nodeStatus.map((ns) => (
              <span key={ns.node} className="inline-flex items-center gap-1 text-xs">
                <StatusDot status={ns.status === "ok" ? "ok" : "unknown"} />
                {ns.node}
              </span>
            ))}
          </div>
        )}
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-1.5">
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onEdit}>
            Edit
          </Button>
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onDelete}>
            Delete
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function FabricsTable({
  fabrics,
  gateDisabled,
  gateTitle,
  onEdit,
  onDelete,
  onCreate,
}: {
  fabrics: SdnFabric[];
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: (fabric: SdnFabric) => void;
  onDelete: (fabric: SdnFabric) => void;
  onCreate: () => void;
}) {
  if (fabrics.length === 0) {
    return (
      <EmptyState
        icon="sdn-fabric"
        variant="unconfigured"
        title="No SDN fabrics configured"
        description="A fabric configures underlay routing a vxlan/evpn zone can ride on. Create one to get started."
        action={
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onCreate}>
            + New fabric
          </Button>
        }
      />
    );
  }
  return (
    <Table aria-label="SDN fabrics">
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Protocol</TableHead>
          <TableHead>Pending</TableHead>
          <TableHead>Member nodes</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {fabrics.map((f) => (
          <FabricRow
            key={f.id}
            fabric={f}
            gateDisabled={gateDisabled}
            gateTitle={gateTitle}
            onEdit={() => { onEdit(f); }}
            onDelete={() => { onDelete(f); }}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function PrefixListsTable({ items }: { items: SdnPrefixList[] }) {
  if (items.length === 0) {
    return <EmptyState icon="sdn-fabric" variant="unconfigured" title="No prefix-lists configured" />;
  }
  return (
    <Table aria-label="SDN prefix-lists">
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((pl) => (
          <TableRow key={pl.id}>
            <TableCell>{pl.id}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function RouteMapsTable({ items }: { items: SdnRouteMap[] }) {
  if (items.length === 0) {
    return <EmptyState icon="sdn-fabric" variant="unconfigured" title="No route-maps configured" />;
  }
  return (
    <Table aria-label="SDN route-maps">
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((rm) => (
          <TableRow key={rm.id}>
            <TableCell>{rm.id}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function FabricsView() {
  const { data: session } = useSession();
  const { data: tree, isLoading, isError, refetch } = useSdnQuery();
  const [createOpen, setCreateOpen] = useState(false);
  const [editingFabric, setEditingFabric] = useState<SdnFabric | undefined>(undefined);
  const [deletingFabric, setDeletingFabric] = useState<SdnFabric | undefined>(undefined);

  const canWrite = hasAnyCap(session, "sdnWrite");
  const gateTitle = canWrite ? undefined : missingCapTooltip(session, "", "sdnWrite");

  if (isLoading) {
    return <p className="text-sm text-slate-600 dark:text-slate-400">Loading SDN fabrics…</p>;
  }
  if (isError || !tree) {
    return (
      <EmptyState
        icon="sdn-fabric"
        variant="failed"
        title="Could not load SDN fabrics"
        description="Check that vnproxd can reach the local PVE API, then reload."
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
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-base font-semibold">
          Fabrics
          <HelpAnchor topic="sdn-fabrics" />
        </h2>
        <Button
          variant="primary"
          size="sm"
          disabled={!canWrite}
          title={gateTitle}
          onClick={() => { setCreateOpen(true); }}
        >
          + New fabric
        </Button>
      </div>

      <FabricsTable
        fabrics={tree.fabrics}
        gateDisabled={!canWrite}
        gateTitle={gateTitle}
        onEdit={setEditingFabric}
        onDelete={setDeletingFabric}
        onCreate={() => { setCreateOpen(true); }}
      />

      <div>
        <h2 className="mb-2 text-base font-semibold">Prefix-lists</h2>
        <PrefixListsTable items={tree.prefixLists} />
      </div>

      <div>
        <h2 className="mb-2 text-base font-semibold">Route-maps</h2>
        <RouteMapsTable items={tree.routeMaps} />
      </div>

      {createOpen && <FabricFormDialog open={createOpen} onOpenChange={setCreateOpen} />}
      {editingFabric && (
        <FabricFormDialog
          open={!!editingFabric}
          onOpenChange={(open) => { if (!open) setEditingFabric(undefined); }}
          existing={editingFabric}
        />
      )}
      {deletingFabric && (
        <SdnConfirmDeleteDialog
          open={!!deletingFabric}
          onOpenChange={(open) => { if (!open) setDeletingFabric(undefined); }}
          title={`Delete fabric ${deletingFabric.id}?`}
          description="Zones riding on this fabric as their underlay will lose it. This adds a delete op to the current changeset draft."
          op={buildSdnFabricDeleteOp(`sdn-fabric::${deletingFabric.id}`)}
          changesetTitle={`Delete sdn fabric ${deletingFabric.id}`}
        />
      )}
    </div>
  );
}
