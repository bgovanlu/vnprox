// SPDX-License-Identifier: Apache-2.0

// SDN Controllers view (T-3102, docs/features/sdn.md): controller list with
// type badge, a create/edit form whose fields reveal per the selected type,
// deletion blocked (server-side, criterion 5) while a zone still references
// the controller. Controllers were a bare string on a zone before this task
// (SdnZone.controller); this view gives them their own inspector — the same
// "new SDN object family gets its own status view" precedent FabricsView.tsx
// set for fabrics (T-3101), not new topology-map geometry.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { Button } from "../../components/Button";
import { EmptyState } from "../../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../../components/Table";
import { HelpAnchor } from "../../help/HelpAnchor";
import type { SdnController } from "../../api/types";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";
import {
  buildSdnControllerCreateOp,
  buildSdnControllerDeleteOp,
  buildSdnControllerUpdateOp,
  type SdnControllerFormValues,
} from "../../changesets/opBuilders";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { EditorDialog } from "../../changesets/editors/EditorDialog";
import { useEditorSubmit } from "../../changesets/editors/useEditorSubmit";
import { SdnConfirmDeleteDialog } from "../editors/SdnConfirmDeleteDialog";
import { useSdnQuery } from "../queries";

const CONTROLLER_TYPES = [
  { value: "bgp", label: "BGP" },
  { value: "evpn", label: "EVPN" },
  { value: "faucet", label: "Faucet" },
  { value: "isis", label: "IS-IS" },
];

function typeLabel(type: string): string {
  return CONTROLLER_TYPES.find((t) => t.value === type)?.label ?? type;
}

function parseList(text: string): string[] {
  return text
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

function emptyControllerForm(): SdnControllerFormValues {
  return {
    type: "bgp",
    bgpMode: "",
    fabric: "",
    isisDomain: "",
    isisNet: "",
    loopback: "",
    node: "",
    peerGroupName: "",
    routeMapIn: "",
    routeMapOut: "",
    nodes: [],
    peers: [],
    isisIfaces: [],
    asn: 0,
    ebgpMultihop: 0,
    ebgp: false,
    bgpMultipathAsPathRelax: false,
  };
}

function controllerToForm(ctl: SdnController): SdnControllerFormValues {
  return {
    type: ctl.type,
    bgpMode: ctl.bgpMode ?? "",
    fabric: ctl.fabric ?? "",
    isisDomain: ctl.isisDomain ?? "",
    isisNet: ctl.isisNet ?? "",
    loopback: ctl.loopback ?? "",
    node: ctl.node ?? "",
    peerGroupName: ctl.peerGroupName ?? "",
    routeMapIn: ctl.routeMapIn ?? "",
    routeMapOut: ctl.routeMapOut ?? "",
    nodes: ctl.nodes ?? [],
    peers: ctl.peers ?? [],
    isisIfaces: ctl.isisIfaces ?? [],
    asn: ctl.asn ?? 0,
    ebgpMultihop: ctl.ebgpMultihop ?? 0,
    ebgp: ctl.ebgp ?? false,
    bgpMultipathAsPathRelax: ctl.bgpMultipathAsPathRelax ?? false,
  };
}

interface ControllerFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Existing controller — undefined for create. */
  existing?: SdnController;
}

