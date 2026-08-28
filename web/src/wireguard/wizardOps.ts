// SPDX-License-Identifier: Apache-2.0

// T-1402's "connect two clusters" wizard: turns the wizard's finished form
// values into the ONE changeset's worth of ops it stages — wg.tunnel.create
// + wg.peer.add (this node's own side) + fw.rule.create (the firewall
// opening exactly the tunnel needs), all existing op types from T-1401/T-502
// (docs/data-model.md's op-group table), never a hand-built shortcut and
// never a second mutation path (CLAUDE.md's change-engine invariant).
//
// Federation seam (see this file's and ConnectClustersWizard.tsx's shared
// doc comment): T-1201/T-1202 (federation core / global topology) are not
// in this repo. This card's original brief assumed a federated "pick a
// target cluster" step; without federation there is no second
// vnprox-managed cluster to fan an apply out to, and — even if there were —
// T-1401's key-custody design generates a tunnel's keypair on the owning
// node *at apply time*, not at stage time, so a same-changeset op can never
// reference a not-yet-applied peer's future public key. The only
// genuinely-one-changeset shape buildable on T-1401's shipped surface is:
// this node's own tunnel + an externally-modeled peer whose public key the
// operator already has (a real external WireGuard install, OR another
// vnprox cluster/node's own already-applied tunnel, whose public key was
// exported via that system's own GET /wireguard/tunnels/{id}/pubkey and
// pasted in here). T-1407 (tunnel-aware federation transport) is the
// follow-up that can automate that key exchange once federation exists;
// nothing here blocks on it.
//
// What T-1407 *did* add here is `peerClusterId`: the operator can name which
// attached federation cluster the far side is, and that annotation rides the
// same wg.peer.add op — still one changeset, still no second mutation path.
// Key exchange remains manual; only the "which cluster is this" linkage is
// automated.
import type { Op, WgPeerAddParams, WgTunnelCreateParams } from "../api/types";
import { buildFwRuleCreateOp, type RuleFormValues } from "../firewall/opBuilders";

export interface ConnectClustersParams {
  /** This cluster's node the tunnel is created on. */
  sourceNode: string;
  /** On-node WireGuard interface name, e.g. "wg0". */
  ifName: string;
  listenPort: number;
  /** Underlying interface the tunnel's endpoint rides on, e.g. "vmbr0" — ""
   * means unset (no mgmt-path carrier declared). */
  carrier: string;
  /** This tunnel's own address, CIDR form, e.g. "10.10.0.1/24". */
  localAddress: string;
  /** 0 means "use the interface default" (WgTunnelCreateParams.mtu omitted). */
  mtu: number;
  /** The far side's WireGuard public key — always operator-supplied (see
   * this file's federation-seam doc comment above): vnprox never generates
   * a key for a peer it doesn't own. */
  peerPublicKey: string;
  /** "" is valid — a peer with no fixed address (road-warrior-style; the
   * far side dials in, this side just accepts). */
  peerEndpoint: string;
  peerAllowedIps: string[];
  /** "" means no PSK. */
  presharedKey: string;
  /** 0 means unset (no persistent keepalive). */
  keepaliveSec: number;
  /** Optional: the attached federation cluster this peer *is*
   * (wireguard_peers.cluster_id). "" means "untagged" — an ordinary external
   * peer, the shape this wizard staged before the linkage existed.
   *
   * Tagging is what links the federated cluster to this tunnel: the daemon
   * derives clusters.wg_tunnel_id from the annotation when no explicit
   * override is stored (internal/federation.TunnelLinker), so a tunnel-down
   * peer collapses into the one tunnel_down_peer_unreachable finding instead
   * of three per-surface "unreachable" flags. Deliberately carried on the
   * ordinary wg.peer.add op rather than a side-channel PUT to
   * /federation/clusters: the linkage lands when — and only when — the
   * changeset that creates the tunnel is actually applied. */
  peerClusterId: string;
  /** CIDR/IP the firewall rule allows the tunnel's UDP port from — "" means
   * "any" (0.0.0.0/0), appropriate for a peer with no fixed endpoint. */
  fwSourceCidr: string;
}

/** wg.tunnel.* op target, per internal/change/params_wg.go's documented
 * Ref convention: "wg-tunnel:<node>:<tunnelId>". */
export function wgTunnelTarget(node: string, tunnelId: string): string {
  return `wg-tunnel:${node}:${tunnelId}`;
}

/** wg.peer.* op target: "wg-peer:<node>:<tunnelId>/<publicKey>" — the
 * on-node gateway's splitWgPeerTarget (cmd/vnproxd/wireguard.go) splits on
 * the first '/', so the tunnel id itself must never contain one (this
 * wizard's generated ids — see ConnectClustersWizard.tsx — never do). */
export function wgPeerTarget(node: string, tunnelId: string, publicKey: string): string {
  return `wg-peer:${node}:${tunnelId}/${publicKey}`;
}

/** A node's own firewall ruleset target (web/src/firewall/refs.ts's
 * "fw-ruleset:<node>:node" node-scope convention). */
export function fwNodeRulesetTarget(node: string): string {
  return `fw-ruleset:${node}:node`;
}

/** Builds the exact ops this wizard stages in one `POST /changesets` call.
 * `tunnelId` is caller-supplied, not generated here, so the wizard's live
 * preview graph (previewGraph.ts) and this op list are always built from
 * the identical id — "the preview pane matches the changeset it actually
 * submits" (T-1402 AC2) holds by construction. `fwPos` is the position to
 * insert the new firewall rule at — callers pass the target ruleset's
 * current rule count (append at the end, never displacing an existing
 * rule). */
export function buildConnectClustersOps(p: ConnectClustersParams, tunnelId: string, fwPos: number): Op[] {
  const tunnelParams: WgTunnelCreateParams = {
    ifName: p.ifName,
    carrier: p.carrier || undefined,
    addresses: p.localAddress ? [p.localAddress] : undefined,
    listenPort: p.listenPort || undefined,
    mtu: p.mtu || undefined,
  };
  const peerParams: WgPeerAddParams = {
    publicKey: p.peerPublicKey,
    endpoint: p.peerEndpoint || undefined,
    presharedKey: p.presharedKey || undefined,
    allowedIps: p.peerAllowedIps.length > 0 ? p.peerAllowedIps : undefined,
    keepaliveSec: p.keepaliveSec || undefined,
    clusterId: p.peerClusterId || undefined,
    // Always true: see this file's federation-seam doc comment — the far
    // side is never vnprox's own to apply against, whether it's a genuinely
    // external install or another vnprox cluster/node reached via a
    // manually-exchanged public key.
    external: true,
  };
  const fwForm: RuleFormValues = {
    direction: "in",
    action: "ACCEPT",
    proto: "udp",
    source: p.fwSourceCidr || "",
    dest: "",
    sport: "",
    dport: p.listenPort ? String(p.listenPort) : "",
    iface: "",
    macro: "",
    log: "",
    comment: `WireGuard tunnel ${p.ifName} (connect-clusters wizard)`,
    enabled: true,
  };

  return [
    { op: "wg.tunnel.create", target: wgTunnelTarget(p.sourceNode, tunnelId), params: tunnelParams },
    { op: "wg.peer.add", target: wgPeerTarget(p.sourceNode, tunnelId, p.peerPublicKey), params: peerParams },
    buildFwRuleCreateOp(fwNodeRulesetTarget(p.sourceNode), fwPos, fwForm),
  ];
}
