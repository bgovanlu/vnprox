// T-1402's "connect two clusters" wizard: builds a site-to-site WireGuard
// tunnel plus the firewall rule it needs, staged as ONE reviewable
// changeset — never a half-open state where the tunnel exists but its
// firewall doesn't (this task's card, verbatim). Built on the exact same
// step-engine/preview-pane infrastructure every SDN zone wizard uses
// (WizardShell + WizardPreviewPane, sdn/wizards/) and the same single
// mutation path every wizard in this codebase shares
// (useDrawerActions().addOps -> POST/PUT /changesets, never a dedicated
// apply route of its own — changesets/useDrawerActions.ts's own doc
// comment: "the one entry point every op-producing feature ... calls to
// land an op in the drawer").
//
// Federation seam: T-1201 (federation core) / T-1202 (global topology) are
// not in this repo. This wizard therefore connects this cluster's own node
// to an endpoint modeled as an external WireGuard peer — either a genuinely
// external system, or another vnprox-managed cluster/node whose own public
// key the operator already exported (via that system's GET
// /wireguard/tunnels/{id}/pubkey) and pastes in on the "Other side" step.
// See wizardOps.ts's doc comment for exactly why a same-changeset
// automatic key exchange between two vnprox-managed sides isn't buildable
// on T-1401's shipped surface (keys are generated on-node at apply time,
// not stage time) — T-1407 (tunnel-aware federation transport) is the
// follow-up that can close this gap once federation exists.
//
// Nothing here ever calls any route except the single POST/PUT
// /changesets useDrawerActions.addOps performs at the final step — exactly
// like WizardShell's own doc comment guarantees for every wizard built on
// it ("nothing was ever sent to the server until the user reaches the last
// step and clicks Create draft"), so cancelling mid-flow (or simply
// closing the dialog) can never leave a half-open tunnel-without-firewall
// state: there is nothing to leave, because nothing was created yet.
import { useMemo, useState } from "react";
import { useSession } from "../api/useSession";
import type { Op } from "../api/types";
import { useToast } from "../components/Toast";
import { capsForNode, missingCapTooltip } from "../changesets/capabilities";
import { Field, inputClass } from "../changesets/editors/EditorDialog";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { useNodeRulesetQuery } from "../firewall/queries";
import { WizardPreviewPane } from "../sdn/wizards/WizardPreviewPane";
import { WizardShell, type WizardStep } from "../sdn/wizards/WizardShell";
import { useClusterNodes } from "../sdn/wizards/useClusterNodes";
import { useFederationClustersQuery } from "../topology/federation/federationQueries";
import { buildConnectClustersPreview } from "./previewGraph";
import { wgWizardStrings as S } from "./strings";
import { buildConnectClustersOps, type ConnectClustersParams } from "./wizardOps";

export interface ConnectClustersWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Preselects the source node (e.g. launched from a specific node's
   * context menu) — the step remains editable either way. */
  initialSourceNode?: string;
}

const DEFAULT_PARAMS: Omit<ConnectClustersParams, "sourceNode"> = {
  ifName: "wg0",
  listenPort: 51820,
  carrier: "",
  localAddress: "10.10.0.1/24",
  mtu: 0,
  peerPublicKey: "",
  peerEndpoint: "",
  peerAllowedIps: [],
  presharedKey: "",
  keepaliveSec: 0,
  peerClusterId: "",
  fwSourceCidr: "",
};

/** A WireGuard public key: base64, 32 raw bytes -> 44 characters, always
 * padded with a trailing '='. Loose on purpose — this is a UX nudge, not a
 * cryptographic validation (the change engine's own validate step is the
 * real gate). */
function looksLikeWgKey(key: string): boolean {
  return key.trim().length === 44 && key.trim().endsWith("=");
}

