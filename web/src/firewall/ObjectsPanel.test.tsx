import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import * as changesetsApi from "../api/changesets";
import { ToastProvider } from "../components/Toast";
import { ObjectsPanel } from "./ObjectsPanel";
import type { Changeset, FirewallObjectsResponse, MeResponse } from "../api/types";
import type { FwRulesetLocation } from "./refs";

vi.mock("../api/changesets", () => ({
  listChangesets: vi.fn(() => Promise.resolve([])),
  getChangeset: vi.fn(),
  createChangeset: vi.fn(),
  updateChangeset: vi.fn(),
}));

// T-607: ObjectsPanel now calls useSession() (its Delete button's fwWrite
// capability gate — see the doc comment on that gate in ObjectsPanel.tsx);
// this file mocks module calls directly rather than global fetch (see
// the ../api/changesets mock above), so useSession's underlying GET
// /auth/me is mocked the same way — full caps, since this file isn't
// testing the read-only case (readonly-crawl.spec.ts covers that).
const fullCapsMe: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};
vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: fullCapsMe, isLoading: false, isError: false }),
}));

function draftChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return { id: "cs1", title: "t", author: "root@pam", status: "draft", ops: [], findings: [], createdAt: 0, updatedAt: 0, ...overrides };
}

/** noUncheckedIndexedAccess makes every array index read `T | undefined`;
 * see ResolvedViewTable.test.tsx's identical helper doc comment. */
function at<T>(arr: T[], i: number): T {
  const v = arr[i];
  if (v === undefined) {
    throw new Error(`expected an element at index ${String(i)}, got undefined`);
  }
  return v;
}

