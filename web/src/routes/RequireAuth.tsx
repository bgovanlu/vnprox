// SPDX-License-Identifier: Apache-2.0

import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useSession } from "../api/useSession";
import { useDemoSessionStore } from "../store/authStub";
import { EmptyState } from "../components/EmptyState";

/**
 * Route guard for every non-/login route: session bootstrap calls GET
 * /auth/me (T-105's real route) on load, and any non-200 is treated the
 * same way — redirect to /login. The demo-mode flag (src/store/authStub.ts,
 * off unless VITE_AUTH_STUB=true) bypasses this so the shell is demoable
 * with no backend at all; it is client-side only — every real API call
 * still 401s without a session.
 */
export function RequireAuth() {
  const location = useLocation();
  const { data: session, isLoading, isError } = useSession();
  const demoSession = useDemoSessionStore((s) => s.demoSession);

  if (demoSession) {
    return <Outlet />;
  }

  if (isLoading) {
    return (
      <div className="flex h-dvh items-center justify-center">
        <EmptyState title="Loading…" description="Checking your session." />
      </div>
    );
  }

  if (isError || !session) {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }

  return <Outlet />;
}