function parseAllowedIps(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function ConnectClustersWizard({ open, onOpenChange, initialSourceNode }: ConnectClustersWizardProps) {
  const { toast } = useToast();
  const { data: session } = useSession();
  const { addOps } = useDrawerActions();
  const clusterNodes = useClusterNodes();

  const [sourceNode, setSourceNode] = useState(initialSourceNode ?? "");
  const [ifName, setIfName] = useState(DEFAULT_PARAMS.ifName);
  const [listenPort, setListenPort] = useState(DEFAULT_PARAMS.listenPort);
  const [carrier, setCarrier] = useState(DEFAULT_PARAMS.carrier);
  const [localAddress, setLocalAddress] = useState(DEFAULT_PARAMS.localAddress);
  const [mtu, setMtu] = useState(DEFAULT_PARAMS.mtu);
  const [peerPublicKey, setPeerPublicKey] = useState(DEFAULT_PARAMS.peerPublicKey);
  const [peerEndpoint, setPeerEndpoint] = useState(DEFAULT_PARAMS.peerEndpoint);
  const [peerAllowedIpsRaw, setPeerAllowedIpsRaw] = useState("");
  const [presharedKey, setPresharedKey] = useState(DEFAULT_PARAMS.presharedKey);
  const [keepaliveSec, setKeepaliveSec] = useState(DEFAULT_PARAMS.keepaliveSec);
  const [peerClusterId, setPeerClusterId] = useState(DEFAULT_PARAMS.peerClusterId);
  const [fwSourceCidr, setFwSourceCidr] = useState(DEFAULT_PARAMS.fwSourceCidr);
  const [finishing, setFinishing] = useState(false);

  // The attached-cluster registry, for the optional "the far side is this
  // federated cluster" tagging on the peer step. Degrades to an empty list
  // (no federation wired / no netRead), which renders the select disabled
  // rather than hiding it — the tagging is optional either way.
  const federationClusters = useFederationClustersQuery(open).data ?? [];

  // Generated once per wizard *instance* (remounted fresh whenever the
  // dialog reopens, since ConnectClustersWizardHost keys it — see that
  // file), then threaded unchanged into both the preview graph and the
  // final op list. This is the entire mechanism behind AC2 ("the preview
  // pane matches the changeset it actually submits") — both are pure
  // functions of the same (params, tunnelId) pair, so they cannot drift.
  const [tunnelId] = useState(() => crypto.randomUUID());

  const params: ConnectClustersParams = useMemo(
    () => ({
      sourceNode,
      ifName,
      listenPort,
      carrier,
      localAddress,
      mtu,
      peerPublicKey: peerPublicKey.trim(),
      peerEndpoint: peerEndpoint.trim(),
      peerAllowedIps: parseAllowedIps(peerAllowedIpsRaw),
      presharedKey,
      keepaliveSec,
      peerClusterId,
      fwSourceCidr,
    }),
    [sourceNode, ifName, listenPort, carrier, localAddress, mtu, peerPublicKey, peerEndpoint, peerAllowedIpsRaw, presharedKey, keepaliveSec, peerClusterId, fwSourceCidr],
  );

  const graph = useMemo(() => buildConnectClustersPreview(params, tunnelId), [params, tunnelId]);

  // The new firewall rule appends after every rule this ruleset already
  // has — never displacing an existing rule's position (wizardOps.ts's own
  // doc comment). Not fetched until the source node is picked. `fwPos` is
  // `undefined` until the count is actually known: defaulting to 0 would
  // top-insert and displace existing rules under a slow/failed fetch, so
  // the Review step stays invalid until the ruleset resolves (review-T-1402).
  const rulesetQuery = useNodeRulesetQuery(sourceNode || undefined);
  const fwPos = rulesetQuery.data?.rules.length;

  const cap = capsForNode(session, sourceNode);
  const capDenied = sourceNode !== "" && !cap.netWrite;
  const capReason = sourceNode ? missingCapTooltip(session, sourceNode, "netWrite") : undefined;

  function handleFinish(): void {
    if (fwPos === undefined) {
      toast({ title: "Loading firewall rules…", description: "One moment — reading the node's ruleset so the new rule appends cleanly.", variant: "error" });
      return;
    }
    setFinishing(true);
    const ops: Op[] = buildConnectClustersOps(params, tunnelId, fwPos);
    void addOps(ops, `WireGuard tunnel: ${sourceNode} → ${params.peerEndpoint || "peer"}`)
      .then(() => {
        toast({ title: "Added to changeset", description: `${String(ops.length)} steps drafted — review before applying.` });
        onOpenChange(false);
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      })
      .finally(() => {
        setFinishing(false);
      });
  }

  const steps: WizardStep[] = [
    {
      id: "source",
      title: S.steps.source,
      isValid: sourceNode.trim().length > 0 && ifName.trim().length > 0 && listenPort > 0 && listenPort < 65536 && localAddress.trim().length > 0,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.intro}</p>
          <Field label="Source node" help={S.sourceHelp.node}>
            <select
              className={inputClass}
              value={sourceNode}
              onChange={(e) => {
                setSourceNode(e.target.value);
              }}
            >
              <option value="">Pick a node…</option>
              {clusterNodes.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Interface name" help={S.sourceHelp.ifName}>
            <input className={inputClass} value={ifName} onChange={(e) => { setIfName(e.target.value); }} placeholder="wg0" />
          </Field>
          <Field label="Listen port" help={S.sourceHelp.listenPort}>
            <input
              type="number"
              className={inputClass}
              value={listenPort || ""}
              onChange={(e) => { setListenPort(Number(e.target.value)); }}
              min={1}
              max={65535}
            />
          </Field>
          <Field label="Tunnel address (CIDR)" help={S.sourceHelp.localAddress}>
            <input className={inputClass} value={localAddress} onChange={(e) => { setLocalAddress(e.target.value); }} placeholder="10.10.0.1/24" />
          </Field>
          <Field label="Carrier interface" help={S.sourceHelp.carrier}>
            <input className={inputClass} value={carrier} onChange={(e) => { setCarrier(e.target.value); }} placeholder="vmbr0" />
          </Field>
          <Field label="MTU" help={S.sourceHelp.mtu}>
            <input type="number" className={inputClass} value={mtu || ""} onChange={(e) => { setMtu(Number(e.target.value)); }} min={0} />
          </Field>
        </div>
      ),
    },
    {
      id: "peer",
      title: S.steps.peer,
      isValid: looksLikeWgKey(params.peerPublicKey) && params.peerAllowedIps.length > 0,
      invalidReason: !looksLikeWgKey(params.peerPublicKey)
        ? "Enter the far side's public key (44 characters)."
        : params.peerAllowedIps.length === 0
          ? "Enter at least one allowed address/range."
          : undefined,
      content: (
        <div className="space-y-3">
          <p className="rounded border border-slate-200 bg-slate-50 p-2 text-xs text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">
            {S.federationNote}
          </p>
          <Field label="Peer public key" help={S.peerHelp.publicKey}>
            <input className={inputClass} value={peerPublicKey} onChange={(e) => { setPeerPublicKey(e.target.value); }} placeholder="base64 public key" />
          </Field>
          <Field label="Peer endpoint (host:port)" help={S.peerHelp.endpoint}>
            <input className={inputClass} value={peerEndpoint} onChange={(e) => { setPeerEndpoint(e.target.value); }} placeholder="203.0.113.10:51820" />
          </Field>
          <Field label="Allowed IPs" help={S.peerHelp.allowedIps}>
            <input className={inputClass} value={peerAllowedIpsRaw} onChange={(e) => { setPeerAllowedIpsRaw(e.target.value); }} placeholder="10.10.0.2/32" />
          </Field>
          <Field label="Preshared key" help={S.peerHelp.presharedKey}>
            <input className={inputClass} value={presharedKey} onChange={(e) => { setPresharedKey(e.target.value); }} />
          </Field>
          <Field label="Keepalive (seconds)" help={S.peerHelp.keepalive}>
            <input type="number" className={inputClass} value={keepaliveSec || ""} onChange={(e) => { setKeepaliveSec(Number(e.target.value)); }} min={0} />
          </Field>
          <Field label="Federated cluster" help={S.peerHelp.federatedCluster}>
            <select
              className={inputClass}
              value={peerClusterId}
              disabled={federationClusters.length === 0}
              onChange={(e) => { setPeerClusterId(e.target.value); }}
            >
              <option value="">{federationClusters.length === 0 ? S.federatedClusterEmpty : S.federatedClusterNone}</option>
              {federationClusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                  {c.wgTunnelId ? " — already linked to a tunnel" : ""}
                </option>
              ))}
            </select>
          </Field>
        </div>
      ),
    },
    {
      id: "firewall",
      title: S.steps.firewall,
      isValid: true,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">
            This will also add a firewall rule on {sourceNode || "the source node"} allowing UDP {listenPort || "?"} in — the tunnel doesn&apos;t
            work without it, so it is part of the same changeset as the tunnel itself, never a separate one.
          </p>
          <Field label="Allow from (optional)" help={S.firewallHelp.source}>
            <input className={inputClass} value={fwSourceCidr} onChange={(e) => { setFwSourceCidr(e.target.value); }} placeholder="203.0.113.10/32" />
          </Field>
        </div>
      ),
    },
    {
      id: "review",
      title: S.steps.review,
      isValid: !capDenied && fwPos !== undefined,
      invalidReason: capReason,
      content: (
        <div className="space-y-2 text-slate-600 dark:text-slate-300">
          <p>This will draft:</p>
          <ul className="list-inside list-disc space-y-1">
            <li>
              WireGuard tunnel &quot;{ifName}&quot; on {sourceNode || "?"}, listening on UDP {listenPort}
            </li>
            <li>
              Peer {peerPublicKey ? `${peerPublicKey.slice(0, 8)}…` : "?"}
              {peerEndpoint ? ` at ${peerEndpoint}` : " (no fixed endpoint)"}, allowed {params.peerAllowedIps.join(", ") || "none"}
            </li>
            <li>Firewall rule on {sourceNode || "?"} allowing UDP {listenPort} in from {fwSourceCidr || "anywhere"}</li>
            {peerClusterId ? (
              <li>
                Tags that peer as federated cluster &quot;{federationClusters.find((c) => c.id === peerClusterId)?.name ?? peerClusterId}&quot; — once
                applied, that cluster counts as reachable over this tunnel
              </li>
            ) : null}
          </ul>
        </div>
      ),
    },
  ];

  return (
    <WizardShell
      open={open}
      onOpenChange={onOpenChange}
      title={S.title}
      helpTopic="wireguard-connect-clusters"
      intro={S.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
