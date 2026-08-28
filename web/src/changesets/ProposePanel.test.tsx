// SPDX-License-Identifier: Apache-2.0

// T-2702's client: proposing a changeset as a pull request against the spec
// repository. The claims this file pins:
//
//   * a successful propose reports whether a NEW pull request opened (201,
//     `created: true`) or an EXISTING one was updated (200, `created:
//     false`) — never the same toast for both;
//   * with no `[gitsync]` repository configured at all, the affordance
//     degrades honestly (no button that would only fail) rather than
//     erroring;
//   * a `501 not_implemented` from an attempt (repository configured, but no
//     push credential) surfaces the daemon's own message, not a generic one;
//   * a `422 nothing_to_propose` refusal is shown the same honest way;
//   * an existing proposal is shown on revisit via `GET .../proposal`.
//
// Mirrors drift/ConfigAsCodePage.test.tsx's own mocking shape for the same
// `../api/gitsync` and `../api/changesets` boundary.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { GitSyncStatus, SpecProposal } from "../api/gitsync";
import { ApiError } from "../api/client";
import { ToastProvider } from "../components/Toast";
import { ProposePanel } from "./ProposePanel";

const fetchGitSyncStatus = vi.fn<() => Promise<GitSyncStatus>>();
const proposeChangeset = vi.fn<(id: string) => Promise<SpecProposal>>();
const fetchChangesetProposal = vi.fn<(id: string) => Promise<SpecProposal | null>>();

vi.mock("../api/gitsync", async () => {
  const actual = await vi.importActual<typeof import("../api/gitsync")>("../api/gitsync");
  return { ...actual, fetchGitSyncStatus: () => fetchGitSyncStatus() };
});

vi.mock("../api/changesets", async () => {
  const actual = await vi.importActual<typeof import("../api/changesets")>("../api/changesets");
  return {
    ...actual,
    proposeChangeset: (id: string) => proposeChangeset(id),
    fetchChangesetProposal: (id: string) => fetchChangesetProposal(id),
  };
});

const enabledStatus: GitSyncStatus = {
  enabled: true,
  remote: "https://github.com/org/infra (github)",
  requireSignedCommits: false,
  planOpCount: 0,
};

function renderPanel(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <ProposePanel changesetId="cs-1" />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("ProposePanel (T-2702)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchChangesetProposal.mockResolvedValue(null);
  });

  it("opens a confirmation, then reports a NEW pull request as opened (201, created: true)", async () => {
    const user = userEvent.setup();
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    proposeChangeset.mockResolvedValue({
      changesetId: "cs-1",
      remote: "https://github.com/org/infra (github)",
      branch: "vnprox/changeset-cs-1",
      path: "network/cluster.yaml",
      pullRequestId: "42",
      pullRequestUrl: "https://github.com/org/infra/pull/42",
      created: true,
    });

    renderPanel();

    const openButton = await screen.findByRole("button", { name: "Propose as pull request…" });
    await user.click(openButton);

    // The dialog's accessible name comes from its DialogTitle
    // (aria-labelledby wins over the DialogContent's own aria-label), so
    // that's what's queried against here.
    const dialog = screen.getByRole("dialog", { name: "Propose this changeset as a pull request?" });
    expect(dialog).toHaveTextContent("The cluster is not touched.");
    await user.click(screen.getByRole("button", { name: "Propose" }));

    await waitFor(() => {
      expect(proposeChangeset).toHaveBeenCalledWith("cs-1");
    });
    expect(await screen.findByText("Pull request opened")).toBeInTheDocument();
  });

  it("reports an EXISTING pull request as updated (200, created: false)", async () => {
    const user = userEvent.setup();
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    proposeChangeset.mockResolvedValue({
      changesetId: "cs-1",
      remote: "https://github.com/org/infra (github)",
      branch: "vnprox/changeset-cs-1",
      path: "network/cluster.yaml",
      pullRequestId: "42",
      pullRequestUrl: "https://github.com/org/infra/pull/42",
      created: false,
    });

    renderPanel();

    await user.click(await screen.findByRole("button", { name: "Propose as pull request…" }));
    await user.click(screen.getByRole("button", { name: "Propose" }));

    expect(await screen.findByText("Pull request updated")).toBeInTheDocument();
    // Never the "opened" toast for an update — the two must stay distinguishable.
    expect(screen.queryByText("Pull request opened")).not.toBeInTheDocument();
  });

  it("degrades honestly with no [gitsync] repository configured: no button offered to fail", async () => {
    fetchGitSyncStatus.mockResolvedValue({ enabled: false, requireSignedCommits: false, planOpCount: 0 });

    renderPanel();

    expect(await screen.findByText(/Unavailable on this deployment/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Propose as pull request…" })).not.toBeInTheDocument();
    // And it never even tried the write.
    expect(proposeChangeset).not.toHaveBeenCalled();
  });

  it("shows the daemon's own 501 message when a repository is configured but has no push credential", async () => {
    const user = userEvent.setup();
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    proposeChangeset.mockRejectedValue(
      new ApiError(
        501,
        "not_implemented",
        "proposing a changeset to a git repository is not configured on this deployment ([gitsync] push_token_file)",
      ),
    );

    renderPanel();

    await user.click(await screen.findByRole("button", { name: "Propose as pull request…" }));
    await user.click(screen.getByRole("button", { name: "Propose" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The daemon refused that proposal");
    expect(alert).toHaveTextContent("[gitsync] push_token_file");
  });

  it("shows the daemon's own 422 nothing_to_propose message, distinct from the 501 case", async () => {
    const user = userEvent.setup();
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    proposeChangeset.mockRejectedValue(
      new ApiError(422, "nothing_to_propose", "this changeset has no operations to propose"),
    );

    renderPanel();

    await user.click(await screen.findByRole("button", { name: "Propose as pull request…" }));
    await user.click(screen.getByRole("button", { name: "Propose" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("this changeset has no operations to propose");
    expect(alert).not.toHaveTextContent("push_token_file");
  });

  it("shows an existing proposal on revisit via GET /changesets/{id}/proposal", async () => {
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    fetchChangesetProposal.mockResolvedValue({
      changesetId: "cs-1",
      remote: "https://github.com/org/infra (github)",
      branch: "vnprox/changeset-cs-1",
      path: "network/cluster.yaml",
      pullRequestId: "42",
      pullRequestUrl: "https://github.com/org/infra/pull/42",
      created: true,
    });

    renderPanel();

    const link = await screen.findByRole("link", { name: "pull request #42" });
    expect(link).toHaveAttribute("href", "https://github.com/org/infra/pull/42");
    expect(screen.getByText(/Already proposed as/)).toBeInTheDocument();
  });

  it("is reachable as a named region — the heading's own text, not the trailing help anchor", async () => {
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    renderPanel();
    // The section's accessible name is computed from its heading's full
    // text content, which includes the "?" help button — so this matches
    // on a leading substring rather than asserting the exact string (same
    // reasoning as drift/ConfigAsCodePage.test.tsx's own `/git sync/i`).
    expect(await screen.findByRole("region", { name: /Propose as pull request/ })).toBeInTheDocument();
  });
});
