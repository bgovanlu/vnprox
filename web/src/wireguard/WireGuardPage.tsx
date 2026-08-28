// SPDX-License-Identifier: Apache-2.0

// T-4015: the general (non-federation-scoped) WireGuard tunnel management
// surface — list every tunnel this node manages with its live state, create
// a tunnel, add/edit/remove peers, and view handshake/transfer status.
// Deliberately separate from ConnectClustersWizard.tsx (T-1402), which stays
// unchanged: that wizard is still the fast path for "stand up a tunnel AND
// its firewall rule for a federated cluster in one guided flow", while this
// page is the fast path for "manage every tunnel, including ones that have
// nothing to do with federation". Both build the exact same wg.* op family
// via the exact same target-string helpers (wgTunnelTarget/wgPeerTarget,
// imported from wizardOps.ts here rather than redefined — see wgTunnelOps.ts's
// doc comment) and both stage through useDrawerActions()/useEditorSubmit(),
// so "one op vocabulary, two entry points" (T-4015 AC1) holds by
// construction, not by convention alone.
//
// Key custody, restated for this surface specifically (see this task's
// report for the full statement): nothing rendered here is ever a private
// key. WireGuardTunnel (api/types.ts) has no private-key field at all — the
// wire type simply cannot carry one — and the two read routes this page
// calls beyond the tunnel list (`pubkey`, `peer-config`) are documented as
// derived-public-key-only and export-only respectively. A peer's preshared
// key is write-only: typed into the add/edit form, never redisplayed once
// staged (WgPeerAddParams.presharedKey's own doc comment) — the "current"
// value edited in-place always starts blank, exactly like changing a
// password field never shows the old one.
import { useMemo, useState } from "react";
import { useSession } from "../api/useSession";
import type { WireGuardPeer, WireGuardTunnel } from "../api/types";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { capsForNode, missingCapTooltip } from "../changesets/capabilities";
import { Field, inputClass } from "../changesets/editors/EditorDialog";
import { EditorDialog } from "../changesets/editors/EditorDialog";
import { useEditorSubmit } from "../changesets/editors/useEditorSubmit";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { HelpAnchor } from "../help/HelpAnchor";
import { formatBytes } from "../microseg/format";
import { formatAge } from "../lib/freshness";
import { useClusterNodes } from "../sdn/wizards/useClusterNodes";
import { useFederationClustersQuery } from "../topology/federation/federationQueries";
import {
  useWireGuardPeerConfigQuery,
  useWireGuardPubkeyQuery,
  useWireGuardTunnelsQuery,
} from "./wgTunnelsQuery";
import { wgTunnelState, type WgTunnelState } from "./wgTunnelState";
import {
  buildWgPeerAddOp,
  buildWgPeerRemoveOp,
  buildWgTunnelCreateOp,
  buildWgTunnelDeleteOp,
  buildWgTunnelUpdateOp,
  emptyWgPeerForm,
  emptyWgTunnelForm,
  looksLikeWgKey,
  parseAddressList,
  type WgPeerFormValues,
  type WgTunnelFormValues,
} from "./wgTunnelOps";

// Mirrors GlobalTopologyView.tsx's InterconnectState visual vocabulary
// exactly (emerald/red/slate for up/down/unknown) — a tunnel's state reads
// the same color whether you're looking at the federation map's edges or
// this page's rows, even though the two components share no code beyond
// the wgTunnelState/tunnelHasFreshHandshake derivation itself.
const STATE_BADGE_CLASS: Record<WgTunnelState, string> = {
  up: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-200",
  down: "border-red-300 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-900/30 dark:text-red-200",
  unknown: "border-slate-300 bg-slate-100 text-slate-700 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300",
};

const STATE_LABEL: Record<WgTunnelState, string> = { up: "up", down: "down", unknown: "unknown" };

