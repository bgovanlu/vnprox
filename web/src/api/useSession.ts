// Session bootstrap: calls GET /auth/me (T-105's real route) on load via
// TanStack Query. Any non-200 (401 not-logged-in, or a 404 from an
// out-of-date backend) is treated uniformly as "logged out" — retry:false
// so an error doesn't spin forever, and callers branch on isError rather
// than inspecting status.
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
