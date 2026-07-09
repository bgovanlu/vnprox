// Hand-maintained mirror of docs/api.md's request/response shapes (a
// generation task exists in the P6 backlog — see docs/development.md's
// TypeScript standards section). Keep this file the single source of
// truth for wire types; do not redeclare API shapes elsewhere.
//
// T-005 only implements the Auth surface end-to-end (everything else in
// docs/api.md is a later task's responsibility). The remaining sections
// are stubbed out below so later tasks extend this file instead of
// creating a second, competing types module.

/** The `{"error": {...}}` envelope documented in docs/api.md's conventions
 * section, returned with the matching HTTP status on every non-2xx
 * response. `code` is a stable identifier (`validation_failed`,
 * `pve_denied`, `changeset_locked`, `peer_unreachable`, ...). */
export interface ErrorEnvelope {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

// --- Auth (docs/api.md §Auth) ---------------------------------------------

/** Per-node capability flags returned by GET /auth/me, per docs/api.md. */
export interface Capabilities {
  netRead: boolean;
  netWrite: boolean;
  sdnRead: boolean;
  sdnWrite: boolean;
  fwRead: boolean;
  fwWrite: boolean;
  guestNet: boolean;
  audit: boolean;
}

export interface AuthUser {
  username: string;
  realm: string;
}

/** POST /auth/login request body. `otp` is only present for realms that
 * require a second factor. */
export interface LoginRequest {
  username: string;
  password: string;
  realm: string;
  otp?: string;
}

/** Shared response shape for POST /auth/login and GET /auth/me. */
export interface MeResponse {
  user: AuthUser;
  caps: Record<string, Capabilities>;
}

// --- Everything else in docs/api.md ---------------------------------------
// Topology, changesets, snapshots, firewall/SDN/IPAM read views, the path
// simulator, metrics, blueprints, and the peer API all have routes defined
// in docs/api.md but no frontend consumer yet — their request/response
// types land with the task that first calls them (T-1xx / T-2xx). Add
// them here, not in a parallel file.
