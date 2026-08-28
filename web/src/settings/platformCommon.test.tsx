// SPDX-License-Identifier: Apache-2.0

// T-4105: RefusalNotice (used by every Platform-panel section — Webhooks,
// Tokens, Plugins, Doctor Live) is where a capability denial's precise
// "why can't I?" answer surfaces in the UI, without a second round trip:
// the daemon already attached it to the same 403 that got the caller here
// (docs/api.md's error-envelope conventions, `details.explanation`). These
// tests exercise the component directly rather than through one section,
// since every section shares this rendering.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import { RefusalNotice } from "./platformCommon";

describe("RefusalNotice explanation", () => {
  it("names the missing PVE privilege and path for a single-privilege denial", () => {
    const err = new ApiError(403, "forbidden", "missing required capability: netWrite", {
      explanation: {
        capability: "netWrite",
        granted: false,
        missing: [{ privilege: "Sys.Modify", path: "/nodes/pve1", confirmed: true }],
      },
    });
    render(<RefusalNotice error={err} testId="notice" />);
    expect(screen.getByTestId("notice-explanation")).toHaveTextContent("Sys.Modify at /nodes/pve1");
  });

  it("names every missing privilege, not just the first, and flags an unconfirmed one", () => {
    const err = new ApiError(403, "forbidden", "missing required capability: capture", {
      explanation: {
        capability: "capture",
        granted: false,
        missing: [
          { privilege: "Sys.Modify", path: "/nodes/pve1", confirmed: true },
          { privilege: "Sys.Console", path: "/nodes/pve1", confirmed: false },
        ],
      },
    });
    render(<RefusalNotice error={err} testId="notice" />);
    const text = screen.getByTestId("notice-explanation").textContent;
    expect(text).toContain("Sys.Modify at /nodes/pve1");
    expect(text).toContain("Sys.Console at /nodes/pve1 (also required; not confirmed missing)");
  });

  it("renders the daemon's reason instead of a privilege for a non-privilege-derived denial", () => {
    const err = new ApiError(403, "forbidden", "missing required capability: automationWrite", {
      explanation: {
        capability: "automationWrite",
        granted: false,
        reason: "not derived from any PVE privilege — automation/automationWrite exist only as API token scopes minted via POST /tokens, never granted through a PVE ACL",
      },
    });
    render(<RefusalNotice error={err} testId="notice" />);
    expect(screen.getByTestId("notice-explanation")).toHaveTextContent("API token scopes");
  });

  it("does not render an explanation block for a 403 that carries no explanation", () => {
    // The non-leaking / no-second-guess form: some other authorization
    // layer's 403 (or an older daemon) that never populated
    // details.explanation must not have this component fabricate one.
    const err = new ApiError(403, "forbidden", "refused");
    render(<RefusalNotice error={err} testId="notice" />);
    expect(screen.queryByTestId("notice-explanation")).not.toBeInTheDocument();
  });

  it("does not render an explanation block for a non-403 error", () => {
    const err = new ApiError(404, "not_found", "route not mounted");
    render(<RefusalNotice error={err} testId="notice" />);
    expect(screen.queryByTestId("notice-explanation")).not.toBeInTheDocument();
  });
});
