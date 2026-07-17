// TanStack Query hooks for the path simulator. `useSimulateQuery` fires
// `POST /simulate/path` (T-503) as a query, not a mutation: the route is
// documented as read-only ("a read-only static analysis over the
// poll-cached inventory snapshot ... mutates nothing" — docs/api.md), so
// modeling it as a cached, re-fetchable query (keyed on the request body)
// matches its actual semantics and lets the URL-state round trip (AC4)
// and the endpoint pickers both just change `request` and get a cache hit
// or a fresh fetch, same as any other GET-shaped read in this app.
import { useMutation, useQuery } from "@tanstack/react-query";
import { simulatePath, simulateVerify, simulateVerifyEligibility } from "../api/simulate";
import type { SimEndpointSpec, SimulateRequest } from "../api/types";

export function useSimulateQuery(request: SimulateRequest | undefined) {
  return useQuery({
    queryKey: ["simulate", "path", request],
    // `enabled` below guarantees TanStack Query never actually calls this
    // queryFn while `request` is undefined; the explicit check (rather than
    // a non-null assertion) keeps this file honest without one.
    queryFn: () => {
      if (!request) {
        return Promise.reject(new Error("useSimulateQuery: queryFn invoked with no request"));
      }
      return simulatePath(request);
    },
    enabled: request !== undefined,
    // A simulation over a given request never changes without new
    // inventory data arriving (no live packets involved) — avoid refiring
    // on every window refocus like a live-data view would.
    staleTime: 15_000,
    retry: false,
  });
}

// T-806: "Verify live" gating (GET /simulate/verify/eligibility) — only
// ever fetched for a guest-nic src (an ip/external src is never eligible,
// and internal/api's own route 400s anything else, so there is nothing
// useful to ask it). A short staleTime keeps re-picking the same source
// within a few seconds from re-hitting the live guest-agent ping every
// keystroke/render, while still reflecting a guest that came back up or
// went down across a longer session.
export function useVerifyEligibilityQuery(src: SimEndpointSpec | undefined) {
  const ref = src?.kind === "guest-nic" ? src.ref : undefined;
  return useQuery({
    queryKey: ["simulate", "verify-eligibility", ref],
    queryFn: () => {
      if (!ref) {
        return Promise.reject(new Error("useVerifyEligibilityQuery: queryFn invoked with no guest-nic ref"));
      }
      return simulateVerifyEligibility(ref);
    },
    enabled: ref !== undefined,
    staleTime: 10_000,
    retry: false,
  });
}

// T-806: the "Verify live" action itself — a mutation, not a query, since
// it is a user-triggered diagnostic action with a real side effect (it
// execs a command inside the source guest and is audited server-side as
// probe.verify), never fired automatically the way useSimulateQuery's
// static analysis is.
export function useVerifyMutation() {
  return useMutation({
    mutationFn: (request: SimulateRequest) => simulateVerify(request),
  });
}
