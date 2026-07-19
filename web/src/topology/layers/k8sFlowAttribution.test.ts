import { describe, expect, it } from "vitest";
import type { K8sOverlay } from "../../api/types";
import { attributeK8sFlow, buildK8sAttributionIndex, formatK8sAttribution, resolveK8sAddress } from "./k8sFlowAttribution";
import { k8sPodId, k8sPodSubnetId, k8sServiceId } from "./k8sOverlay";

function overlay(): K8sOverlay {
  return {
    clusterId: "c1",
    cni: "flannel",
    podCidrs: [{ node: "k8s-node-1", cidr: "10.244.0.0/24" }],
    services: [
      { namespace: "default", name: "web", type: "ClusterIP", clusterIp: "10.96.0.10" },
      { namespace: "default", name: "headless", type: "ClusterIP" }, // no clusterIp
    ],
    pods: [{ namespace: "default", name: "web-abc123", node: "k8s-node-1", podIp: "10.244.0.6", phase: "Running" }],
    nodes: [{ k8sNode: "k8s-node-1", matched: false }],
    generatedAt: 1700000000,
  };
}

describe("resolveK8sAddress", () => {
  it("matches an exact service ClusterIP first", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    expect(resolveK8sAddress(index, "10.96.0.10")).toBe(k8sServiceId("c1", "default", "web"));
  });

  it("matches an exact pod IP", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    expect(resolveK8sAddress(index, "10.244.0.6")).toBe(k8sPodId("c1", "default", "web-abc123"));
  });

  it("falls back to pod-CIDR containment for an address inside the block but not a known pod", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    expect(resolveK8sAddress(index, "10.244.0.99")).toBe(k8sPodSubnetId("c1", "k8s-node-1"));
  });

  it("returns undefined for an address outside every CIDR/service/pod — never a wrong guess", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    expect(resolveK8sAddress(index, "192.168.50.5")).toBeUndefined();
  });

  it("never indexes a headless service (no clusterIp)", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    expect(index.exact.has("")).toBe(false);
    for (const ref of index.exact.values()) {
      expect(ref).not.toContain("headless");
    }
  });
});

describe("attributeK8sFlow", () => {
  it("checks dstIp before srcIp", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    const ref = attributeK8sFlow(index, { srcIp: "203.0.113.5", dstIp: "10.96.0.10" });
    expect(ref).toBe(k8sServiceId("c1", "default", "web"));
  });

  it("falls back to srcIp when dstIp resolves to nothing", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    const ref = attributeK8sFlow(index, { srcIp: "10.244.0.6", dstIp: "203.0.113.5" });
    expect(ref).toBe(k8sPodId("c1", "default", "web-abc123"));
  });

  it("shows no attribution (undefined) when neither address resolves", () => {
    const index = buildK8sAttributionIndex([overlay()]);
    const ref = attributeK8sFlow(index, { srcIp: "203.0.113.5", dstIp: "203.0.113.6" });
    expect(ref).toBeUndefined();
  });
});

describe("formatK8sAttribution", () => {
  it("renders a service ref as 'ns/name (svc)'", () => {
    expect(formatK8sAttribution(k8sServiceId("c1", "default", "web"))).toBe("default/web (svc)");
  });
  it("renders a pod ref as 'ns/name (pod)'", () => {
    expect(formatK8sAttribution(k8sPodId("c1", "default", "web-abc123"))).toBe("default/web-abc123 (pod)");
  });
  it("renders a pod-subnet ref as 'node (pod net)'", () => {
    expect(formatK8sAttribution(k8sPodSubnetId("c1", "k8s-node-1"))).toBe("k8s-node-1 (pod net)");
  });
});