// ObjectsPanel's rows now stage fw.*.delete ops (T-502's usage-guarded
// delete), which needs useDrawerActions' QueryClient and useToast's
// ToastProvider — the same wrapping every other op-staging component's
// tests use (e.g. changesets/CountdownBanner.test.tsx).
function renderPanel(
  objects: FirewallObjectsResponse,
  onNavigate?: (loc: FwRulesetLocation, pos: number) => void,
  onInspectGroup?: (name: string) => void,
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ObjectsPanel objects={objects} onNavigate={onNavigate} onInspectGroup={onInspectGroup} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

const objects: FirewallObjectsResponse = {
  aliases: [
    {
      kind: "alias", scope: "cluster", name: "office_net", comment: "office/management subnet", count: 9,
      referencedBy: [
        { scope: "cluster", ref: "fw-ruleset::cluster", pos: 0 },
        { scope: "guest", ref: "fw-ruleset:pve1:guest/qemu/100", pos: 2 },
      ],
    },
    { kind: "alias", scope: "guest", name: "unused_alias", count: 0 },
  ],
  ipsets: [
    { kind: "ipset", scope: "cluster", name: "blocklist", count: 1, referencedBy: [{ scope: "cluster", ref: "fw-ruleset::cluster", pos: 1 }] },
  ],
  groups: [
    { kind: "group", scope: "cluster", name: "webservers", count: 2 },
  ],
  macros: [
    { name: "HTTP", comment: "Web traffic (HTTP)", ports: [{ proto: "tcp", dport: "80" }] },
  ],
};

describe("ObjectsPanel", () => {
  it("shows the referenced-by-N-rules count for each object", () => {
    renderPanel(objects);
    expect(screen.getByText("referenced by 9 rules — view")).toBeInTheDocument();
    expect(screen.getByText("referenced by 1 rule — view")).toBeInTheDocument();
    expect(screen.getByText("referenced by 2 rules — view")).toBeInTheDocument();
  });

  it("labels a zero-reference object honestly instead of a misleading count", () => {
    renderPanel(objects);
    expect(screen.getByText("not referenced")).toBeInTheDocument();
  });

  it("expands the reference list on click (the 'view' deep-link)", async () => {
    const user = userEvent.setup();
    renderPanel(objects);
    await user.click(screen.getByText("referenced by 9 rules — view"));
    expect(screen.getByText(/fw-ruleset::cluster/)).toBeInTheDocument();
    expect(screen.getByText(/fw-ruleset:pve1:guest\/qemu\/100/)).toBeInTheDocument();
  });

  it("renders the macro catalog with its expansion preview", () => {
    renderPanel(objects);
    expect(screen.getByText("HTTP → tcp/80")).toBeInTheDocument();
  });
});

// Acceptance criterion 2: deleting a referenced alias is blocked; deleting
// an unreferenced one stages the delete op; the reference list's entries
// deep-link back to the referencing rule's scope.
describe("ObjectsPanel usage-guarded delete (acceptance criterion 2)", () => {
  it("blocks deleting an alias referenced by rules — no changeset op is staged", async () => {
    const user = userEvent.setup();
    renderPanel(objects);
    const deleteButtons = screen.getAllByText("Delete");
    // office_net (cluster scope, count 9) is the first cluster-scope alias row.
    await user.click(at(deleteButtons, 0));
    expect(await screen.findByText(/referenced by 9 rules and cannot be deleted/)).toBeInTheDocument();
    expect(changesetsApi.createChangeset).not.toHaveBeenCalled();
  });

  it("stages a delete op for an unreferenced cluster-scope object", async () => {
    vi.mocked(changesetsApi.createChangeset).mockResolvedValue(
      draftChangeset({ ops: [{ op: "fw.ipset.delete", target: "fw-ruleset::cluster", params: { name: "blocklist" } }] }),
    );
    const user = userEvent.setup();
    const unreferenced: FirewallObjectsResponse = {
      aliases: [],
      groups: [],
      macros: [],
      ipsets: [{ kind: "ipset", scope: "cluster", name: "blocklist", count: 0 }],
    };
    renderPanel(unreferenced);
    const deleteButtons = screen.getAllByText("Delete");
    await user.click(at(deleteButtons, 0));
    expect(await screen.findByText(/staged for deletion/)).toBeInTheDocument();
    expect(changesetsApi.createChangeset).toHaveBeenCalledWith({
      title: "Delete ipset blocklist",
      ops: [{ op: "fw.ipset.delete", target: "fw-ruleset::cluster", params: { name: "blocklist" } }],
    });
  });

  it("deep-links a reference entry to its scope/node/guest location", async () => {
    const user = userEvent.setup();
    const onNavigate = vi.fn();
    renderPanel(objects, onNavigate);
    await user.click(screen.getByText("referenced by 9 rules — view"));
    await user.click(screen.getByText(/fw-ruleset:pve1:guest\/qemu\/100/));
    expect(onNavigate).toHaveBeenCalledWith({ scope: "guest", node: "pve1", guestRef: "guest:pve1:100" }, 2);
  });

  it("does not offer a delete button for non-cluster-scope objects", () => {
    renderPanel(objects);
    // unused_alias is guest-scope; its row has no Delete affordance.
    const rows = screen.getAllByRole("row");
    const unusedRow = rows.find((r) => r.textContent.includes("unused_alias"));
    expect(unusedRow?.textContent ?? "").not.toContain("Delete");
  });
});

// T-2002: the security-group inspector's launch point.
describe("ObjectsPanel security-group Inspect action", () => {
  it("offers Inspect only for the groups table, and calls onInspectGroup with the group's name", async () => {
    const user = userEvent.setup();
    const onInspectGroup = vi.fn();
    renderPanel(objects, undefined, onInspectGroup);

    const inspectButtons = screen.getAllByText("Inspect");
    expect(inspectButtons).toHaveLength(1); // only the one group row, not aliases/ipsets
    await user.click(at(inspectButtons, 0));
    expect(onInspectGroup).toHaveBeenCalledWith("webservers");
  });

  it("renders no Inspect action when onInspectGroup is omitted", () => {
    renderPanel(objects);
    expect(screen.queryByText("Inspect")).not.toBeInTheDocument();
  });
});
