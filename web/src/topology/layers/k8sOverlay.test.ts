import { describe, expect, it } from "vitest";
import type { K8sOverlay } from "../../api/types";
import {
  computeK8sOverlay,
  isK8sSyntheticId,
  k8sPodId,
  k8sPodSubnetId,
  k8sServiceId,
  parseK8sSyntheticId,
  pveNodeFromGuestRef,
} from "./k8sOverlay";

function fixture(): K8sOverlay {
  return {
    clusterId: "c1",
    cni: "flannel",
    podCidrs: [
      { node: "k8s-node-1", cidr: "10.244.0.0/24" },
      { node: "k8s-node-2", cidr: "10.244.1.0/24" },
    ],
    services: [
      {
        namespace: "default",
        name: "web",
        type: "ClusterIP",
        clusterIp: "10.96.0.10",
        ports: [{ port: 80, protocol: "TCP" }],
      },
      {
        namespace: "default",
        name: "cache",
        type: "NodePort",
        clusterIp: "10.96.0.20",
        ports: [{ port: 6379, nodePort: 30637, protocol: "TCP" }],
      },
      // Headless service — no ClusterIP, must never produce a node.
      { namespace: "default", name: "headless", type: "ClusterIP", ports: [] },
    ],
    pods: [
      { namespace: "default", name: "web-abc123", node: "k8s-node-1", podIp: "10.244.0.6", phase: "Running" },
      { namespace: "default", name: "cache-xyz789", node: "k8s-node-2", podIp: "10.244.1.7", phase: "Pending" },
    ],
    nodes: [
      { k8sNode: "k8s-node-1", internalIp: "10.10.0.11", guestRef: "guest:pve1:200", matched: true },
      { k8sNode: "k8s-node-2", internalIp: "10.10.0.99", matched: false },
    ],
    nodePortFindings: [
      {
        clusterId: "c1",
        namespace: "default",
        service: "cache",
        port: 6379,
        nodePort: 30637,
        proto: "tcp",
        refs: ["guest:pve2:201"],
        detail: "NodePort 30637/tcp on default/cache has no covering PVE firewall allow rule on guest:pve2:201",
      },
    ],
    generatedAt: 1700000000,
  };
}

describe("computeK8sOverlay", () => {
  it("renders one pod-cidr region node per PodCIDR, anchored to the matched guest's node", () => {
    const { nodes } = computeK8sOverlay(fixture());
    const region1 = nodes.find((n) => n.id === k8sPodSubnetId("c1", "k8s-node-1"));
    expect(region1).toBeDefined();
    expect(region1?.kind).toBe("k8s-pod-cidr");
    expect(region1?.label).toBe("10.244.0.0/24");
    expect(region1?.nodeGroup).toBe("pve1");
    expect(region1?.status).toBe("ok");
  });

  it("places an unmatched k8s node's region in the cluster-spanning band, never a guessed column", () => {
    const { nodes } = computeK8sOverlay(fixture());
    const region2 = nodes.find((n) => n.id === k8sPodSubnetId("c1", "k8s-node-2"));
    expect(region2).toBeDefined();
    expect(region2?.nodeGroup).toBe("");
    expect(region2?.status).toBe("unknown");
  });

  it("draws a node<->guest correlation line only for matched nodes", () => {
    const { edges } = computeK8sOverlay(fixture());
    const correlationEdges = edges.filter((e) => e.kind === "k8s-correlation");
    expect(correlationEdges).toHaveLength(1);
    expect(correlationEdges[0]).toMatchObject({
      from: "guest:pve1:200",
      to: k8sPodSubnetId("c1", "k8s-node-1"),
    });
  });

  it("renders one node per pod, anchored the same way as its region", () => {
    const { nodes } = computeK8sOverlay(fixture());
    const pod1 = nodes.find((n) => n.id === k8sPodId("c1", "default", "web-abc123"));
    expect(pod1).toMatchObject({ kind: "k8s-pod", label: "web-abc123", nodeGroup: "pve1", status: "ok" });
    const pod2 = nodes.find((n) => n.id === k8sPodId("c1", "default", "cache-xyz789"));
    expect(pod2).toMatchObject({ nodeGroup: "", status: "degraded" });
  });

  it("renders one service node per service with a ClusterIP, skipping headless services", () => {
    const { nodes } = computeK8sOverlay(fixture());
    const web = nodes.find((n) => n.id === k8sServiceId("c1", "default", "web"));
    expect(web).toMatchObject({ kind: "k8s-service", label: "default/web", status: "ok" });
    const headless = nodes.find((n) => n.label.includes("headless"));
    expect(headless).toBeUndefined();
  });

  it("marks a service carrying a nodePortFinding as degraded with an 'unprotected' badge", () => {
    const { nodes } = computeK8sOverlay(fixture());
    const cache = nodes.find((n) => n.id === k8sServiceId("c1", "default", "cache"));
    expect(cache?.status).toBe("degraded");
    expect(cache?.badges).toContain("unprotected");
  });
});

describe("k8s synthetic id helpers", () => {
  it("round-trips pod-cidr/pod/service ids through parseK8sSyntheticId", () => {
    expect(parseK8sSyntheticId(k8sPodSubnetId("c1", "k8s-node-1"))).toEqual({
      kind: "pod-cidr",
      clusterId: "c1",
      node: "k8s-node-1",
    });
    expect(parseK8sSyntheticId(k8sPodId("c1", "default", "web-abc123"))).toEqual({
      kind: "pod",
      clusterId: "c1",
      namespace: "default",
      name: "web-abc123",
    });
    expect(parseK8sSyntheticId(k8sServiceId("c1", "default", "web"))).toEqual({
      kind: "service",
      clusterId: "c1",
      namespace: "default",
      name: "web",
    });
  });

  it("isK8sSyntheticId is true only for this module's own ids, never a real inventory ref", () => {
    expect(isK8sSyntheticId(k8sPodSubnetId("c1", "n1"))).toBe(true);
    expect(isK8sSyntheticId(k8sPodId("c1", "ns", "name"))).toBe(true);
    expect(isK8sSyntheticId(k8sServiceId("c1", "ns", "name"))).toBe(true);
    expect(isK8sSyntheticId("guest:pve1:200")).toBe(false);
    expect(isK8sSyntheticId("bridge:pve1:vmbr0")).toBe(false);
  });

  it("parseK8sSyntheticId returns undefined for a non-k8s id", () => {
    expect(parseK8sSyntheticId("guest:pve1:200")).toBeUndefined();
  });

  it("pveNodeFromGuestRef extracts the node segment of a guest ref", () => {
    expect(pveNodeFromGuestRef("guest:pve1:200")).toBe("pve1");
    expect(pveNodeFromGuestRef(undefined)).toBeUndefined();
    expect(pveNodeFromGuestRef("not-a-ref")).toBeUndefined();
  });
});
