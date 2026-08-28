// SPDX-License-Identifier: Apache-2.0

// T-1502: turns T-1501's GET /k8s/{clusterId}/overlay into the map's
// "Kubernetes" layer overlay — synthetic {nodes, edges} merged into the real
// topology canvas via TopologyPage's existing extraNodes/extraEdges seam
// (the same injection point T-1402's WireGuard layer and T-1003's expanded
// guest-group pills already use), a pure `K8sOverlay -> {nodes, edges}`
// transform so it is directly Vitest-able without mounting React Flow —
// mirrors web/src/wireguard/wgTunnelEdges.ts's identical "pure compute"
// shape.
//
// Three synthetic node kinds:
//   - k8s-pod-cidr — one per PodCIDR entry (internal/k8s.Overlay.PodCIDRs):
//     the "pod/service CIDR region" this task's card names, one per k8s
//     node's advertised pod-network block.
//   - k8s-pod — one per pod (internal/k8s.Overlay.Pods) — internal/k8s/
//     overlay.go's own doc comment: "PodSummary is one pod, carried for
//     T-1502's pod-drilldown selection (pod -> node-guest -> bridge ->
//     bond)" — this is what a user clicks to open PodDrilldown.tsx.
//   - k8s-service — one per service with a ClusterIP (internal/k8s.Overlay.
//     Services) — never a guessed "service CIDR" (internal/k8s/overlay.go's
//     own doc comment), so this renders every real service instead.
//
// Synthetic node ids reuse internal/k8s/resolver.go's own ref-string
// format verbatim (ServiceRef/PodRef/PodSubnetRef's "k8s-svc:"/"k8s-pod:"/
// "k8s-podnet:" prefixes) — the same opaque, stable, human-legible string
// the backend's flow resolver already produces, so a synthetic node id and
// a flow-attribution ref are always the identical string for the same
// entity (see k8sFlowAttribution.ts). This also gives every synthetic id a
// prefix that can never collide with a real inventory.Ref string (none of
// inventory's closed Kind set starts with "k8s-").
//
// A pod/service/pod-cidr region only ever anchors into the correlated PVE
// guest's own node column (TopologyNode.nodeGroup) when its owning k8s node
// is matched (NodeCorrelation.matched) — an unmatched k8s node's entities
// render in the cluster-spanning band ("") rather than guessing a column,
// the same "never guessed" contract every other part of this package
// follows.
import type { EntityStatus, K8sOverlay, K8sPodSummary, TopologyEdge, TopologyNode } from "../../api/types";

export const K8S_POD_CIDR_KIND = "k8s-pod-cidr";
export const K8S_POD_KIND = "k8s-pod";
export const K8S_SERVICE_KIND = "k8s-service";

const K8S_PODNET_PREFIX = "k8s-podnet:";
const K8S_POD_PREFIX = "k8s-pod:";
const K8S_SVC_PREFIX = "k8s-svc:";

/** Mirrors internal/k8s/resolver.go's PodSubnetRef exactly. */
export function k8sPodSubnetId(clusterId: string, node: string): string {
  return `${K8S_PODNET_PREFIX}${clusterId}/${node}`;
}

/** Mirrors internal/k8s/resolver.go's PodRef exactly. */
export function k8sPodId(clusterId: string, namespace: string, name: string): string {
  return `${K8S_POD_PREFIX}${clusterId}/${namespace}/${name}`;
}

/** Mirrors internal/k8s/resolver.go's ServiceRef exactly. */
export function k8sServiceId(clusterId: string, namespace: string, name: string): string {
  return `${K8S_SVC_PREFIX}${clusterId}/${namespace}/${name}`;
}

/** True for any synthetic id this overlay produces — never a real
 * inventory Ref, so callers (TopologyPage's click routing, queries.ts's
 * inventory-detail guard) can exclude it from an inventory lookup. */
export function isK8sSyntheticId(id: string): boolean {
  return id.startsWith(K8S_PODNET_PREFIX) || id.startsWith(K8S_POD_PREFIX) || id.startsWith(K8S_SVC_PREFIX);
}

export type K8sSelection =
  | { kind: "pod-cidr"; clusterId: string; node: string }
  | { kind: "pod"; clusterId: string; namespace: string; name: string }
  | { kind: "service"; clusterId: string; namespace: string; name: string };

/** Parses one of this module's own synthetic ids back into a selection
 * (the inverse of k8sPodSubnetId/k8sPodId/k8sServiceId) — undefined for
 * anything not produced by this module. */
export function parseK8sSyntheticId(id: string): K8sSelection | undefined {
  if (id.startsWith(K8S_PODNET_PREFIX)) {
    const rest = id.slice(K8S_PODNET_PREFIX.length);
    const slash = rest.indexOf("/");
    if (slash === -1) return undefined;
    const clusterId = rest.slice(0, slash);
    const node = rest.slice(slash + 1);
    if (!clusterId || !node) return undefined;
    return { kind: "pod-cidr", clusterId, node };
  }
  if (id.startsWith(K8S_POD_PREFIX)) {
    const parsed = parseNamespacedId(id.slice(K8S_POD_PREFIX.length));
    return parsed && { kind: "pod", ...parsed };
  }
  if (id.startsWith(K8S_SVC_PREFIX)) {
    const parsed = parseNamespacedId(id.slice(K8S_SVC_PREFIX.length));
    return parsed && { kind: "service", ...parsed };
  }
  return undefined;
}

