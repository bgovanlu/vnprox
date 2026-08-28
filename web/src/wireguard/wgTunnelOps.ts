// SPDX-License-Identifier: Apache-2.0

// T-4015: op builders for the general (non-federation-scoped) WireGuard
// management surface — the same wg.* op family wizardOps.ts already builds
// for the "connect clusters" wizard, reused here rather than duplicated:
// wgTunnelTarget/wgPeerTarget are imported from wizardOps.ts, not
// redefined, so the two surfaces can never disagree about a target Ref's
// string form. Every op-drafting function here is pure — (form values) ->
// Op — and stages nothing itself; callers pass the result to
// useDrawerActions()/useEditorSubmit() exactly like every other editor in
// this codebase (CLAUDE.md's "never apply outside the change engine").
//
// wg.peer.add is an upsert keyed by (tunnelId, publicKey) —
// internal/store/wireguard.go's AddPeer: "inserts or replaces" — so editing
// an existing peer's endpoint/allowedIps/keepalive/PSK is the identical op
// as adding one, re-submitted with the same public key. There is no
// wg.peer.update op in internal/change/op.go; this module does not invent
// one.
import type { Op, WgPeerAddParams, WgTunnelCreateParams, WgTunnelUpdateParams } from "../api/types";
import { wgPeerTarget, wgTunnelTarget } from "./wizardOps";

export interface WgTunnelFormValues {
  ifName: string;
  /** "" means unset (no mgmt-path carrier declared). */
  carrier: string;
  addresses: string[];
  /** 0 means "let the interface pick" (WgTunnelCreateParams.listenPort omitted). */
  listenPort: number;
  /** 0 means "use the interface default" (WgTunnelCreateParams.mtu omitted). */
  mtu: number;
}

export function emptyWgTunnelForm(): WgTunnelFormValues {
  return { ifName: "", carrier: "", addresses: [], listenPort: 51820, mtu: 0 };
}

/** wg.tunnel.create: `node` is the owning node, `tunnelId` is caller-
 * generated (crypto.randomUUID(), the same convention ConnectClustersWizard
 * uses) since the create op itself names the tunnel's id in its target Ref
 * — there is no server-assigned id to wait for. */
export function buildWgTunnelCreateOp(node: string, tunnelId: string, form: WgTunnelFormValues): Op {
  const params: WgTunnelCreateParams = {
    ifName: form.ifName,
    carrier: form.carrier || undefined,
    addresses: form.addresses.length > 0 ? form.addresses : undefined,
    listenPort: form.listenPort || undefined,
    mtu: form.mtu || undefined,
  };
  return { op: "wg.tunnel.create", target: wgTunnelTarget(node, tunnelId), params };
}

/** wg.tunnel.update: only fields that actually changed from `initial` are
 * set — WgTunnelUpdateParams' pointer fields mean "omitted = leave
 * unchanged" on the wire (mirrors firewall/opBuilders.ts's
 * buildFwRuleUpdateOp diff-against-initial convention). No key/ifName field
 * — key rotation and interface renames are delete+recreate, matching
 * internal/change/params_wg.go's documented design. */
export function buildWgTunnelUpdateOp(
  node: string,
  tunnelId: string,
  initial: WgTunnelFormValues,
  form: WgTunnelFormValues,
): Op {
  const params: WgTunnelUpdateParams = {};
  if (form.listenPort !== initial.listenPort) params.listenPort = form.listenPort;
  if (form.mtu !== initial.mtu) params.mtu = form.mtu;
  if (form.carrier !== initial.carrier) params.carrier = form.carrier;
  if (JSON.stringify(form.addresses) !== JSON.stringify(initial.addresses)) params.addresses = form.addresses;
  return { op: "wg.tunnel.update", target: wgTunnelTarget(node, tunnelId), params };
}

/** wg.tunnel.delete: no params. Deleting a tunnel removes its on-node
 * interface/config, its store row (including the sealed private key), and
 * every peer — T-1401 AC6, unchanged by this general-purpose surface. */
export function buildWgTunnelDeleteOp(node: string, tunnelId: string): Op {
  return { op: "wg.tunnel.delete", target: wgTunnelTarget(node, tunnelId), params: {} };
}

export interface WgPeerFormValues {
  publicKey: string;
  /** "" is valid — a peer with no fixed address (road-warrior-style). */
  endpoint: string;
  allowedIps: string[];
  /** "" means no PSK. Write-only: sealed at stage time, never round-tripped
   * back to this form on read (WgPeerAddParams.presharedKey's own doc
   * comment in api/types.ts). */
  presharedKey: string;
  /** 0 means unset (no persistent keepalive). */
  keepaliveSec: number;
  /** Optional federation-cluster tag — "" means untagged, an ordinary
   * external peer. Carried through unchanged for callers that want it (the
   * general surface exposes it too, since tagging is valid outside the
   * wizard flow as well), but never required. */
  clusterId: string;
}

export function emptyWgPeerForm(): WgPeerFormValues {
  return { publicKey: "", endpoint: "", allowedIps: [], presharedKey: "", keepaliveSec: 0, clusterId: "" };
}

/** wg.peer.add. `external` is always true — see this file's and
 * wizardOps.ts's shared doc comment: the far side is never vnprox's own to
 * apply against, whether a genuinely external WireGuard install or another
 * vnprox-managed node reached via a manually-exchanged public key (T-1401's
 * key-custody design generates a tunnel's keypair on the owning node at
 * apply time, not stage time, so no same-changeset key exchange is
 * buildable). Re-submitting with the same publicKey edits the peer in
 * place (AddPeer is an upsert) rather than creating a duplicate. */
export function buildWgPeerAddOp(node: string, tunnelId: string, form: WgPeerFormValues): Op {
  const params: WgPeerAddParams = {
    publicKey: form.publicKey,
    endpoint: form.endpoint || undefined,
    presharedKey: form.presharedKey || undefined,
    allowedIps: form.allowedIps.length > 0 ? form.allowedIps : undefined,
    keepaliveSec: form.keepaliveSec || undefined,
    clusterId: form.clusterId || undefined,
    external: true,
  };
  return { op: "wg.peer.add", target: wgPeerTarget(node, tunnelId, form.publicKey), params };
}

/** wg.peer.remove. */
export function buildWgPeerRemoveOp(node: string, tunnelId: string, publicKey: string): Op {
  return { op: "wg.peer.remove", target: wgPeerTarget(node, tunnelId, publicKey), params: { publicKey } };
}

/** A WireGuard public key: base64, 32 raw bytes -> 44 characters, always
 * padded with a trailing '='. A UX nudge, not cryptographic validation —
 * the change engine's own validate step is the real gate (mirrors
 * ConnectClustersWizard.tsx's identical `looksLikeWgKey`). */
export function looksLikeWgKey(key: string): boolean {
  return key.trim().length === 44 && key.trim().endsWith("=");
}

/** Parses a comma-separated address/CIDR list, mirroring
 * ConnectClustersWizard.tsx's identical `parseAllowedIps` helper. */
export function parseAddressList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}
