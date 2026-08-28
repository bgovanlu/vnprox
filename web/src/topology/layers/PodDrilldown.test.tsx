// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { K8sOverlay, TopologyEdge, TopologyNode } from "../../api/types";
import { PodDrilldown } from "./PodDrilldown";

function overlay(): K8sOverlay {
  return {
    clusterId: "c1",
    cni: "flannel",
    podCidrs: [{ node: "k8s-node-1", cidr: "10.244.0.0/24" }],
    services: [
      {
        namespace: "default",
        name: "cache",
        type: "NodePort",
        clusterIp: "10.96.0.20",
        ports: [{ port: 6379, nodePort: 30637, protocol: "TCP" }],
      },
    ],
    pods: [
      { namespace: "default", name: "web-abc123", node: "k8s-node-1", podIp: "10.244.0.6", phase: "Running" },
    ],
    nodes: [{ k8sNode: "k8s-node-1", internalIp: "10.10.0.11", guestRef: "guest:pve1:200", matched: true }],
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

function topologyNodes(): TopologyNode[] {
  return [
    { id: "guest:pve1:200", kind: "guest", label: "app01", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
    {
      id: "guest-nic:pve1:200/net0",
      kind: "guest-nic",
      label: "app01/net0",
      layer: "guest",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    },
    { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "bond:pve1:bond0", kind: "bond", label: "bond0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
  ];
}

function topologyEdges(): TopologyEdge[] {
  return [
    { from: "guest-nic:pve1:200/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    { from: "bond:pve1:bond0", to: "bridge:pve1:vmbr0", kind: "port-of", status: "ok", badges: [] },
  ];
}

describe("PodDrilldown", () => {
  it("shows a loading state while the overlay hasn't arrived yet", () => {
    render(
      <PodDrilldown
        selection={{ kind: "pod", clusterId: "c1", namespace: "default", name: "web-abc123" }}
        overlay={undefined}
        topologyNodes={[]}
        topologyEdges={[]}
        onClose={() => undefined}
      />,
    );
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });

  it("renders the full pod -> node-guest -> bridge -> bond chain for a matched pod", () => {
    render(
      <PodDrilldown
        selection={{ kind: "pod", clusterId: "c1", namespace: "default", name: "web-abc123" }}
        overlay={overlay()}
        topologyNodes={topologyNodes()}
        topologyEdges={topologyEdges()}
        onClose={() => undefined}
      />,
    );
    expect(screen.getByText("default/web-abc123")).toBeInTheDocument();
    expect(screen.getByText("guest:pve1:200")).toBeInTheDocument();
    expect(screen.getByText("app01")).toBeInTheDocument();
    expect(screen.getByText("app01/net0")).toBeInTheDocument();
    expect(screen.getByText("vmbr0")).toBeInTheDocument();
    expect(screen.getByText("bond0")).toBeInTheDocument();
  });

  it("shows 'unmatched' and no underlay path for a pod-cidr region on an unmatched k8s node", () => {
    const unmatchedOverlay: K8sOverlay = {
      ...overlay(),
      nodes: [{ k8sNode: "k8s-node-1", internalIp: "10.10.0.99", matched: false }],
    };
    render(
      <PodDrilldown
        selection={{ kind: "pod-cidr", clusterId: "c1", node: "k8s-node-1" }}
        overlay={unmatchedOverlay}
        topologyNodes={topologyNodes()}
        topologyEdges={topologyEdges()}
        onClose={() => undefined}
      />,
    );
    expect(screen.getByText("unmatched")).toBeInTheDocument();
    expect(screen.getByText(/underlay path can.t be shown/)).toBeInTheDocument();
    expect(screen.queryByText("bond0")).not.toBeInTheDocument();
  });

  it("renders service details and the NodePort-exposure finding for a service selection", () => {
    render(
      <PodDrilldown
        selection={{ kind: "service", clusterId: "c1", namespace: "default", name: "cache" }}
        overlay={overlay()}
        topologyNodes={topologyNodes()}
        topologyEdges={topologyEdges()}
        onClose={() => undefined}
      />,
    );
    expect(screen.getByText("default/cache")).toBeInTheDocument();
    expect(screen.getByText("NodePort")).toBeInTheDocument();
    expect(screen.getByText(/no covering PVE firewall allow rule/)).toBeInTheDocument();
  });

  it("calls onClose when the close button is clicked", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <PodDrilldown
        selection={{ kind: "pod", clusterId: "c1", namespace: "default", name: "web-abc123" }}
        overlay={overlay()}
        topologyNodes={topologyNodes()}
        topologyEdges={topologyEdges()}
        onClose={onClose}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