// The create/edit form (a single form, not a multi-step wizard — a
// controller has no multi-object graph to build the way a zone+vnet+subnet
// does). Fields reveal per the selected type, mirroring FabricsView's own
// protocol-conditional form.
function ControllerFormDialog({ open, onOpenChange, existing }: ControllerFormDialogProps) {
  const { data: session } = useSession();
  const { findings, submit } = useEditorSubmit();

  const initial = existing ? controllerToForm(existing) : emptyControllerForm();
  const [controllerId, setControllerId] = useState("");
  const [type, setType] = useState(initial.type);
  const [bgpMode, setBgpMode] = useState(initial.bgpMode);
  const [asn, setAsn] = useState(initial.asn);
  const [ebgp, setEbgp] = useState(initial.ebgp);
  const [ebgpMultihop, setEbgpMultihop] = useState(initial.ebgpMultihop);
  const [bgpMultipathAsPathRelax, setBgpMultipathAsPathRelax] = useState(initial.bgpMultipathAsPathRelax);
  const [peersText, setPeersText] = useState(initial.peers.join(", "));
  const [fabric, setFabric] = useState(initial.fabric);
  const [peerGroupName, setPeerGroupName] = useState(initial.peerGroupName);
  const [routeMapIn, setRouteMapIn] = useState(initial.routeMapIn);
  const [routeMapOut, setRouteMapOut] = useState(initial.routeMapOut);
  const [isisDomain, setIsisDomain] = useState(initial.isisDomain);
  const [isisNet, setIsisNet] = useState(initial.isisNet);
  const [isisIfacesText, setIsisIfacesText] = useState(initial.isisIfaces.join(", "));
  const [node, setNode] = useState(initial.node);
  const [nodesText, setNodesText] = useState(initial.nodes.join(", "));
  const [loopback, setLoopback] = useState(initial.loopback);

  const isCreate = !existing;
  const target = existing ? `sdn-controller::${existing.id}` : `sdn-controller::${controllerId}`;
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");

  function handleSubmit(): void {
    const form: SdnControllerFormValues = {
      type, bgpMode, asn, ebgp, ebgpMultihop, bgpMultipathAsPathRelax,
      peers: parseList(peersText), fabric, peerGroupName, routeMapIn, routeMapOut,
      isisDomain, isisNet, isisIfaces: parseList(isisIfacesText),
      node, nodes: parseList(nodesText), loopback,
    };
    const op = isCreate ? buildSdnControllerCreateOp(target, form) : buildSdnControllerUpdateOp(target, initial, form);
    const label = isCreate ? controllerId : existing.id;
    submit([op], `Edit sdn controller ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? "Create SDN controller" : `Edit SDN controller ${existing.id}`}
      description="A controller configures a BGP/EVPN/Faucet/IS-IS underlay control plane a zone can reference by id via its own controller field."
      onSubmit={handleSubmit}
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="ID" help="Starts with a letter, ends with a letter or digit; letters, digits, underscores and hyphens in between.">
          <input className={inputClass} value={controllerId} onChange={(e) => { setControllerId(e.target.value); }} placeholder="bgp1" />
        </Field>
      )}

      {isCreate && (
        <Field label="Type" help="Not editable after creation — changing type is a delete+create.">
          <select className={inputClass} value={type} onChange={(e) => { setType(e.target.value); }}>
            {CONTROLLER_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>
      )}

      <Field label="Node" help="Optional — a single cluster node this controller is scoped to.">
        <input className={inputClass} value={node} onChange={(e) => { setNode(e.target.value); }} />
      </Field>
      <Field label="Nodes" help="Comma-separated cluster node names.">
        <input className={inputClass} value={nodesText} onChange={(e) => { setNodesText(e.target.value); }} />
      </Field>
      <Field label="Loopback" help="Name of the loopback/dummy interface that provides the Router-IP.">
        <input className={inputClass} value={loopback} onChange={(e) => { setLoopback(e.target.value); }} />
      </Field>

      {type === "bgp" && (
        <>
          <Field label="ASN" help="Autonomous system number, 0-4294967295.">
            <input type="number" className={inputClass} value={asn} onChange={(e) => { setAsn(Number(e.target.value)); }} />
          </Field>
          <Field label="BGP mode" help="auto | external | internal — whether to use eBGP or iBGP.">
            <select className={inputClass} value={bgpMode} onChange={(e) => { setBgpMode(e.target.value); }}>
              <option value="">(default: auto)</option>
              <option value="auto">auto</option>
              <option value="external">external</option>
              <option value="internal">internal</option>
            </select>
          </Field>
          <Field label="eBGP" help="Enable eBGP (remote-as external).">
            <input type="checkbox" checked={ebgp} onChange={(e) => { setEbgp(e.target.checked); }} />
          </Field>
          <Field label="eBGP multihop" help="Maximum amount of hops for eBGP peers.">
            <input type="number" className={inputClass} value={ebgpMultihop} onChange={(e) => { setEbgpMultihop(Number(e.target.value)); }} />
          </Field>
          <Field label="BGP multipath AS-path relax" help="Consider different AS paths of equal length for multipath computation.">
            <input type="checkbox" checked={bgpMultipathAsPathRelax} onChange={(e) => { setBgpMultipathAsPathRelax(e.target.checked); }} />
          </Field>
          <Field label="Peers" help="Comma-separated peer address list.">
            <input className={inputClass} value={peersText} onChange={(e) => { setPeersText(e.target.value); }} />
          </Field>
        </>
      )}

      {type === "evpn" && (
        <>
          <Field label="Fabric" help="SDN fabric to use as underlay for this EVPN controller.">
            <input className={inputClass} value={fabric} onChange={(e) => { setFabric(e.target.value); }} />
          </Field>
          <Field label="Peer group name" help="Name of the peer group for this EVPN controller (default VTEP).">
            <input className={inputClass} value={peerGroupName} onChange={(e) => { setPeerGroupName(e.target.value); }} placeholder="VTEP" />
          </Field>
          <Field label="Route map in" help="Route map applied for incoming routes.">
            <input className={inputClass} value={routeMapIn} onChange={(e) => { setRouteMapIn(e.target.value); }} />
          </Field>
          <Field label="Route map out" help="Route map applied for outgoing routes.">
            <input className={inputClass} value={routeMapOut} onChange={(e) => { setRouteMapOut(e.target.value); }} />
          </Field>
        </>
      )}

      {type === "isis" && (
        <>
          <Field label="IS-IS domain" help="Name of the IS-IS domain.">
            <input className={inputClass} value={isisDomain} onChange={(e) => { setIsisDomain(e.target.value); }} />
          </Field>
          <Field label="IS-IS interfaces" help="Comma-separated interfaces where IS-IS should be active.">
            <input className={inputClass} value={isisIfacesText} onChange={(e) => { setIsisIfacesText(e.target.value); }} />
          </Field>
          <Field label="IS-IS net" help="Network Entity title for this node in the IS-IS network.">
            <input className={inputClass} value={isisNet} onChange={(e) => { setIsisNet(e.target.value); }} placeholder="49.0001.0000.0000.0001.00" />
          </Field>
        </>
      )}
    </EditorDialog>
  );
}

function ControllerRow({
  controller,
  gateDisabled,
  gateTitle,
  onEdit,
  onDelete,
}: {
  controller: SdnController;
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <TableRow>
      <TableCell className="font-medium">{controller.id}</TableCell>
      <TableCell>{typeLabel(controller.type)}</TableCell>
      <TableCell>{controller.pending ?? "—"}</TableCell>
      <TableCell>{controller.asn ?? "—"}</TableCell>
      <TableCell>{controller.peers && controller.peers.length > 0 ? controller.peers.join(", ") : "—"}</TableCell>
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

function ControllersTable({
  controllers,
  gateDisabled,
  gateTitle,
  onEdit,
  onDelete,
  onCreate,
}: {
  controllers: SdnController[];
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: (controller: SdnController) => void;
  onDelete: (controller: SdnController) => void;
  onCreate: () => void;
}) {
  if (controllers.length === 0) {
    return (
      <EmptyState
        icon="sdn-zone"
        variant="unconfigured"
        title="No SDN controllers configured"
        description="A controller configures a BGP/EVPN/Faucet/IS-IS underlay control plane a zone can reference by id. Create one to get started."
        action={
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onCreate}>
            + New controller
          </Button>
        }
      />
    );
  }
  return (
    <Table aria-label="SDN controllers">
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Pending</TableHead>
          <TableHead>ASN</TableHead>
          <TableHead>Peers</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {controllers.map((c) => (
          <ControllerRow
            key={c.id}
            controller={c}
            gateDisabled={gateDisabled}
            gateTitle={gateTitle}
            onEdit={() => { onEdit(c); }}
            onDelete={() => { onDelete(c); }}
          />
        ))}
      </TableBody>
    </Table>
  );
}

export function ControllersView() {
  const { data: session } = useSession();
  const { data: tree, isLoading, isError, refetch } = useSdnQuery();
  const [createOpen, setCreateOpen] = useState(false);
  const [editingController, setEditingController] = useState<SdnController | undefined>(undefined);
  const [deletingController, setDeletingController] = useState<SdnController | undefined>(undefined);

  const canWrite = hasAnyCap(session, "sdnWrite");
  const gateTitle = canWrite ? undefined : missingCapTooltip(session, "", "sdnWrite");

  if (isLoading) {
    return <p className="text-sm text-fg-muted">Loading SDN controllers…</p>;
  }
  if (isError || !tree) {
    return (
      <EmptyState
        icon="sdn-zone"
        variant="failed"
        title="Could not load SDN controllers"
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
          Controllers
          <HelpAnchor topic="sdn-controllers" />
        </h2>
        <Button
          variant="primary"
          size="sm"
          disabled={!canWrite}
          title={gateTitle}
          onClick={() => { setCreateOpen(true); }}
        >
          + New controller
        </Button>
      </div>

      <ControllersTable
        controllers={tree.controllers}
        gateDisabled={!canWrite}
        gateTitle={gateTitle}
        onEdit={setEditingController}
        onDelete={setDeletingController}
        onCreate={() => { setCreateOpen(true); }}
      />

      {createOpen && <ControllerFormDialog open={createOpen} onOpenChange={setCreateOpen} />}
      {editingController && (
        <ControllerFormDialog
          open={!!editingController}
          onOpenChange={(open) => { if (!open) setEditingController(undefined); }}
          existing={editingController}
        />
      )}
      {deletingController && (
        <SdnConfirmDeleteDialog
          open={!!deletingController}
          onOpenChange={(open) => { if (!open) setDeletingController(undefined); }}
          title={`Delete controller ${deletingController.id}?`}
          description="Blocked if any zone still references this controller. This adds a delete op to the current changeset draft."
          op={buildSdnControllerDeleteOp(`sdn-controller::${deletingController.id}`)}
          changesetTitle={`Delete sdn controller ${deletingController.id}`}
        />
      )}
    </div>
  );
}
