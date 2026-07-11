// TanStack Query hooks for the path simulator. `useSimulateQuery` fires
// `POST /simulate/path` (T-503) as a query, not a mutation: the route is
// documented as read-only ("a read-only static analysis over the
// poll-cached inventory snapshot ... mutates nothing" — docs/api.md), so
// modeling it as a cached, re-fetchable query (keyed on the request body)
// matches its actual semantics and lets the URL-state round trip (AC4)
// and the endpoint pickers both just change `request` and get a cache hit
// or a fresh fetch, same as any other GET-shaped read in this app.
import { useQuery } from "@tanstack/react-query";
import { simulatePath } from "../api/simulate";
import type { SimulateRequest } from "../api/types";

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
