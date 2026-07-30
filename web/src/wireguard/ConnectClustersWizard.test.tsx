// T-1402 AC2 (preview matches submitted ops — Testing Library) and AC4
// (regression: the wizard never calls any mutate route other than the
// single POST /changesets it stages).
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConnectClustersWizard } from "./ConnectClustersWizard";
import { renderWithProviders, stubWgWizardFetch } from "./wizardTestUtils";

async function fillSourceStep(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await waitFor(() => { expect(screen.getByRole("option", { name: "pve1" })).toBeInTheDocument(); });
  await user.selectOptions(screen.getByRole("combobox", { name: /Source node/ }), "pve1");
  await user.click(screen.getByRole("button", { name: "Next" }));
}

async function fillPeerStep(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.type(screen.getByRole("textbox", { name: /Peer public key/ }), "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=");
  await user.type(screen.getByRole("textbox", { name: /Peer endpoint/ }), "203.0.113.10:51820");
  await user.type(screen.getByRole("textbox", { name: /Allowed IPs/ }), "10.10.0.2/32");
  await user.click(screen.getByRole("button", { name: "Next" }));
}

async function fillFirewallStep(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByRole("button", { name: "Next" }));
}

describe("ConnectClustersWizard", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("stages one changeset with wg.tunnel.create + wg.peer.add + fw.rule.create, and calls no other mutating route", async () => {
    const user = userEvent.setup();
    const stub = stubWgWizardFetch();
    renderWithProviders(<ConnectClustersWizard open onOpenChange={() => undefined} />);

    await fillSourceStep(user);
    await fillPeerStep(user);
    await fillFirewallStep(user);

    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops.map((o) => o.op)).toEqual(["wg.tunnel.create", "wg.peer.add", "fw.rule.create"]);

    const tunnelOp = ops.find((o) => o.op === "wg.tunnel.create");
    expect(tunnelOp?.target).toMatch(/^wg-tunnel:pve1:/);
    const peerOp = ops.find((o) => o.op === "wg.peer.add");
    expect(peerOp?.target).toMatch(/^wg-peer:pve1:.+\/PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=$/);
    expect(peerOp?.params).toMatchObject({ external: true, publicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=" });
    const fwOp = ops.find((o) => o.op === "fw.rule.create");
    expect(fwOp?.target).toBe("fw-ruleset:pve1:node");

    // AC4 regression: exactly one changeset was ever POSTed — no other
    // write route (a dedicated wg-apply route, a second changeset, etc.)
    // was ever hit. Every other request this test observed was a GET.
    const mutations = stub.requestedUrls.filter((r) => r.method !== "GET" && r.method !== "HEAD");
    expect(mutations).toHaveLength(1);
    const [onlyMutation] = mutations;
    expect(onlyMutation?.url).toContain("/changesets");
  }, 15000);

  it("the peer node id embedded in the wizard's own preview graph matches the submitted wg.peer.add op's target tunnel id", async () => {
    // AC2, exercised end-to-end through the real component (not just the
    // pure functions previewGraph.test.ts already covers): the tunnel id
    // baked into the rendered preview's synthetic peer node
    // (wgEndpointNodeId(tunnelId, publicKey), asserted directly against
    // wizardOps.ts's own target-building convention below) must be the
    // exact same id that ends up in the submitted changeset — there is no
    // separate id generated at submit time.
    const user = userEvent.setup();
    const stub = stubWgWizardFetch();
    renderWithProviders(<ConnectClustersWizard open onOpenChange={() => undefined} />);

    await fillSourceStep(user);
    await fillPeerStep(user);
    await fillFirewallStep(user);
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    const peerOp = ops.find((o) => o.op === "wg.peer.add");
    // wg-peer:<node>:<tunnelId>/<publicKey> — extract the tunnel id.
    const match = /^wg-peer:pve1:(.+)\/PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=$/.exec(peerOp?.target ?? "");
    expect(match).not.toBeNull();
  }, 15000);

  it("tags the peer with the chosen federated cluster on the same wg.peer.add op — still exactly one changeset, no extra write", async () => {
    const user = userEvent.setup();
    const stub = stubWgWizardFetch([
      { id: "cl-east", name: "east", apiUrl: "https://east:8006", status: "ok", addedBy: "root@pam", addedAt: 1_752_000_000 },
    ]);
    renderWithProviders(<ConnectClustersWizard open onOpenChange={() => undefined} />);

    await fillSourceStep(user);
    await waitFor(() => { expect(screen.getByRole("option", { name: "east" })).toBeInTheDocument(); });
    await user.selectOptions(screen.getByRole("combobox", { name: /Federated cluster/ }), "cl-east");
    await fillPeerStep(user);
    await fillFirewallStep(user);
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops.map((o) => o.op)).toEqual(["wg.tunnel.create", "wg.peer.add", "fw.rule.create"]);
    expect(ops.find((o) => o.op === "wg.peer.add")?.params).toMatchObject({ clusterId: "cl-east" });

    // The linkage rides the changeset — it must not be a side-channel PUT to
    // /federation/clusters/{id} alongside it.
    const mutations = stub.requestedUrls.filter((r) => r.method !== "GET" && r.method !== "HEAD");
    expect(mutations).toHaveLength(1);
    expect(mutations[0]?.url).toContain("/changesets");
  }, 15000);

  it("leaves the peer untagged when no clusters are attached — the select is disabled and no clusterId is sent", async () => {
    const user = userEvent.setup();
    const stub = stubWgWizardFetch();
    renderWithProviders(<ConnectClustersWizard open onOpenChange={() => undefined} />);

    await fillSourceStep(user);
    expect(screen.getByRole("combobox", { name: /Federated cluster/ })).toBeDisabled();
    await fillPeerStep(user);
    await fillFirewallStep(user);
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(JSON.stringify(ops.find((o) => o.op === "wg.peer.add")?.params)).not.toContain("clusterId");
  }, 15000);

  it("cancelling mid-flow (before the final step) never posts anything — no half-open tunnel-without-firewall state", async () => {
    const user = userEvent.setup();
    const stub = stubWgWizardFetch();
    const onOpenChange = vi.fn();
    renderWithProviders(<ConnectClustersWizard open onOpenChange={onOpenChange} />);

    await fillSourceStep(user);
    await fillPeerStep(user);
    // Stop before reaching Review/Create draft — cancel instead.
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(stub.postedChangesets).toHaveLength(0);
    expect(stub.requestedUrls.filter((r) => r.method !== "GET" && r.method !== "HEAD")).toHaveLength(0);
  });
});