function StateBadge({ state }: { state: WgTunnelState }) {
  return (
    <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${STATE_BADGE_CLASS[state]}`}>
      {STATE_LABEL[state]}
    </span>
  );
}

function tunnelTitle(t: WireGuardTunnel): string {
  return `${t.ifName} on ${t.node}`;
}

// --- Create/edit tunnel ----------------------------------------------------

interface TunnelFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing?: WireGuardTunnel;
}

function TunnelFormDialog({ open, onOpenChange, existing }: TunnelFormDialogProps) {
  const { data: session } = useSession();
  const { findings, submit } = useEditorSubmit();
  const clusterNodes = useClusterNodes();

  const isCreate = !existing;
  const initial: WgTunnelFormValues = existing
    ? {
        ifName: existing.ifName,
        carrier: existing.carrier ?? "",
        addresses: existing.addresses,
        listenPort: existing.listenPort,
        mtu: existing.mtu,
      }
    : emptyWgTunnelForm();

  const [node, setNode] = useState(existing?.node ?? "");
  const [ifName, setIfName] = useState(initial.ifName);
  const [carrier, setCarrier] = useState(initial.carrier);
  const [addressesRaw, setAddressesRaw] = useState(initial.addresses.join(", "));
  const [listenPort, setListenPort] = useState(initial.listenPort);
  const [mtu, setMtu] = useState(initial.mtu);
  // Generated once per dialog instance so the same id is used if the create
  // submit needs to be re-attempted (useEditorSubmit's amend-in-place path)
  // — mirrors ConnectClustersWizard.tsx's identical tunnelId convention.
  const [tunnelId] = useState(() => existing?.id ?? crypto.randomUUID());

  const form: WgTunnelFormValues = useMemo(
    () => ({ ifName, carrier, addresses: parseAddressList(addressesRaw), listenPort, mtu }),
    [ifName, carrier, addressesRaw, listenPort, mtu],
  );

  const cap = capsForNode(session, node);
  const capDenied = node !== "" && !cap.netWrite;
  const capReason = node ? missingCapTooltip(session, node, "netWrite") : undefined;
  const target = isCreate ? `wg-tunnel:${node}:${tunnelId}` : `wg-tunnel:${existing.node}:${existing.id}`;

  function handleSubmit(): void {
    const op = isCreate
      ? buildWgTunnelCreateOp(node, tunnelId, form)
      : buildWgTunnelUpdateOp(existing.node, existing.id, initial, form);
    const label = isCreate ? `${ifName} on ${node}` : tunnelTitle(existing);
    submit([op], isCreate ? `Create WireGuard tunnel ${label}` : `Edit WireGuard tunnel ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? "Create WireGuard tunnel" : `Edit tunnel ${tunnelTitle(existing)}`}
      description="A tunnel creates one on-node WireGuard interface with its own keypair, generated on the owning node when the changeset applies — never here, never before then."
      onSubmit={handleSubmit}
      disabledReason={capDenied ? capReason : node === "" ? "Pick a node first." : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="Node" help="The node this tunnel's interface and keypair are created on.">
          <select className={inputClass} value={node} onChange={(e) => { setNode(e.target.value); }}>
            <option value="">Pick a node…</option>
            {clusterNodes.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </Field>
      )}
      {isCreate ? (
        <Field label="Interface name" help="On-node WireGuard interface, e.g. wg0. Not editable after creation — renaming is delete+recreate.">
          <input className={inputClass} value={ifName} onChange={(e) => { setIfName(e.target.value); }} placeholder="wg0" />
        </Field>
      ) : (
        <Field label="Interface name" help="Not editable — renaming a tunnel's interface is delete+recreate.">
          <input className={inputClass} value={ifName} disabled />
        </Field>
      )}
      <Field label="Listen port" help="The UDP port this tunnel listens on.">
        <input
          type="number"
          className={inputClass}
          value={listenPort || ""}
          onChange={(e) => { setListenPort(Number(e.target.value)); }}
          min={1}
          max={65535}
        />
      </Field>
      <Field label="Tunnel addresses (CIDR, comma-separated)" help="This tunnel's own address(es), e.g. 10.10.0.1/24.">
        <input className={inputClass} value={addressesRaw} onChange={(e) => { setAddressesRaw(e.target.value); }} placeholder="10.10.0.1/24" />
      </Field>
      <Field label="Carrier interface" help="The underlying interface the tunnel's endpoint rides on, e.g. vmbr0. Leave blank if none.">
        <input className={inputClass} value={carrier} onChange={(e) => { setCarrier(e.target.value); }} placeholder="vmbr0" />
      </Field>
      <Field label="MTU" help="0 uses the interface default.">
        <input type="number" className={inputClass} value={mtu || ""} onChange={(e) => { setMtu(Number(e.target.value)); }} min={0} />
      </Field>
    </EditorDialog>
  );
}

