// SPDX-License-Identifier: Apache-2.0

// T-806 AC1: gating (disabled with correct copy for a non-qemu/external/
// IP-literal src and for a qemu src with no detected guest agent; enabled
// and calls POST /simulate/verify for an eligible src).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SimEndpointSpec, SimulateRequest, VerifyEligibility, VerifyResult } from "../api/types";
import { VerifyLiveButton } from "./VerifyLiveButton";

const eligibilityMock = vi.fn<(ref: string) => Promise<VerifyEligibility>>();
const verifyMock = vi.fn<(req: SimulateRequest) => Promise<VerifyResult>>();

vi.mock("../api/simulate", () => ({
  simulateVerifyEligibility: (ref: string) => eligibilityMock(ref),
  simulateVerify: (req: SimulateRequest) => verifyMock(req),
  simulatePath: vi.fn(),
}));

const guestNicSrc: SimEndpointSpec = { kind: "guest-nic", ref: "guest-nic:pve1:300/net0" };
const ipSrc: SimEndpointSpec = { kind: "ip", ip: "10.0.0.5" };
const externalSrc: SimEndpointSpec = { kind: "external" };

const request: SimulateRequest = {
  src: guestNicSrc,
  dst: { kind: "guest-nic", ref: "guest-nic:pve1:301/net0" },
  proto: "tcp",
  port: 22,
};

function renderButton(src: SimEndpointSpec | undefined, onResult = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <VerifyLiveButton src={src} request={request} onResult={onResult} />
    </QueryClientProvider>,
  );
  return { onResult };
}

describe("VerifyLiveButton gating", () => {
  it("is disabled with plain-English copy for an external src", () => {
    renderButton(externalSrc);
    expect(screen.getByRole("button", { name: /verify live/i })).toBeDisabled();
    expect(screen.getByText(/pick a guest NIC/i)).toBeInTheDocument();
    expect(eligibilityMock).not.toHaveBeenCalled();
  });

  it("is disabled with plain-English copy for an IP-literal src", () => {
    renderButton(ipSrc);
    expect(screen.getByRole("button", { name: /verify live/i })).toBeDisabled();
    expect(screen.getByText(/pick a guest NIC/i)).toBeInTheDocument();
    expect(eligibilityMock).not.toHaveBeenCalled();
  });

  it("is disabled with a not-a-QEMU-guest reason when eligibility resolves not-qemu", async () => {
    eligibilityMock.mockResolvedValueOnce({ eligible: false, reason: "not-qemu" });
    renderButton(guestNicSrc);
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /verify live/i })).toBeDisabled();
    });
    expect(await screen.findByText(/isn't a QEMU guest/i)).toBeInTheDocument();
  });

  it("is disabled with a no-detected-agent reason for a qemu src with no reachable guest agent", async () => {
    eligibilityMock.mockResolvedValueOnce({ eligible: false, reason: "agent-unreachable" });
    renderButton(guestNicSrc);
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /verify live/i })).toBeDisabled();
    });
    expect(await screen.findByText(/no guest agent was detected/i)).toBeInTheDocument();
  });

  it("is enabled for an eligible qemu guest-nic src and calls POST /simulate/verify on click", async () => {
    eligibilityMock.mockResolvedValueOnce({ eligible: true });
    const verifyResult: VerifyResult = {
      simulated: {
        verdict: "allow",
        src: { kind: "guest-nic" },
        dst: { kind: "guest-nic" },
        hops: [],
        caveats: [],
      },
      observed: { outcome: "reachable" },
      diverges: false,
    };
    verifyMock.mockResolvedValueOnce(verifyResult);

    const { onResult } = renderButton(guestNicSrc);
    const button = await screen.findByRole("button", { name: /verify live/i });
    await waitFor(() => {
      expect(button).toBeEnabled();
    });
    expect(screen.queryByText(/QEMU guest/i)).not.toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(button);

    await waitFor(() => {
      expect(verifyMock).toHaveBeenCalledWith(request);
    });
    await waitFor(() => {
      expect(onResult).toHaveBeenCalledWith(verifyResult);
    });
  });
});
