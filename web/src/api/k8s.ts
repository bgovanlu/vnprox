// Kubernetes read-view API calls (docs/api.md's Kubernetes section, T-1501;
// internal/api/k8s.go's GET /k8s/clusters and GET /k8s/{clusterId}/overlay).
// Read-only forever — T-1501's own invariant, carried forward here: this
// module never calls POST/DELETE /k8s/clusters (registering/deregistering a
// *local* cluster record, itself never a k8s-cluster mutation) because
// T-1502 is a read-only map layer + flow attribution feature with no
// cluster-management UI anywhere (see docs/features/topology.md §1's new
// Kubernetes layer entry and this task's completion report).
import { apiFetch } from "./client";
import type { K8sCluster, K8sClustersResponse, K8sOverlay } from "./types";

/** GET /k8s/clusters — every registered cluster (kubeconfig never echoed
 * back). */
export function fetchK8sClusters(): Promise<K8sCluster[]> {
  return apiFetch<K8sClustersResponse>("/k8s/clusters").then((r) => r.items);
}

/** GET /k8s/{clusterId}/overlay — a live poll: pod CIDR / node<->guest
 * correlation / detected CNI / NodePort-exposure findings, computed fresh
 * on every call. */
export function fetchK8sOverlay(clusterId: string): Promise<K8sOverlay> {
  return apiFetch<K8sOverlay>(`/k8s/${encodeURIComponent(clusterId)}/overlay`);
}
