// Direct unit tests for the shared SubnetStep component (T-701) — the five
// zone wizards' own golden-ops tests (SimpleZoneWizard.test.tsx etc.) cover
// it end to end through one wizard each; this file drives it standalone so
// every zone-type variant and the live-allocation-grid refinement get
// focused coverage without repeating a full wizard flow five times.
import { afterEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SubnetStep, emptySubnetStepValue, type SubnetStepValue, type SubnetZoneType } from "./SubnetStep";
import { wizardStrings } from "./strings";

const S = wizardStrings;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/** Stubs GET /ipam/subnets/{cidr}/allocations: `cellsByCidr` maps a bare
 * (non-percent-encoded) CIDR to the addresses it should resolve, standing in
 * for "vnprox already has IPAM data for this subnet" — any other CIDR 404s,
 * modeling "brand new, nothing known yet" (SubnetStep's own doc comment: it
 * then falls back to the pure firstUsableIPv4 guess). The fixture is written
 * as a flat cell list (each with a state); this stub derives the address
 * list's occupied `entries` and collapsed `freeRanges` from it, so the free
 * cells become the free ranges SubnetStep now reads its suggestion from. */
function stubIpamFetch(cellsByCidr: Record<string, { ip: string; state: string }[]>, delayMs = 0): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
      const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const m = /\/ipam\/subnets\/(.+)\/allocations(?:\?|$)/.exec(url);
      if (!m) return jsonResponse({ error: { code: "not_found", message: "not found" } }, 404);
      const cidr = decodeURIComponent(m[1] ?? "");
      const cells = cellsByCidr[cidr];
      if (!cells) return jsonResponse({ error: { code: "not_found", message: "not found" } }, 404);
      const entries = cells.filter((c) => c.state !== "free");
      const freeRanges = cells.filter((c) => c.state === "free").map((c) => ({ start: c.ip, end: c.ip, count: 1 }));
      return jsonResponse({
        cidr, prefix: 24, total: cells.length, entries, freeRanges,
        counts: { allocated: 0, reserved: 0, observed: 0, gateway: 0, conflict: 0, free: freeRanges.length },
        conflicts: [], generatedAt: 1,
      });
    }),
  );
}

/** A minimal real-state wrapper (mirrors how every actual wizard wires
 * SubnetStep: `useState` + the component's own `onChange`) so typing/click
 * interactions re-render with fresh props exactly like production —
 * SubnetStep is a controlled component, so driving it with plain variable
 * mutation instead of real React state would fight React's own DOM
 * reconciliation on every keystroke. */
function Harness({
  initial,
  zoneType = "simple",
  evpnExitNodeCount,
  onValue,
}: {
  initial: SubnetStepValue;
  zoneType?: SubnetZoneType;
  evpnExitNodeCount?: number;
  onValue?: (v: SubnetStepValue) => void;
}) {
  const [value, setValue] = useState(initial);
  return (
    <SubnetStep
      zoneType={zoneType}
      value={value}
      onChange={(next) => {
        setValue(next);
        onValue?.(next);
      }}
      evpnExitNodeCount={evpnExitNodeCount}
    />
  );
}

function renderStep(props: Parameters<typeof Harness>[0]): ReturnType<typeof render> {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <Harness {...props} />
    </QueryClientProvider>,
  );
}

