// Shared test-only fixtures/harness for the five zone wizard component
// tests (SimpleZoneWizard.test.tsx etc.) — not imported by any production
// code. Builds a small three-node topology (mirroring testdata/clusters/
// three-node-vlan.yaml's bond0/vmbr0/LLDP shape closely enough for the
// LLDP trunk-check and peer-suggest hooks to have real graph structure to
// traverse) plus a `vi.stubGlobal("fetch", ...)` mock covering every route
// a wizard's hooks touch: GET /auth/me, GET /topology, GET /inventory/*,
// and POST /changesets (captured into `postedChangesets` so a test can
// assert the exact drafted `ops`).
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import { ToastProvider } from "../../components/Toast";
import type { Changeset, EntityDetail, MeResponse, Op, TopologyResponse } from "../../api/types";

export const sessionWithSdnWrite: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: {
    pve1: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
    pve2: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
    pve3: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
    "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
  },
};

/** pve3's trunk is deliberately missing VID 300 (present on pve1/pve2) —
 * mirrors testdata/clusters/three-node-vlan.yaml's own T-403 fixture, so
 * the VLAN wizard's LLDP cross-check has a real case to warn about. */
export function threeNodeTopology(): TopologyResponse {
  const nodes: TopologyResponse["nodes"] = [];
  const edges: TopologyResponse["edges"] = [];
  const NODES = ["pve1", "pve2", "pve3"];
  for (const node of NODES) {
    nodes.push(
      { id: `bridge:${node}:vmbr0`, kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: node, status: "ok", badges: [] },
      { id: `bond:${node}:bond0`, kind: "bond", label: "bond0", layer: "l2", nodeGroup: node, status: "ok", badges: [] },
      { id: `physnic:${node}:eno1`, kind: "physnic", label: "eno1", layer: "phys", nodeGroup: node, status: "ok", badges: [] },
      { id: `physnic:${node}:eno2`, kind: "physnic", label: "eno2", layer: "phys", nodeGroup: node, status: "ok", badges: [] },
      {
        id: `lldp-neighbor:${node}:eno1/sw1`,
        kind: "lldp-neighbor",
        label: "sw-core-01",
        layer: "phys",
        nodeGroup: node,
        status: "ok",
        badges: [],
      },
      {
        id: `lldp-neighbor:${node}:eno2/sw2`,
        kind: "lldp-neighbor",
        label: "sw-core-02",
        layer: "phys",
        nodeGroup: node,
        status: "ok",
        badges: [],
      },
    );
    edges.push(
      { from: `bond:${node}:bond0`, to: `bridge:${node}:vmbr0`, kind: "port-of", status: "ok", badges: [] },
      { from: `physnic:${node}:eno1`, to: `bond:${node}:bond0`, kind: "enslaved-by", status: "ok", badges: [] },
      { from: `physnic:${node}:eno2`, to: `bond:${node}:bond0`, kind: "enslaved-by", status: "ok", badges: [] },
      { from: `physnic:${node}:eno1`, to: `lldp-neighbor:${node}:eno1/sw1`, kind: "lldp-adjacent", status: "ok", badges: [] },
      { from: `physnic:${node}:eno2`, to: `lldp-neighbor:${node}:eno2/sw2`, kind: "lldp-adjacent", status: "ok", badges: [] },
    );
  }
  return { nodes, edges, layers: ["phys", "l2", "sdn", "guest"], generatedAt: 1_752_000_000 };
}

function entityDetail(ref: string, kind: string, fields: Record<string, unknown>): EntityDetail {
  const node = ref.split(":")[1] ?? "";
  return { ref, kind, node, label: ref, fields, provenance: {}, related: [], generatedAt: 1_752_000_000 };
}

/** node -> underlay address, for the VXLAN wizard's peer-suggest test. */
export function bridgeAddress(node: string): string {
  return `10.10.0.1${node.slice(-1)}/24`;
}

