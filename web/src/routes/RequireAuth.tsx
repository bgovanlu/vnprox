import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useSession } from "../api/useSession";
import { useDemoSessionStore } from "../store/authStub";
import { EmptyState } from "../components/EmptyState";

/**
 * Route guard for every non-/login route. Per T-005's task card: session
 * bootstrap calls GET /auth/me on load, and any non-200 (401
 * not-logged-in, or today, a 404 against T-002's stub before T-105 lands
 * the real route) is treated the same way — redirect to /login. The
 * demo-mode flag (src/store/authStub.ts) bypasses this so the shell is
 * demoable without a working backend auth system.
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
