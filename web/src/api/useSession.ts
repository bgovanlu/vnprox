// Session bootstrap: calls GET /auth/me on load via TanStack Query.
// Per T-005's task card, any non-200 (401 not-logged-in today, or a 404
// before T-105 wires up the real route on top of T-002's stub) is
// treated uniformly as "logged out" — retry:false so a 404 doesn't spin
// forever, and callers branch on isError rather than inspecting status.
import { useQuery } from "@tanstack/react-query";
import { getMe } from "./auth";
import type { MeResponse } from "./types";

export const SESSION_QUERY_KEY = ["auth", "me"] as const;

export function useSession() {
  return useQuery<MeResponse>({
    queryKey: SESSION_QUERY_KEY,
    queryFn: getMe,
    retry: false,
    staleTime: 60_000,
  });
}
