// T-1703's tenant administration: the eight admin routes (docs/api.md
// §"Tenants & self-service").
//
//   GET    /tenants                            list
//   POST   /tenants                            create      {id?, name} -> 201
//   GET    /tenants/{id}                       one, incl. scopes + members
//   DELETE /tenants/{id}                       delete      -> 204
//   PUT    /tenants/{id}/scopes                add scope   {scopeRef} -> 204
//   DELETE /tenants/{id}/scopes?scopeRef=      remove scope           -> 204
//   PUT    /tenants/{id}/members               add/promote {identity, role} -> 204
//   DELETE /tenants/{id}/members/{identity}    remove member          -> 204
//
// There is NO tenant self-service request or approval route. The only
// approval routes in this API are `POST /changesets/{id}/approve` (a tenant
// approver converting a request-changeset to a draft) and
// `POST /changesets/{id}/review/approve` (the two-person rule) — neither
// belongs to tenancy administration.
//
// **`GET /tenants` does not populate `scopes` or `members`.**
// internal/api/tenant.go's handleListTenants emits `Scopes: []string{}` and
// `Members: []` as literals for every row — it never reads either table —
// while `GET /tenants/{id}` reads both. docs/api.md documents the `Tenant`
// shape with both fields and does not say the list omits them, so a client
// that trusts the list renders "no scopes, no members" for a tenant that has
// plenty. That is precisely the absent-rendered-as-definite failure this arc
// keeps finding, so the two responses are modelled as two different types
// here: the list carries only what the list actually answers.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";

/** A tenant member's role. `approver` may convert that tenant's
 * request-changesets to drafts (never their own); `member` may only request.
 * The server refuses any other value with `400 validation_failed`. */
export type TenantRole = "member" | "approver";

export interface TenantMember {
  identity: string;
  role: string;
}

/** One row of `GET /tenants`. Deliberately carries no `scopes`/`members`:
 * the route always answers those as empty arrays without consulting the
 * store, so they are an ABSENT answer wearing an empty one's clothes. Read
 * `GET /tenants/{id}` for either. */
export interface TenantListItem {
  id: string;
  name: string;
  createdBy: string;
  createdAt: number;
}

/** `GET /tenants/{id}` and `POST /tenants`. Here `scopes`/`members` are real:
 * the handler reads both tables. `scopes[]` are inventory Ref strings — a
 * guest or subnet, or a coarse VLAN/VNet expanded to its members live at read
 * time. */
export interface TenantDetail extends TenantListItem {
  scopes: string[];
  members: TenantMember[];
}

/** GET /tenants — `netRead`. */
export async function fetchTenants(): Promise<TenantListItem[]> {
  const body = await apiFetch<{ items?: TenantListItem[] }>("/tenants");
  return body.items ?? [];
}

/** GET /tenants/{id} — `netRead`. `404 not_found` for a tenant that does not
 * exist. */
export function fetchTenant(id: string): Promise<TenantDetail> {
  return apiFetch<TenantDetail>(`/tenants/${encodeURIComponent(id)}`);
}

/** POST /tenants — `netWrite` + CSRF, `201`. `id` defaults to a ULID
 * server-side; `name` is required. */
export function createTenant(name: string, id?: string): Promise<TenantDetail> {
  return apiFetch<TenantDetail>("/tenants", {
    method: "POST",
    json: id === undefined || id === "" ? { name } : { id, name },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /tenants/{id} — `netWrite` + CSRF, `204`. Cascades scopes, members
 * and request linkages. */
export async function deleteTenant(id: string): Promise<void> {
  await apiFetch(`/tenants/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}

/** PUT /tenants/{id}/scopes — `netWrite` + CSRF, `204`. Adds one Ref to what
 * the tenant may see. */
export async function addTenantScope(id: string, scopeRef: string): Promise<void> {
  await apiFetch(`/tenants/${encodeURIComponent(id)}/scopes`, {
    method: "PUT",
    json: { scopeRef },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /tenants/{id}/scopes?scopeRef= — `netWrite` + CSRF, `204`. The ref
 * travels as a QUERY parameter here, not a body, unlike the PUT above. */
export async function removeTenantScope(id: string, scopeRef: string): Promise<void> {
  await apiFetch(
    `/tenants/${encodeURIComponent(id)}/scopes?scopeRef=${encodeURIComponent(scopeRef)}`,
    { method: "DELETE", csrfToken: readCsrfCookie() },
  );
}

/** PUT /tenants/{id}/members — `netWrite` + CSRF, `204`. Also the promote
 * path: writing an existing identity with a different role upserts it. */
export async function putTenantMember(id: string, identity: string, role: TenantRole): Promise<void> {
  await apiFetch(`/tenants/${encodeURIComponent(id)}/members`, {
    method: "PUT",
    json: { identity, role },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /tenants/{id}/members/{identity} — `netWrite` + CSRF, `204`. */
export async function removeTenantMember(id: string, identity: string): Promise<void> {
  await apiFetch(
    `/tenants/${encodeURIComponent(id)}/members/${encodeURIComponent(identity)}`,
    { method: "DELETE", csrfToken: readCsrfCookie() },
  );
}
