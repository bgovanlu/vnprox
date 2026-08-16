// T-3004's WAN half: the verdict vocabulary is rendered honestly (an
// unrecognised verdict is not coerced into a known one, and "likely your
// ISP" is never shown for the plainer "WAN degraded"), and a target the
// daemon refuses is surfaced by name rather than as a generic failure.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { EdgeRoutesView, MeResponse, WanStatus, WanTargetsView } from "../api/types";
import { WanHealthPanel } from "./WanHealthPanel";

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: {
    "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
  },
};

let statusResult: { data: WanStatus | undefined; isLoading: boolean; error: Error | null } = {
  data: { verdict: "healthy", summary: "All configured WAN uplinks are healthy.", uplinks: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};
const targetsResult: { data: WanTargetsView | undefined } = { data: { node: "pve1", targets: [] } };
const routesResult: { data: EdgeRoutesView | undefined } = {
  data: { defaultRoutes: [{ node: "pve1", iface: "vmbr0", gateway: "192.0.2.1" }], staticRoutes: [], generatedAt: 0 },
};

const replaceMutate = vi.fn();

vi.mock("./edgeQueries", () => ({
  useWanStatusQuery: () => statusResult,
  useWanTargetsQuery: () => targetsResult,
  useEdgeRoutesQuery: () => routesResult,
  useReplaceWanTargetsMutation: () => ({ mutate: replaceMutate, isPending: false }),
}));

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: fullSession }),
}));

describe("WanHealthPanel", () => {
  it("renders the daemon's own verdict and summary", () => {
    statusResult = {
      data: {
        verdict: "likely_isp",
        summary: "WAN reference targets are degraded but the rest of the cluster looks healthy.",
        uplinks: [
          {
            node: "pve1",
            uplink: "vmbr0",
            status: "degraded",
            availabilityPct: 62.5,
            rttMs: 41.2,
            lossPct: 12,
            targets: [{ host: "1.1.1.1", at: 0, rttMs: 41, lossPct: 12, rollingRttMs: 40, rollingLossPct: 11, reachable: true }],
          },
        ],
        generatedAt: 0,
      },
      isLoading: false,
      error: null,
    };
    render(<WanHealthPanel />);

    expect(screen.getByTestId("wan-verdict")).toHaveTextContent("Likely your ISP, not the cluster");
    expect(screen.getByTestId("wan-verdict")).toHaveTextContent("rest of the cluster looks healthy");
  });

  it("does not read wan_degraded as an ISP verdict", () => {
    // The two are deliberately different claims: likely_isp additionally
    // asserts the rest of the cluster is quiet, which wan_degraded does not.
    statusResult = {
      data: { verdict: "wan_degraded", summary: "One or more WAN uplinks are degraded.", uplinks: [], generatedAt: 0 },
      isLoading: false,
      error: null,
    };
    render(<WanHealthPanel />);

    expect(screen.getByTestId("wan-verdict")).toHaveTextContent("WAN degraded");
    expect(screen.getByTestId("wan-verdict")).not.toHaveTextContent("ISP");
  });

  it("renders an unrecognised verdict as unrecognised", () => {
    statusResult = {
      data: { verdict: "brand_new_verdict", summary: "Something this client has not seen.", uplinks: [], generatedAt: 0 },
      isLoading: false,
      error: null,
    };
    render(<WanHealthPanel />);

    expect(screen.getByTestId("wan-verdict")).toHaveTextContent("Verdict not recognised");
  });

  it("surfaces a refused target host by name", async () => {
    statusResult = {
      data: { verdict: "no_targets", summary: "No WAN reference targets are configured yet.", uplinks: [], generatedAt: 0 },
      isLoading: false,
      error: null,
    };
    replaceMutate.mockImplementation((_targets: unknown, opts: { onError: (e: unknown) => void }) => {
      opts.onError(
        new ApiError(400, "validation_failed", "host must be an IP address or DNS name: --evil"),
      );
    });
    render(<WanHealthPanel />);

    await userEvent.type(screen.getByLabelText("Host"), "--evil");
    await userEvent.click(screen.getByRole("button", { name: "Add target" }));

    expect(screen.getByTestId("wan-target-refusal")).toHaveTextContent(
      "host must be an IP address or DNS name: --evil",
    );
  });

  it("sends a full-set replace when removing a target, not a patch", async () => {
    statusResult = {
      data: { verdict: "healthy", summary: "All configured WAN uplinks are healthy.", uplinks: [], generatedAt: 0 },
      isLoading: false,
      error: null,
    };
    targetsResult.data = {
      node: "pve1",
      targets: [
        { uplink: "vmbr0", host: "1.1.1.1" },
        { uplink: "vmbr0", host: "9.9.9.9" },
      ],
    };
    replaceMutate.mockImplementation(() => undefined);
    render(<WanHealthPanel />);

    const [firstRemove] = screen.getAllByRole("button", { name: "Remove" });
    if (firstRemove === undefined) throw new Error("expected a Remove button per configured target");
    await userEvent.click(firstRemove);

    expect(replaceMutate).toHaveBeenCalledWith([{ uplink: "vmbr0", host: "9.9.9.9" }], expect.anything());
  });
});
