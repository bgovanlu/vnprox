// T-3003 AC4, the card's single most important assertion: `doctor --live`
// renders pass / fail / skip as three distinct states, and a skipped check is
// neither styled nor counted as a passing one.
//
// The rule comes from `vnproxctl verify`, which exits non-zero on an
// all-skipped run and prints "A skipped check is not a passing one" — because
// a wall of skips under a "0 failed" footer reads as success. Two of this
// route's four checks still skip by design (`T-2406-followup-01`/`-02`), so
// this is the normal case on a real deployment, not an edge case.
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { DoctorResult } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { DoctorLiveSection } from "./DoctorLiveSection";

const fetchDoctorLive = vi.fn<() => Promise<DoctorResult[]>>();

vi.mock("../api/doctor", async (importOriginal) => {
  // asDoctorStatus is real narrowing logic the component depends on; only the
  // network call is substituted.
  const actual = await importOriginal<typeof import("../api/doctor")>();
  return { ...actual, fetchDoctorLive: () => fetchDoctorLive() };
});

/** The shape a real deployment returns today: two answered, two skipped by
 * design. */
const REALISTIC: DoctorResult[] = [
  { check: "pve_reachable", status: "pass", detail: "PVE API reachable and the token authenticates" },
  { check: "pve_privileges", status: "pass", detail: "the PVE token holds every privilege vnprox uses" },
  { check: "peer_secret", status: "skip", detail: "could not collect peer secret digests: no peer client" },
  { check: "clock_skew", status: "skip", detail: "PVE did not report a server time" },
];

function renderSection(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <DoctorLiveSection />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchDoctorLive.mockReset();
});

describe("pass / warn / fail / skip are four distinct states", () => {
  it("gives each status its own data-status, label and styling", async () => {
    fetchDoctorLive.mockResolvedValue([
      { check: "a_pass", status: "pass", detail: "healthy" },
      { check: "a_warn", status: "warn", detail: "degraded", remediation: "grant Sys.Console" },
      { check: "a_fail", status: "fail", detail: "broken", remediation: "check the token" },
      { check: "a_skip", status: "skip", detail: "not checked: no peer client" },
    ]);
    renderSection();

    const pass = await screen.findByTestId("doctor-check-a_pass");
    const warn = screen.getByTestId("doctor-check-a_warn");
    const fail = screen.getByTestId("doctor-check-a_fail");
    const skip = screen.getByTestId("doctor-check-a_skip");

    expect(pass).toHaveAttribute("data-status", "pass");
    expect(warn).toHaveAttribute("data-status", "warn");
    expect(fail).toHaveAttribute("data-status", "fail");
    expect(skip).toHaveAttribute("data-status", "skip");

    // Four distinct words, so the state survives a screen reader and a
    // colour-blind reader alike.
    const labels = ["a_pass", "a_warn", "a_fail", "a_skip"].map(
      (c) => screen.getByTestId(`doctor-status-${c}`).textContent,
    );
    expect(new Set(labels).size).toBe(4);
    expect(labels).toEqual(["PASS", "WARN", "FAIL", "SKIPPED"]);
  });

  it("does NOT style a skipped check the way it styles a passing one", async () => {
    fetchDoctorLive.mockResolvedValue([
      { check: "a_pass", status: "pass", detail: "healthy" },
      { check: "a_skip", status: "skip", detail: "not checked: no peer client" },
    ]);
    renderSection();

    const passChip = await screen.findByTestId("doctor-status-a_pass");
    const skipChip = screen.getByTestId("doctor-status-a_skip");
    expect(skipChip.className).not.toBe(passChip.className);
    // and specifically not the success colour family.
    expect(passChip.className).toContain("emerald");
    expect(skipChip.className).not.toContain("emerald");

    // The card carries the statement too, not just the colour.
    expect(screen.getByTestId("doctor-check-a_skip")).toHaveTextContent("NOT checked");
    expect(screen.getByTestId("doctor-check-a_skip")).toHaveTextContent("This is not a pass");
  });

  it("does not count a skip as a pass", async () => {
    fetchDoctorLive.mockResolvedValue(REALISTIC);
    renderSection();

    expect(await screen.findByTestId("doctor-counts")).toHaveTextContent(
      "2 passed, 0 warned, 0 failed, 2 skipped",
    );
    expect(screen.getByTestId("doctor-verdict")).toHaveTextContent("A skipped check is not a passing one");
  });

  it("refuses to call an all-skipped run clean", async () => {
    fetchDoctorLive.mockResolvedValue(
      ["pve_reachable", "pve_privileges", "peer_secret", "clock_skew"].map((check) => ({
        check,
        status: "skip",
        detail: "not checked: the vnprox daemon could not be reached",
      })),
    );
    renderSection();

    expect(await screen.findByTestId("doctor-counts")).toHaveTextContent("0 passed");
    const verdict = screen.getByTestId("doctor-verdict");
    expect(verdict).toHaveTextContent("Nothing was checked");
    expect(verdict).toHaveTextContent("A skipped check is not a passing one");
  });

  it("renders an unrecognised status as an explicit unknown, never as a pass", async () => {
    fetchDoctorLive.mockResolvedValue([{ check: "a_new", status: "inconclusive", detail: "?" }]);
    renderSection();

    const card = await screen.findByTestId("doctor-check-a_new");
    expect(card).toHaveAttribute("data-status", "unknown");
    expect(screen.getByTestId("doctor-status-a_new")).toHaveTextContent("UNRECOGNISED");
    expect(screen.getByTestId("doctor-counts")).toHaveTextContent("0 passed");
    expect(screen.getByTestId("doctor-counts")).toHaveTextContent("1 unrecognised");
  });
});

describe("scope and refusals", () => {
  it("says these are the daemon-credentialed checks, not the whole doctor suite", async () => {
    fetchDoctorLive.mockResolvedValue(REALISTIC);
    renderSection();

    await screen.findByTestId("doctor-results");
    // The route returns internal/doctor.LiveChecks — four, not ten.
    expect(screen.getByTestId("doctor-results").children).toHaveLength(4);
    expect(screen.getByText(/four checks that need a credential/i)).toBeInTheDocument();
  });

  it("renders a 403 as 'not allowed to look', never as a failing run", async () => {
    fetchDoctorLive.mockRejectedValue(new ApiError(403, "forbidden", "missing capability: audit"));
    renderSection();

    const notice = await screen.findByTestId("doctor-error");
    expect(notice).toHaveAttribute("data-refusal-kind", "forbidden");
    expect(notice).toHaveTextContent("audit");
    expect(screen.queryByTestId("doctor-counts")).toBeNull();
    expect(screen.queryByTestId("doctor-verdict")).toBeNull();
  });

  it("renders a 404 as 'not mounted here', distinct from a refusal", async () => {
    fetchDoctorLive.mockRejectedValue(new ApiError(404, "not_found", "no such API route"));
    renderSection();

    expect(await screen.findByTestId("doctor-error")).toHaveAttribute("data-refusal-kind", "unavailable");
  });
});
