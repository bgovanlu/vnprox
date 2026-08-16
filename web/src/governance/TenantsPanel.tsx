// T-3002: tenant administration — the eight admin routes as a management
// screen (T-1703).
//
// Two properties this panel holds deliberately, both of them about what it
// does NOT do:
//
//   1. **It renders no ref it was not given.** Every resource ref on screen
//      comes from `GET /tenants/{id}`'s own `scopes` array. There is no guest
//      picker, no inventory read, no "add everything on this node" — adding a
//      scope is a typed Ref. An admin screen that enumerated the cluster's
//      guests for you would be an inventory read this screen has no business
//      making, and for a tenant-scoped session it would be the exact leak
//      T-1703's server-side scoping exists to prevent.
//   2. **It concludes nothing about scopes or members from the list route.**
//      `GET /tenants` answers `scopes: []` and `members: []` as literals for
//      every row without reading either table (internal/api/tenant.go's
//      handleListTenants). Those are absent answers, not empty ones, so
//      api/tenants.ts models the list row without either field and this panel
//      says "select a tenant to read them" rather than "none".
//
// See the card report for the third property this panel CANNOT hold: neither
// admin route applies tenant scoping, and both are `netRead`-gated, so a
// tenant member can read every tenant's scope list from the API regardless of
// what any UI does. That is a daemon-side gap, recorded rather than papered
// over here.
import { useState } from "react";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { ApiError } from "../api/client";
import type { TenantRole } from "../api/tenants";
import {
  useAddScopeMutation,
  useCreateTenantMutation,
  useDeleteTenantMutation,
  usePutMemberMutation,
  useRemoveMemberMutation,
  useRemoveScopeMutation,
  useTenantQuery,
  useTenantsQuery,
} from "./queries";

const ROLE_NOTE: Record<TenantRole, string> = {
  member: "may request changes within the tenant's scope; may not approve",
  approver: "may convert this tenant's request-changesets to drafts — never their own",
};

function classifyRole(raw: string): TenantRole | undefined {
  return raw === "member" || raw === "approver" ? raw : undefined;
}

