// SPDX-License-Identifier: Apache-2.0

// Phase 36. This resolver's failure mode is silence — an unresolved remedy
// renders no button, and the finding still looks perfectly fine — so every
// branch that returns `undefined` is asserted deliberately rather than being
// left as whatever falls out.
import { describe, expect, it, vi } from "vitest";
import type { Remediation } from "../api/types";
import { mgmtStrings } from "../mgmt/strings";
import { isKnownOperationalAction, remediationAction, type RemediationContext } from "./remediation";

function ctx(over: Partial<RemediationContext> = {}): RemediationContext {
  return {
    netWrite: true,
    navigate: vi.fn(),
    openMgmtWizard: vi.fn(),
    runOperational: vi.fn(),
    ...over,
  };
}

const mgmt: Remediation = {
  action: "mgmt.redundancy",
  kind: "navigate",
  label: "Add a redundant path",
  params: { node: "pve1" },
};

describe("remediationAction — navigate remedies", () => {
  it("opens the redundancy wizard for the named node", () => {
    const c = ctx();
    const resolved = remediationAction(mgmt, c);
    resolved?.onClick();
    expect(c.openMgmtWizard).toHaveBeenCalledWith({ node: "pve1" });
    expect(resolved?.confirms).toBe(false);
  });

  it("uses the frontend's own copy, not the daemon's label", () => {
    // The label is user-visible copy and belongs where i18nCoverage.test.ts
    // can see it. It also has to stay byte-identical to what this
    // affordance said before Phase 36 moved the decision — several specs
    // locate the button by that exact name.
    expect(remediationAction(mgmt, ctx())?.label).toBe(mgmtStrings.launch.button);
    expect(remediationAction(mgmt, ctx())?.label).not.toBe("Add a redundant path");
  });

  it("prefers local copy over the daemon's label where it has some", () => {
    const resolved = remediationAction(
      { action: "navigate", kind: "navigate", label: "Go somewhere", params: { to: "/x" } },
      ctx(),
    );
    expect(resolved?.label).toBe("View in simulator");
  });

  it("falls back to the daemon's label for a runnable action it has no copy for", () => {
    // The fallback matters for actions this build knows how to *run* but
    // has no wording for — without it they would render an empty button.
    const resolved = remediationAction(
      { action: "service.start", kind: "operational", label: "Start dnsmasq on pvecube" },
      ctx(),
    );
    expect(resolved?.label).toBe("Start dnsmasq on pvecube");
  });

  it("navigates to the target route", () => {
    const c = ctx();
    remediationAction(
      { action: "navigate", kind: "navigate", label: "View in simulator", params: { to: "/tools?a=b" } },
      c,
    )?.onClick();
    expect(c.navigate).toHaveBeenCalledWith("/tools?a=b");
  });

  it("resolves nothing when a required parameter is missing", () => {
    // A wizard opened for no node, or a navigation to nowhere, is worse
    // than no button: it looks like the product offered help and then did
    // nothing.
    expect(remediationAction({ ...mgmt, params: {} }, ctx())).toBeUndefined();
    expect(remediationAction({ ...mgmt, params: { node: "" } }, ctx())).toBeUndefined();
    expect(
      remediationAction({ action: "navigate", kind: "navigate", label: "x", params: {} }, ctx()),
    ).toBeUndefined();
  });

  it("resolves nothing when the surface cannot perform the navigation", () => {
    expect(remediationAction(mgmt, ctx({ openMgmtWizard: undefined }))).toBeUndefined();
  });
});

describe("remediationAction — operational remedies", () => {
  const install: Remediation = { action: "lldp.install", kind: "operational", label: "Install lldpd" };

  it("always demands confirmation", () => {
    // Tier 2's ceremony. A surface that renders this without confirming
    // first has broken the contract in docs/security.md, and `confirms` is
    // how it knows.
    expect(remediationAction(install, ctx())?.confirms).toBe(true);
  });

  it("dispatches to the surface's runner with the whole remedy", () => {
    const c = ctx();
    remediationAction(install, c)?.onClick();
    expect(c.runOperational).toHaveBeenCalledWith(install);
  });

  it("resolves nothing without netWrite", () => {
    // A read-only session gets no button at all, not a disabled one: the
    // finding's own text already says what is wrong, and a greyed-out
    // control explains nothing about why.
    expect(remediationAction(install, ctx({ netWrite: false }))).toBeUndefined();
  });

  it("resolves nothing when the surface supplied no runner", () => {
    // Fails closed. The findings stream has no confirmation dialog, so it
    // passes no runner, so it offers no mutating button — by construction
    // rather than by remembering to.
    expect(remediationAction(install, ctx({ runOperational: undefined }))).toBeUndefined();
  });

  it("resolves nothing for an operational action this build cannot run", () => {
    expect(
      remediationAction({ action: "service.stop", kind: "operational", label: "Stop it" }, ctx()),
    ).toBeUndefined();
  });
});

describe("remediationAction — forward compatibility", () => {
  it("renders nothing for an unknown action rather than guessing", () => {
    // A newer daemon offering a remedy this SPA has never heard of must
    // degrade to "no button", not to a crash and not to a wrong button.
    expect(
      remediationAction({ action: "future.thing", kind: "navigate", label: "Do it" }, ctx()),
    ).toBeUndefined();
  });

  it("renders nothing when there is no remedy at all", () => {
    expect(remediationAction(undefined, ctx())).toBeUndefined();
  });
});

describe("isKnownOperationalAction", () => {
  it("names exactly the operational actions this build can run", () => {
    // Kept in step with Phase 36's cards deliberately: adding an action to
    // the set without a runner produces a button that posts a request the
    // client cannot shape.
    for (const a of ["lldp.install", "collector.refresh", "service.start"]) {
      expect(isKnownOperationalAction(a)).toBe(true);
    }
    expect(isKnownOperationalAction("mgmt.redundancy")).toBe(false);
    expect(isKnownOperationalAction("service.restart")).toBe(false);
  });
});
