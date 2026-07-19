// WireGuard read-view API calls (docs/api.md's WireGuard section, T-1401;
// internal/api/wireguard.go's GET /wireguard/tunnels et al.). Every route
// here is netRead-gated and read-only — WireGuard is mutated exclusively
// through the wg.* changeset op family (see wizardOps.ts), never a
// dedicated write route.
import { apiFetch } from "./client";
import type { WireGuardTunnel, WireGuardTunnelsResponse } from "./types";

/** GET /wireguard/tunnels — every tunnel this node can see, app-owned
 * config merged with live status (T-1401's running-vs-config-truth
 * pattern, mirroring GET /sdn). Node-local only today (no peer fan-out —
 * T-1401's documented single-node scope). */
export function fetchWireGuardTunnels(): Promise<WireGuardTunnel[]> {
  return apiFetch<WireGuardTunnelsResponse>("/wireguard/tunnels").then((r) => r.items);
}

/** GET /wireguard/tunnels/{id}/pubkey — the tunnel's derived public key
 * only; no route or op can retrieve the private key. */
export function fetchWireGuardPubkey(id: string): Promise<string> {
  return apiFetch<{ id: string; publicKey: string }>(`/wireguard/tunnels/${encodeURIComponent(id)}/pubkey`).then(
    (r) => r.publicKey,
  );
}

/** GET /wireguard/tunnels/{id}/peer-config — the exportable wg-quick
 * config an external peer would install on its own side. */
export function fetchWireGuardPeerConfig(id: string): Promise<string> {
  return apiFetch<{ id: string; peerConfig: string }>(
    `/wireguard/tunnels/${encodeURIComponent(id)}/peer-config`,
  ).then((r) => r.peerConfig);
}