// Go field names holding Go types, because that is what
// GET /inventory/{ref} actually returns: topology.Detail runs
// json.Marshal over the inventory struct, so inventory.Bridge.Addresses
// ([]string) arrives as `Addresses: [...]` and LldpNeighbor.TaggedVLANs
// ([]int) as `TaggedVLANs: [...]`.
//
// This fixture used to say `{ addresses: "<cidr>" }` and
// `{ chassisName, portId, taggedVlans: "100,200,300" }` — keys and types
// the server has never produced. Both wizards read the map the same wrong
// way, so both were broken in production while every test here passed
// (T-2108). A fixture that invents the shape the code expects tests
// nothing. internal/topology's TestDetailFieldShapes pins the server half
// of this pair; api/entityFields.ts is the one place that reads it.
const inventoryFixture: Record<string, EntityDetail> = {
  "bridge:pve1:vmbr0": entityDetail("bridge:pve1:vmbr0", "bridge", { Addresses: [bridgeAddress("pve1")] }),
  "bridge:pve2:vmbr0": entityDetail("bridge:pve2:vmbr0", "bridge", { Addresses: [bridgeAddress("pve2")] }),
  "bridge:pve3:vmbr0": entityDetail("bridge:pve3:vmbr0", "bridge", { Addresses: [bridgeAddress("pve3")] }),
  "lldp-neighbor:pve1:eno1/sw1": entityDetail("lldp-neighbor:pve1:eno1/sw1", "lldp-neighbor", {
    ChassisName: "sw-core-01",
    PortID: "Te1/0/1",
    TaggedVLANs: [100, 200, 300],
  }),
  "lldp-neighbor:pve1:eno2/sw2": entityDetail("lldp-neighbor:pve1:eno2/sw2", "lldp-neighbor", {
    ChassisName: "sw-core-02",
    PortID: "Te1/0/1",
    TaggedVLANs: [100, 200, 300],
  }),
  "lldp-neighbor:pve2:eno1/sw1": entityDetail("lldp-neighbor:pve2:eno1/sw1", "lldp-neighbor", {
    ChassisName: "sw-core-01",
    PortID: "Te1/0/2",
    TaggedVLANs: [100, 200, 300],
  }),
  "lldp-neighbor:pve2:eno2/sw2": entityDetail("lldp-neighbor:pve2:eno2/sw2", "lldp-neighbor", {
    ChassisName: "sw-core-02",
    PortID: "Te1/0/2",
    TaggedVLANs: [100, 200, 300],
  }),
  // pve3 is missing VID 300 on both switch ports — the AC2 scenario.
  "lldp-neighbor:pve3:eno1/sw1": entityDetail("lldp-neighbor:pve3:eno1/sw1", "lldp-neighbor", {
    ChassisName: "sw-core-01",
    PortID: "Te1/0/3",
    TaggedVLANs: [100, 200],
  }),
  "lldp-neighbor:pve3:eno2/sw2": entityDetail("lldp-neighbor:pve3:eno2/sw2", "lldp-neighbor", {
    ChassisName: "sw-core-02",
    PortID: "Te1/0/3",
    TaggedVLANs: [100, 200],
  }),
};

function urlOf(input: RequestInfo | URL): string {
  return typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export interface WizardFetchStub {
  /** Every POST /changesets request body captured, in order. */
  postedChangesets: { title: string; ops: Op[] }[];
}

/** Installs the shared fetch stub. Call in beforeEach; pair with
 * `vi.unstubAllGlobals()` in afterEach. */
export function stubWizardFetch(): WizardFetchStub {
  const stub: WizardFetchStub = { postedChangesets: [] };
  const topology = threeNodeTopology();

  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = urlOf(input);
      const method = init?.method ?? "GET";

      if (url.includes("/auth/me")) {
        return Promise.resolve(jsonResponse(sessionWithSdnWrite));
      }
      if (url.includes("/topology")) {
        return Promise.resolve(jsonResponse(topology));
      }
      if (url.includes("/inventory/")) {
        const ref = decodeURIComponent(url.split("/inventory/")[1] ?? "");
        const detail = inventoryFixture[ref];
        if (detail) return Promise.resolve(jsonResponse(detail));
        return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "not found" } }, 404));
      }
      if (url.includes("/ipam/subnets/") && url.includes("/allocations")) {
        // The SubnetStep gateway pre-fill reads this; a brand-new wizard CIDR
        // has no known allocations, so return an empty (but well-formed)
        // address list — arrays, never null, matching the real backend.
        return Promise.resolve(
          jsonResponse({
            cidr: "", prefix: 24, total: 0, entries: [], freeRanges: [], conflicts: [],
            counts: { allocated: 0, reserved: 0, observed: 0, gateway: 0, conflict: 0, free: 0 }, generatedAt: 1,
          }),
        );
      }
      if (url.includes("/changesets") && method === "POST") {
        const rawBody = typeof init?.body === "string" ? init.body : "";
        const body = rawBody ? (JSON.parse(rawBody) as { title: string; ops: Op[] }) : { title: "", ops: [] };
        stub.postedChangesets.push(body);
        const created: Changeset = {
          id: `cs-${String(stub.postedChangesets.length)}`,
          title: body.title,
          author: "root@pam",
          status: "draft",
          ops: body.ops,
          findings: [],
          createdAt: 1_752_000_000,
          updatedAt: 1_752_000_000,
        };
        return Promise.resolve(jsonResponse(created));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  return stub;
}

export function renderWithProviders(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>{ui}</ToastProvider>
    </QueryClientProvider>,
  );
}