describe("SubnetStep (T-701)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders nothing beyond the CIDR field until a CIDR is entered", () => {
    stubIpamFetch({});
    renderStep({ initial: emptySubnetStepValue });
    expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /^Gateway/ })).not.toBeInTheDocument();
  });

  it("pre-fills the gateway to the CIDR's first usable address as the user types it", async () => {
    stubIpamFetch({});
    const user = userEvent.setup();
    let last: SubnetStepValue | undefined;
    renderStep({ initial: emptySubnetStepValue, onValue: (v) => { last = v; } });

    await user.type(screen.getByRole("textbox", { name: /^Address range/ }), "10.50.0.0/24");

    expect(last?.cidr).toBe("10.50.0.0/24");
    expect(last?.gateway).toBe("10.50.0.1");
    expect(last?.isolated).toBe(false);
    expect(screen.getByRole("textbox", { name: /^Gateway/ })).toHaveValue("10.50.0.1");
  });

  it("SNAT is disabled with a reason until a gateway is set", () => {
    stubIpamFetch({});
    renderStep({ initial: { cidr: "10.50.0.0/24", gateway: "", isolated: false, snat: false } });
    const snat = screen.getByRole("checkbox", { name: /Enable SNAT/ });
    expect(snat).toBeDisabled();
    expect(screen.getByText(S.common.snatDisabledNoGateway)).toBeInTheDocument();
  });

  it("SNAT is enabled once a gateway is set", () => {
    stubIpamFetch({});
    renderStep({ initial: { cidr: "10.50.0.0/24", gateway: "10.50.0.1", isolated: false, snat: false } });
    expect(screen.getByRole("checkbox", { name: /Enable SNAT/ })).not.toBeDisabled();
  });

  it("choosing 'keep isolated' clears the gateway and snat, and hides both fields", async () => {
    stubIpamFetch({});
    const user = userEvent.setup();
    let last: SubnetStepValue | undefined;
    renderStep({
      initial: { cidr: "10.50.0.0/24", gateway: "10.50.0.1", isolated: false, snat: true },
      onValue: (v) => { last = v; },
    });

    await user.click(screen.getByRole("radio", { name: S.common.gatewayModeIsolated }));

    expect(last).toEqual({ cidr: "10.50.0.0/24", gateway: "", isolated: true, snat: false });
    expect(screen.queryByRole("textbox", { name: /^Gateway/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /Enable SNAT/ })).not.toBeInTheDocument();
  });

  it("switching back to 'has a gateway' re-fills a fresh guess", async () => {
    stubIpamFetch({});
    const user = userEvent.setup();
    let last: SubnetStepValue | undefined;
    renderStep({
      initial: { cidr: "10.50.0.0/24", gateway: "", isolated: true, snat: false },
      onValue: (v) => { last = v; },
    });

    await user.click(screen.getByRole("radio", { name: S.common.gatewayModeHasGateway }));

    expect(last).toEqual({ cidr: "10.50.0.0/24", gateway: "10.50.0.1", isolated: false, snat: false });
  });

  it("shows zone-type-specific gateway copy", () => {
    stubIpamFetch({});
    renderStep({ initial: { cidr: "10.80.0.0/24", gateway: "10.80.0.1", isolated: false, snat: false }, zoneType: "evpn" });
    expect(screen.getByText(S.common.gatewayZoneCopy.evpn)).toBeInTheDocument();
  });

  it("warns when an EVPN subnet has SNAT on but no exit nodes selected", () => {
    stubIpamFetch({});
    renderStep({
      initial: { cidr: "10.80.0.0/24", gateway: "10.80.0.1", isolated: false, snat: true },
      zoneType: "evpn",
      evpnExitNodeCount: 0,
    });
    expect(screen.getByText(S.common.evpnSnatNeedsExitNode)).toBeInTheDocument();
  });

  it("does not warn when an EVPN subnet's SNAT has at least one exit node", () => {
    stubIpamFetch({});
    renderStep({
      initial: { cidr: "10.80.0.0/24", gateway: "10.80.0.1", isolated: false, snat: true },
      zoneType: "evpn",
      evpnExitNodeCount: 1,
    });
    expect(screen.queryByText(S.common.evpnSnatNeedsExitNode)).not.toBeInTheDocument();
  });

  it("refines an auto-filled gateway to the live allocation grid's free address when the CIDR overlaps a known subnet (T-405 shared-component contract)", async () => {
    stubIpamFetch({
      "10.60.0.0/24": [
        { ip: "10.60.0.1", state: "gateway" },
        { ip: "10.60.0.2", state: "allocated" },
        { ip: "10.60.0.3", state: "free" },
      ],
    });
    // Typing the CIDR pre-fills the naive "network + 1" guess immediately
    // (10.60.0.1 — already taken as the gateway record); once the
    // allocation grid resolves for this exact CIDR, the gateway refines to
    // the real first free address, skipping the already-taken gateway/
    // allocated addresses.
    const user = userEvent.setup();
    let last: SubnetStepValue | undefined;
    renderStep({ initial: emptySubnetStepValue, onValue: (v) => { last = v; } });

    await user.type(screen.getByRole("textbox", { name: /^Address range/ }), "10.60.0.0/24");

    await waitFor(() => { expect(last?.gateway).toBe("10.60.0.3"); });
    expect(screen.getByRole("textbox", { name: /^Gateway/ })).toHaveValue("10.60.0.3");
  });

  it("never overwrites a gateway the user typed themselves, even once the grid resolves", async () => {
    // A deliberate, generous response delay so the user's own edit below
    // always lands before the grid query resolves, making the "never
    // clobbers a live edit" race deterministic instead of depending on
    // relative timing between userEvent's own per-keystroke pacing and the
    // stub's fetch.
    stubIpamFetch({ "10.60.0.0/24": [{ ip: "10.60.0.1", state: "gateway" }, { ip: "10.60.0.5", state: "free" }] }, 300);
    const user = userEvent.setup();
    let last: SubnetStepValue | undefined;
    renderStep({ initial: emptySubnetStepValue, onValue: (v) => { last = v; } });

    await user.type(screen.getByRole("textbox", { name: /^Address range/ }), "10.60.0.0/24");
    expect(last?.gateway).toBe("10.60.0.1");

    // The user overwrites the auto-filled guess themselves before the grid
    // query resolves.
    const gatewayField = screen.getByRole("textbox", { name: /^Gateway/ });
    await user.clear(gatewayField);
    await user.type(gatewayField, "10.60.0.99");
    expect(last?.gateway).toBe("10.60.0.99");

    // Give the in-flight grid query time to resolve; it must never clobber
    // the user's own edit.
    await new Promise((resolve) => setTimeout(resolve, 400));
    expect(last?.gateway).toBe("10.60.0.99");
    expect(screen.getByRole("textbox", { name: /^Gateway/ })).toHaveValue("10.60.0.99");
  });
});