function parseNamespacedId(rest: string): { clusterId: string; namespace: string; name: string } | undefined {
  const parts = rest.split("/");
  if (parts.length < 3) return undefined;
  const [clusterId, namespace, ...nameParts] = parts;
  const name = nameParts.join("/");
  if (!clusterId || !namespace || !name) return undefined;
  return { clusterId, namespace, name };
}

/** Extracts the PVE node name a correlated guest ref belongs to
 * ("guest:<node>:<vmid>" -> "<node>"), mirroring inventory.Ref.String()'s
 * "kind:node:id" layout (docs/data-model.md §1) as an opaque prefix split
 * only — never re-derived structurally. Returns undefined for a ref that
 * doesn't parse as at least two ':'-separated segments. */
export function pveNodeFromGuestRef(ref: string | undefined): string | undefined {
  if (!ref) return undefined;
  const parts = ref.split(":");
  if (parts.length < 2) return undefined;
  const node = parts[1];
  return node === "" || node === undefined ? undefined : node;
}

function podStatus(phase: string | undefined): EntityStatus {
  switch (phase) {
    case "Running":
    case "Succeeded":
      return "ok";
    case "Pending":
      return "degraded";
    case "Failed":
      return "down";
    default:
      return "unknown";
  }
}

export interface K8sClusterOverlay {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

/** Builds the Kubernetes layer's overlay nodes/edges for one cluster's
 * current Overlay poll. Pure — no I/O, no clock read (mirrors
 * computeWgTunnelOverlay's shape) — so a fixture Overlay is enough to
 * exercise every branch in a Vitest test. */
export function computeK8sOverlay(overlay: K8sOverlay): K8sClusterOverlay {
  const nodes: TopologyNode[] = [];
  const edges: TopologyEdge[] = [];

  const correlationByK8sNode = new Map(overlay.nodes.map((c) => [c.k8sNode, c]));
  const nodePortByService = new Map(
    (overlay.nodePortFindings ?? []).map((f) => [`${f.namespace}/${f.service}`, f]),
  );
  const podsByNode = new Map<string, K8sPodSummary[]>();
  for (const p of overlay.pods) {
    if (!p.node) continue;
    const list = podsByNode.get(p.node);
    if (list) list.push(p);
    else podsByNode.set(p.node, [p]);
  }

  for (const pc of overlay.podCidrs) {
    const correlation = correlationByK8sNode.get(pc.node);
    const guestRef = correlation?.matched ? correlation.guestRef : undefined;
    const pveNode = pveNodeFromGuestRef(guestRef);
    const id = k8sPodSubnetId(overlay.clusterId, pc.node);
    const podCount = podsByNode.get(pc.node)?.length ?? 0;

    nodes.push({
      id,
      kind: K8S_POD_CIDR_KIND,
      label: pc.cidr,
      layer: "guest",
      nodeGroup: pveNode ?? "",
      status: guestRef ? "ok" : "unknown",
      badges: ["k8s", `k8sNode=${pc.node}`, `pods=${String(podCount)}`],
    });

    if (guestRef) {
      edges.push({ from: guestRef, to: id, kind: "k8s-correlation", status: "ok", badges: ["k8s"] });
    }
  }

  for (const pod of overlay.pods) {
    const correlation = pod.node ? correlationByK8sNode.get(pod.node) : undefined;
    const guestRef = correlation?.matched ? correlation.guestRef : undefined;
    const pveNode = pveNodeFromGuestRef(guestRef);
    const id = k8sPodId(overlay.clusterId, pod.namespace, pod.name);

    nodes.push({
      id,
      kind: K8S_POD_KIND,
      label: pod.name,
      layer: "guest",
      nodeGroup: pveNode ?? "",
      status: podStatus(pod.phase),
      badges: pod.podIp ? [`ip=${pod.podIp}`] : [],
    });

    if (pod.node) {
      edges.push({
        from: id,
        to: k8sPodSubnetId(overlay.clusterId, pod.node),
        kind: "k8s-pod-in-subnet",
        status: "ok",
        badges: [],
      });
    }
  }

  for (const svc of overlay.services) {
    if (!svc.clusterIp) continue; // headless service — no single address, nothing to anchor
    const id = k8sServiceId(overlay.clusterId, svc.namespace, svc.name);
    const finding = nodePortByService.get(`${svc.namespace}/${svc.name}`);
    const badges = [`type=${svc.type}`];
    if (finding) badges.push("unprotected");

    nodes.push({
      id,
      kind: K8S_SERVICE_KIND,
      label: `${svc.namespace}/${svc.name}`,
      layer: "guest",
      nodeGroup: "",
      status: finding ? "degraded" : "ok",
      badges,
    });
  }

  return { nodes, edges };
}
