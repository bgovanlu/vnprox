// Phase 36. The rule is "report what the cluster said, verbatim" — with one
// exception, and this pins both halves of it.
import { describe, expect, it } from "vitest";
import { actionErrorMessage } from "./actionError";
import { ApiError } from "./client";

describe("actionErrorMessage", () => {
  it("rewrites a stale CSRF failure into something an operator can act on", () => {
    // Observed on the deployed instance from a tab left open across a
    // daemon restart. "missing or invalid X-VNPROX-CSRF header" names an
    // HTTP header at somebody trying to start a DHCP server: accurate,
    // and useless.
    const err = new ApiError(403, "csrf_required", "missing or invalid X-VNPROX-CSRF header");
    expect(actionErrorMessage(err, "fallback")).toMatch(/session has expired/i);
    expect(actionErrorMessage(err, "fallback")).not.toMatch(/CSRF/);
  });

  it("rewrites an expired session the same way", () => {
    const err = new ApiError(401, "not_authenticated", "not logged in");
    expect(actionErrorMessage(err, "fallback")).toMatch(/reload the page/i);
  });

  it("passes systemd's own message through untouched", () => {
    // The whole reason these buttons report verbatim: this sentence tells
    // the operator exactly what to do next, and any summary of it would be
    // worse.
    const err = new ApiError(502, "service_start_failed", "systemctl start frr: exit status 1: Unit frr.service is masked.");
    expect(actionErrorMessage(err, "fallback")).toBe(
      "systemctl start frr: exit status 1: Unit frr.service is masked.",
    );
  });

  it("passes a collector's poll error through untouched", () => {
    const err = new Error("host links (pve001): context canceled");
    expect(actionErrorMessage(err, "fallback")).toBe("host links (pve001): context canceled");
  });

  it("does not rewrite a 403 that is not a CSRF failure", () => {
    // A capability refusal is about the cluster's rules, not the tab's age.
    // Telling that operator to reload would send them round a loop.
    const err = new ApiError(403, "forbidden", "netWrite required");
    expect(actionErrorMessage(err, "fallback")).toBe("netWrite required");
  });

  it("falls back when there is no message at all", () => {
    expect(actionErrorMessage(undefined, "could not start the service")).toBe("could not start the service");
    expect(actionErrorMessage(new Error(""), "could not start the service")).toBe("could not start the service");
  });
});