// --- Delete tunnel confirm --------------------------------------------------

function DeleteTunnelDialog({
  open,
  onOpenChange,
  tunnel,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tunnel: WireGuardTunnel;
}) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const disabledReason = missingCapTooltip(session, tunnel.node, "netWrite");

  function handleSubmit(): void {
    void addOps([buildWgTunnelDeleteOp(tunnel.node, tunnel.id)], `Delete WireGuard tunnel ${tunnelTitle(tunnel)}`)
      .then(() => {
        onOpenChange(false);
        toast({ title: "Added to changeset" });
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete tunnel ${tunnelTitle(tunnel)}?`}
      description="Removes the on-node interface/config, the stored keypair, and every peer on this tunnel. This adds a delete op to the current changeset draft — nothing is removed until the changeset applies."
      onSubmit={handleSubmit}
      submitLabel="Delete"
      disabledReason={!capsForNode(session, tunnel.node).netWrite ? disabledReason : undefined}
    >
      <p className="text-sm text-slate-600 dark:text-slate-400">Nothing is removed until the changeset applies.</p>
    </EditorDialog>
  );
}

// --- Add/edit peer -----------------------------------------------------------

interface PeerFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tunnel: WireGuardTunnel;
  existing?: WireGuardPeer;
}

function PeerFormDialog({ open, onOpenChange, tunnel, existing }: PeerFormDialogProps) {
  const { data: session } = useSession();
  const { findings, submit } = useEditorSubmit();
  const federationClusters = useFederationClustersQuery(open).data ?? [];

  const isCreate = !existing;
  const initial: WgPeerFormValues = existing
    ? {
        publicKey: existing.publicKey,
        endpoint: existing.endpoint ?? "",
        allowedIps: existing.allowedIps,
        presharedKey: "",
        keepaliveSec: existing.keepaliveSec ?? 0,
        clusterId: "",
      }
    : emptyWgPeerForm();

  const [publicKey, setPublicKey] = useState(initial.publicKey);
  const [endpoint, setEndpoint] = useState(initial.endpoint);
  const [allowedIpsRaw, setAllowedIpsRaw] = useState(initial.allowedIps.join(", "));
  const [presharedKey, setPresharedKey] = useState("");
  const [keepaliveSec, setKeepaliveSec] = useState(initial.keepaliveSec);
  const [clusterId, setClusterId] = useState(initial.clusterId);

  const form: WgPeerFormValues = useMemo(
    () => ({ publicKey: publicKey.trim(), endpoint: endpoint.trim(), allowedIps: parseAddressList(allowedIpsRaw), presharedKey, keepaliveSec, clusterId }),
    [publicKey, endpoint, allowedIpsRaw, presharedKey, keepaliveSec, clusterId],
  );

  const cap = capsForNode(session, tunnel.node);
  const target = `wg-peer:${tunnel.node}:${tunnel.id}/${form.publicKey || publicKey}`;
  const keyInvalid = !looksLikeWgKey(form.publicKey);

  function handleSubmit(): void {
    if (keyInvalid) return;
    const op = buildWgPeerAddOp(tunnel.node, tunnel.id, form);
    const label = `${form.publicKey.slice(0, 8)}… on ${tunnelTitle(tunnel)}`;
    submit([op], isCreate ? `Add WireGuard peer ${label}` : `Edit WireGuard peer ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? `Add peer to ${tunnelTitle(tunnel)}` : `Edit peer on ${tunnelTitle(tunnel)}`}
      description="The far side is always modeled as an external peer — vnprox never generates or applies against a key it doesn't own. Paste in the public key the other side already generated."
      onSubmit={handleSubmit}
      disabledReason={!cap.netWrite ? missingCapTooltip(session, tunnel.node, "netWrite") : keyInvalid ? "Enter a valid public key (44 characters)." : undefined}
      generalErrors={findings.general}
    >
      <Field label="Peer public key" help="Base64, 44 characters — the far side's own public key, never generated here.">
        <input className={inputClass} value={publicKey} disabled={!isCreate} onChange={(e) => { setPublicKey(e.target.value); }} placeholder="base64 public key" />
      </Field>
      <Field label="Endpoint (host:port)" help="Leave blank for a peer with no fixed address (it dials in; this side just accepts).">
        <input className={inputClass} value={endpoint} onChange={(e) => { setEndpoint(e.target.value); }} placeholder="203.0.113.10:51820" />
      </Field>
      <Field label="Allowed IPs" help="Comma-separated CIDRs this peer is allowed to route.">
        <input className={inputClass} value={allowedIpsRaw} onChange={(e) => { setAllowedIpsRaw(e.target.value); }} placeholder="10.10.0.2/32" />
      </Field>
      <Field label="Preshared key" help="Optional. Write-only — never redisplayed once staged; leave blank to keep the existing one unchanged.">
        <input className={inputClass} type="password" value={presharedKey} onChange={(e) => { setPresharedKey(e.target.value); }} autoComplete="off" />
      </Field>
      <Field label="Keepalive (seconds)" help="0 turns it off. Useful behind NAT/stateful firewalls.">
        <input type="number" className={inputClass} value={keepaliveSec || ""} onChange={(e) => { setKeepaliveSec(Number(e.target.value)); }} min={0} />
      </Field>
      <Field label="Federated cluster (optional)" help="Tag this peer as a specific attached cluster, so its reachability rolls into that cluster's own status.">
        <select className={inputClass} value={clusterId} disabled={federationClusters.length === 0} onChange={(e) => { setClusterId(e.target.value); }}>
          <option value="">{federationClusters.length === 0 ? "No attached clusters" : "Untagged"}</option>
          {federationClusters.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      </Field>
    </EditorDialog>
  );
}

function RemovePeerDialog({
  open,
  onOpenChange,
  tunnel,
  peer,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tunnel: WireGuardTunnel;
  peer: WireGuardPeer;
}) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps } = useDrawerActions();

  function handleSubmit(): void {
    void addOps([buildWgPeerRemoveOp(tunnel.node, tunnel.id, peer.publicKey)], `Remove WireGuard peer from ${tunnelTitle(tunnel)}`)
      .then(() => {
        onOpenChange(false);
        toast({ title: "Added to changeset" });
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Remove peer ${peer.publicKey.slice(0, 8)}…?`}
      description="This adds a remove op to the current changeset draft — nothing changes until the changeset applies."
      onSubmit={handleSubmit}
      submitLabel="Remove"
      disabledReason={!capsForNode(session, tunnel.node).netWrite ? missingCapTooltip(session, tunnel.node, "netWrite") : undefined}
    >
      <p className="text-sm text-slate-600 dark:text-slate-400">The remaining peers on this tunnel are unaffected.</p>
    </EditorDialog>
  );
}

// --- Public key / peer config viewer (read-only, no secret ever shown) -----

function KeyViewerDialog({ open, onOpenChange, tunnel }: { open: boolean; onOpenChange: (open: boolean) => void; tunnel: WireGuardTunnel }) {
  const { toast } = useToast();
  const pubkey = useWireGuardPubkeyQuery(tunnel.id, open);
  const peerConfig = useWireGuardPeerConfigQuery(tunnel.id, open);

  function copy(text: string | undefined, what: string): void {
    if (!text) return;
    void navigator.clipboard
      .writeText(text)
      .then(() => {
        toast({ title: `${what} copied` });
      })
      .catch(() => {
        toast({ title: `Could not copy ${what.toLowerCase()}`, variant: "error" });
      });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>{`${tunnelTitle(tunnel)} — public key & peer config`}</DialogTitle>
        <DialogDescription>
          Only this tunnel&apos;s derived public key and an exportable peer-config block — never the private key. Nothing here is fetched
          until this dialog is open.
        </DialogDescription>
        <div className="mt-3 space-y-3 text-sm">
          <Field label="Public key">
            <div className="flex gap-2">
              <input className={inputClass} readOnly value={pubkey.data ?? (pubkey.isLoading ? "Loading…" : "—")} />
              <Button variant="secondary" size="sm" disabled={!pubkey.data} onClick={() => { copy(pubkey.data, "Public key"); }}>
                Copy
              </Button>
            </div>
          </Field>
          <Field label="Exportable peer config" help="The wg-quick config block an external peer would install on its own side.">
            <div className="flex flex-col gap-2">
              <textarea className={`${inputClass} h-32 font-mono text-xs`} readOnly value={peerConfig.data ?? (peerConfig.isLoading ? "Loading…" : "—")} />
              <Button variant="secondary" size="sm" disabled={!peerConfig.data} onClick={() => { copy(peerConfig.data, "Peer config"); }}>
                Copy
              </Button>
            </div>
          </Field>
        </div>
        <div className="mt-4 flex justify-end">
          <Button variant="ghost" size="sm" onClick={() => { onOpenChange(false); }}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// --- Peer table --------------------------------------------------------------

function PeerRow({
  peer,
  gateDisabled,
  gateTitle,
  onEdit,
  onRemove,
}: {
  peer: WireGuardPeer;
  gateDisabled: boolean;
  gateTitle: string | undefined;
  onEdit: () => void;
  onRemove: () => void;
}) {
  return (
    <TableRow>
      <TableCell className="font-mono text-xs">{peer.publicKey.slice(0, 12)}…</TableCell>
      <TableCell>{peer.endpoint ?? <span className="text-slate-600 dark:text-slate-400">no fixed endpoint</span>}</TableCell>
      <TableCell>{peer.allowedIps.join(", ") || "—"}</TableCell>
      <TableCell>
        {peer.endpointDrifted && (
          <span className="mr-1 inline-flex items-center rounded-full border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-200">
            drift
          </span>
        )}
        {peer.lastHandshakeUnix ? formatAge(peer.lastHandshakeUnix * 1000) : "never"}
      </TableCell>
      <TableCell>
        {formatBytes(peer.rxBytes)} / {formatBytes(peer.txBytes)}
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-1.5">
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onEdit}>
            Edit
          </Button>
          <Button variant="secondary" size="sm" disabled={gateDisabled} title={gateTitle} onClick={onRemove}>
            Remove
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

// --- Tunnel row + page ---------------------------------------------------

function TunnelSection({ tunnel, tunnelsUnavailable }: { tunnel: WireGuardTunnel; tunnelsUnavailable: boolean }) {
  const { data: session } = useSession();
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [keyViewerOpen, setKeyViewerOpen] = useState(false);
  const [addPeerOpen, setAddPeerOpen] = useState(false);
  const [editingPeer, setEditingPeer] = useState<WireGuardPeer | undefined>(undefined);
  const [removingPeer, setRemovingPeer] = useState<WireGuardPeer | undefined>(undefined);

  const state = wgTunnelState(tunnel, tunnelsUnavailable);
  const canWrite = capsForNode(session, tunnel.node).netWrite;
  const gateTitle = canWrite ? undefined : missingCapTooltip(session, tunnel.node, "netWrite");

  return (
    <div className="rounded-md border border-slate-200 dark:border-slate-700">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-200 p-3 dark:border-slate-700">
        <div className="flex items-center gap-3">
          <StateBadge state={state} />
          <div>
            <div className="font-medium">{tunnelTitle(tunnel)}</div>
            <div className="text-xs text-slate-600 dark:text-slate-400">
              {tunnel.addresses.join(", ") || "no address"} · UDP {tunnel.listenPort} · {tunnel.peers.length} peer{tunnel.peers.length === 1 ? "" : "s"}
              {tunnel.carrier ? ` · via ${tunnel.carrier}` : ""}
            </div>
          </div>
        </div>
        <div className="flex flex-wrap gap-1.5">
          <Button variant="secondary" size="sm" onClick={() => { setKeyViewerOpen(true); }}>
            View key
          </Button>
          <Button variant="secondary" size="sm" disabled={!canWrite} title={gateTitle} onClick={() => { setAddPeerOpen(true); }}>
            + Peer
          </Button>
          <Button variant="secondary" size="sm" disabled={!canWrite} title={gateTitle} onClick={() => { setEditOpen(true); }}>
            Edit
          </Button>
          <Button variant="secondary" size="sm" disabled={!canWrite} title={gateTitle} onClick={() => { setDeleteOpen(true); }}>
            Delete
          </Button>
        </div>
      </div>
      {tunnel.peers.length === 0 ? (
        <p className="p-3 text-sm text-slate-600 dark:text-slate-400">No peers configured yet.</p>
      ) : (
        <Table aria-label={`Peers on ${tunnelTitle(tunnel)}`}>
          <TableHeader>
            <TableRow>
              <TableHead>Public key</TableHead>
              <TableHead>Endpoint</TableHead>
              <TableHead>Allowed IPs</TableHead>
              <TableHead>Last handshake</TableHead>
              <TableHead>Rx / Tx</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {tunnel.peers.map((p) => (
              <PeerRow
                key={p.publicKey}
                peer={p}
                gateDisabled={!canWrite}
                gateTitle={gateTitle}
                onEdit={() => { setEditingPeer(p); }}
                onRemove={() => { setRemovingPeer(p); }}
              />
            ))}
          </TableBody>
        </Table>
      )}

      {editOpen && <TunnelFormDialog open={editOpen} onOpenChange={setEditOpen} existing={tunnel} />}
      {deleteOpen && <DeleteTunnelDialog open={deleteOpen} onOpenChange={setDeleteOpen} tunnel={tunnel} />}
      {keyViewerOpen && <KeyViewerDialog open={keyViewerOpen} onOpenChange={setKeyViewerOpen} tunnel={tunnel} />}
      {addPeerOpen && <PeerFormDialog open={addPeerOpen} onOpenChange={setAddPeerOpen} tunnel={tunnel} />}
      {editingPeer && (
        <PeerFormDialog
          open={!!editingPeer}
          onOpenChange={(o) => { if (!o) setEditingPeer(undefined); }}
          tunnel={tunnel}
          existing={editingPeer}
        />
      )}
      {removingPeer && (
        <RemovePeerDialog
          open={!!removingPeer}
          onOpenChange={(o) => { if (!o) setRemovingPeer(undefined); }}
          tunnel={tunnel}
          peer={removingPeer}
        />
      )}
    </div>
  );
}

export function WireGuardPage() {
  const { data: session } = useSession();
  const { data: tunnels, isLoading, isError } = useWireGuardTunnelsQuery(true);
  const [createOpen, setCreateOpen] = useState(false);

  const canWriteAny = Object.values(session?.caps ?? {}).some((c) => c.netWrite);
  const gateTitle = canWriteAny ? undefined : missingCapTooltip(session, "", "netWrite");
  // "unknown" tracks the CURRENT read's own success/failure, not merely
  // "loading" — react-query keeps the last successful `tunnels` payload
  // around through a failed background refetch (status flips to "error"
  // but `data` is untouched), so a background failure after an earlier
  // success still has a list to render. Every row is then marked "unknown"
  // rather than silently kept at its last-known up/down verdict, which
  // would misreport a stale reading as current. Only the true "never
  // loaded anything" case (isError with no cached tunnels at all) falls
  // back to a page-level empty state below, since there is nothing to put
  // a row-level badge on.
  const tunnelsUnavailable = isError;

  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="flex items-center gap-1.5 text-lg font-semibold">
          WireGuard tunnels
          <HelpAnchor topic="wireguard-page" />
        </h1>
        <Button variant="primary" size="sm" disabled={!canWriteAny} title={gateTitle} onClick={() => { setCreateOpen(true); }}>
          + New tunnel
        </Button>
      </div>

      {isLoading ? (
        <p className="text-sm text-slate-600 dark:text-slate-400">Loading WireGuard tunnels…</p>
      ) : !tunnels ? (
        <EmptyState
          title="Could not load WireGuard tunnels"
          description="Check that vnproxd can reach this node's live state, then reload."
        />
      ) : tunnels.length === 0 ? (
        <EmptyState
          title="No WireGuard tunnels configured"
          description="A tunnel is a site-to-site (or road-warrior) WireGuard link this node manages. Create one to get started — federation's own Connect clusters wizard also creates tunnels here, so both surfaces show the same list."
        />
      ) : (
        <div className="flex flex-col gap-3">
          {isError && (
            <p role="alert" className="rounded border border-amber-300 bg-amber-50 p-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-200">
              Could not refresh live WireGuard state — showing the last known tunnel list. Every state below reads &quot;unknown&quot;
              until the read succeeds again; a tunnel marked unknown is not necessarily down.
            </p>
          )}
          {tunnels.map((t) => (
            <TunnelSection key={t.id} tunnel={t} tunnelsUnavailable={tunnelsUnavailable} />
          ))}
        </div>
      )}

      {createOpen && <TunnelFormDialog open={createOpen} onOpenChange={setCreateOpen} />}
    </div>
  );
}