export function TenantsPanel() {
  const tenantsQuery = useTenantsQuery();
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const detail = useTenantQuery(selectedId);

  const createTenant = useCreateTenantMutation();
  const deleteTenant = useDeleteTenantMutation();
  const addScope = useAddScopeMutation();
  const removeScope = useRemoveScopeMutation();
  const putMember = usePutMemberMutation();
  const removeMember = useRemoveMemberMutation();

  const [newName, setNewName] = useState("");
  const [newScope, setNewScope] = useState("");
  const [newIdentity, setNewIdentity] = useState("");
  const [newRole, setNewRole] = useState<TenantRole>("member");

  const tenants = tenantsQuery.data ?? [];
  const notFound = detail.error instanceof ApiError && detail.error.status === 404;

  return (
    <section aria-label="Tenants" data-testid="tenants-panel" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold">Tenants</h2>
        <HelpAnchor topic="tenants-panel" />
      </div>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        Delegated, server-side-scoped views: a tenant's members see only the resources listed in its scope, and can
        request changes that route to one of its approvers. Scoping is enforced in the daemon at the data-access layer
        — it only ever narrows a member's view, and it adds no mutation path around the change engine.
      </p>
      <p className="text-xs text-slate-500 dark:text-slate-400">
        There is no tenant self-service request or approval route in this API. A member requests a change by staging a
        changeset against the tenant; an approver converts it to an ordinary draft from the changeset itself, and then
        drives the usual review flow. Nothing on this screen approves anything.
      </p>

      {tenantsQuery.isLoading && <p className="text-sm text-slate-500 dark:text-slate-400">Reading tenants…</p>}
      {tenantsQuery.error !== null && (
        <p className="text-sm text-slate-700 dark:text-slate-200" role="status">
          The tenant list could not be read, so which tenants exist is unknown. The daemon said:{" "}
          {tenantsQuery.error instanceof Error ? tenantsQuery.error.message : "the read failed"}
        </p>
      )}

      <div className="grid gap-4 sm:grid-cols-[minmax(0,18rem)_1fr]">
        <div className="flex flex-col gap-2">
          {tenantsQuery.data !== undefined && tenants.length === 0 && (
            <p className="text-sm text-slate-500 dark:text-slate-400">No tenants are defined.</p>
          )}
          <ul className="flex flex-col gap-1" data-testid="tenant-list">
            {tenants.map((t) => (
              <li key={t.id}>
                <button
                  type="button"
                  onClick={() => {
                    setSelectedId(t.id);
                  }}
                  aria-pressed={selectedId === t.id}
                  className={
                    selectedId === t.id
                      ? "w-full rounded-md border border-accent-600 px-2 py-1 text-left text-sm"
                      : "w-full rounded-md border border-slate-200 px-2 py-1 text-left text-sm dark:border-slate-800"
                  }
                >
                  <span className="font-medium">{t.name}</span>
                  <span className="block font-mono text-xs text-slate-500 dark:text-slate-400">{t.id}</span>
                </button>
              </li>
            ))}
          </ul>

          <div className="rounded-md border border-slate-200 p-2 dark:border-slate-800">
            <label className="flex flex-col gap-1 text-xs">
              <span className="font-medium">New tenant name</span>
              <input
                value={newName}
                onChange={(e) => {
                  setNewName(e.target.value);
                }}
                aria-label="New tenant name"
                className="rounded border border-slate-300 px-1.5 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
              />
            </label>
            <Button
              variant="primary"
              size="sm"
              className="mt-2"
              disabled={newName.trim() === "" || createTenant.isPending}
              onClick={() => {
                createTenant.mutate(
                  { name: newName.trim() },
                  {
                    onSuccess: (created) => {
                      setNewName("");
                      setSelectedId(created.id);
                    },
                  },
                );
              }}
            >
              Create tenant
            </Button>
            {createTenant.error !== null && (
              <p className="mt-1 text-xs text-red-700 dark:text-red-300" role="alert">
                {createTenant.error.message}
              </p>
            )}
          </div>
        </div>

        <div className="flex flex-col gap-3">
          {selectedId === undefined && (
            <p className="text-sm text-slate-500 dark:text-slate-400">
              Select a tenant to read its scopes and members. The list above carries neither: the list route reports
              both as empty without consulting the store, so nothing may be concluded from it either way.
            </p>
          )}

          {selectedId !== undefined && detail.isLoading && (
            <p className="text-sm text-slate-500 dark:text-slate-400">Reading the tenant…</p>
          )}

          {notFound && (
            <p className="text-sm text-slate-700 dark:text-slate-200" role="status" data-testid="tenant-not-found">
              No such tenant. An out-of-scope object is reported as not found rather than refused, so this answer does
              not distinguish "it does not exist" from "it is not yours" — by design.
            </p>
          )}

          {detail.error !== null && !notFound && (
            <p className="text-sm text-slate-700 dark:text-slate-200" role="status">
              The tenant could not be read, so its scopes and members are unknown. The daemon said:{" "}
              {detail.error instanceof Error ? detail.error.message : "the read failed"}
            </p>
          )}

          {detail.data !== undefined && (
            <>
              <div>
                <h3 className="text-sm font-semibold">{detail.data.name}</h3>
                <p className="font-mono text-xs text-slate-500 dark:text-slate-400">{detail.data.id}</p>
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  Created by {detail.data.createdBy === "" ? "an unrecorded principal" : detail.data.createdBy}
                  {detail.data.createdAt === 0
                    ? " at an unrecorded time"
                    : ` on ${new Date(detail.data.createdAt * 1000).toLocaleString()}`}
                </p>
              </div>

              <div data-testid="tenant-scopes">
                <h4 className="text-sm font-medium">Visible resources</h4>
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  Inventory Ref strings. A coarse ref (a VLAN or VNet) is expanded to its members live at read time, so
                  this list is what was declared, not necessarily its full expansion.
                </p>
                {detail.data.scopes.length === 0 ? (
                  <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
                    This tenant has no scope, so its members see nothing.
                  </p>
                ) : (
                  <ul className="mt-1 flex flex-col gap-1">
                    {detail.data.scopes.map((ref) => (
                      <li key={ref} className="flex items-center gap-2 text-sm">
                        <span className="font-mono">{ref}</span>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={removeScope.isPending}
                          onClick={() => {
                            removeScope.mutate({ id: detail.data.id, scopeRef: ref });
                          }}
                        >
                          Remove
                        </Button>
                      </li>
                    ))}
                  </ul>
                )}
                <div className="mt-2 flex items-end gap-2">
                  <label className="flex flex-col gap-1 text-xs">
                    <span className="font-medium">Add a Ref</span>
                    <input
                      value={newScope}
                      onChange={(e) => {
                        setNewScope(e.target.value);
                      }}
                      placeholder="guest:pve1:101"
                      aria-label="Scope ref"
                      className="w-64 rounded border border-slate-300 px-1.5 py-1 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
                    />
                  </label>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={newScope.trim() === "" || addScope.isPending}
                    onClick={() => {
                      addScope.mutate(
                        { id: detail.data.id, scopeRef: newScope.trim() },
                        {
                          onSuccess: () => {
                            setNewScope("");
                          },
                        },
                      );
                    }}
                  >
                    Add scope
                  </Button>
                </div>
                {addScope.error !== null && (
                  <p className="mt-1 text-xs text-red-700 dark:text-red-300" role="alert">
                    {addScope.error.message}
                  </p>
                )}
              </div>

              <div data-testid="tenant-members">
                <h4 className="text-sm font-medium">Members</h4>
                {detail.data.members.length === 0 ? (
                  <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
                    This tenant has no members, so nobody reads through its scope.
                  </p>
                ) : (
                  <ul className="mt-1 flex flex-col gap-1">
                    {detail.data.members.map((m) => {
                      const role = classifyRole(m.role);
                      return (
                        <li key={m.identity} className="flex items-center gap-2 text-sm">
                          <span className="font-mono">{m.identity}</span>
                          <span className="text-xs text-slate-500 dark:text-slate-400">
                            {role === undefined
                              ? `unrecognised role "${m.role}" — this build cannot say what it permits`
                              : `${role}: ${ROLE_NOTE[role]}`}
                          </span>
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={removeMember.isPending}
                            onClick={() => {
                              removeMember.mutate({ id: detail.data.id, identity: m.identity });
                            }}
                          >
                            Remove
                          </Button>
                        </li>
                      );
                    })}
                  </ul>
                )}
                <div className="mt-2 flex items-end gap-2">
                  <label className="flex flex-col gap-1 text-xs">
                    <span className="font-medium">Identity</span>
                    <input
                      value={newIdentity}
                      onChange={(e) => {
                        setNewIdentity(e.target.value);
                      }}
                      placeholder="alice@pve"
                      aria-label="Member identity"
                      className="w-48 rounded border border-slate-300 px-1.5 py-1 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
                    />
                  </label>
                  <label className="flex flex-col gap-1 text-xs">
                    <span className="font-medium">Role</span>
                    <select
                      value={newRole}
                      aria-label="Member role"
                      onChange={(e) => {
                        setNewRole(classifyRole(e.target.value) ?? "member");
                      }}
                      className="rounded border border-slate-300 px-1.5 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
                    >
                      <option value="member">member</option>
                      <option value="approver">approver</option>
                    </select>
                  </label>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={newIdentity.trim() === "" || putMember.isPending}
                    onClick={() => {
                      putMember.mutate(
                        { id: detail.data.id, identity: newIdentity.trim(), role: newRole },
                        {
                          onSuccess: () => {
                            setNewIdentity("");
                          },
                        },
                      );
                    }}
                  >
                    Add or promote
                  </Button>
                </div>
                {putMember.error !== null && (
                  <p className="mt-1 text-xs text-red-700 dark:text-red-300" role="alert">
                    {putMember.error.message}
                  </p>
                )}
              </div>

              <div className="rounded-md border border-red-300 p-2 dark:border-red-700">
                <p className="text-xs">
                  Deleting a tenant cascades its scopes, its members and its request linkages. Its members lose their
                  scoped view immediately and read unscoped afterwards, exactly as a non-member does.
                </p>
                <Button
                  variant="destructive"
                  size="sm"
                  className="mt-2"
                  disabled={deleteTenant.isPending}
                  onClick={() => {
                    deleteTenant.mutate(detail.data.id, {
                      onSuccess: () => {
                        setSelectedId(undefined);
                      },
                    });
                  }}
                >
                  Delete {detail.data.name}
                </Button>
              </div>
            </>
          )}
        </div>
      </div>
    </section>
  );
}
