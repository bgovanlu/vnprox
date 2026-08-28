// SPDX-License-Identifier: Apache-2.0

// T-3003 AC2: registering a webhook at a private address in the default
// configuration is refused with the daemon's own reason, naming the policy
// and the knob that would permit it.
//
// It also pins the card-vs-contract finding this section exists around: the
// webhook routes are gated on `automation`, which no browser session can ever
// hold, so the ordinary rendering here is a *named refusal* rather than a
// list. Both halves are asserted, because a future change that silently
// swallowed the 403 into an empty table would look like a working screen.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { Webhook, WebhookCreateRequest } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { WebhooksSection } from "./WebhooksSection";

const fetchWebhooks = vi.fn<() => Promise<Webhook[]>>();
const createWebhook = vi.fn<(req: WebhookCreateRequest) => Promise<Webhook>>();
const deleteWebhook = vi.fn<(id: string) => Promise<void>>();

vi.mock("../api/webhooks", () => ({
  fetchWebhooks: () => fetchWebhooks(),
  createWebhook: (req: WebhookCreateRequest) => createWebhook(req),
  deleteWebhook: (id: string) => deleteWebhook(id),
}));

/** The exact string internal/automation/targetguard.go's checkIP produces.
 * Copied verbatim rather than paraphrased: the point of the assertion is that
 * the UI relays the daemon's words, so a paraphrase here would test nothing. */
const PRIVATE_TARGET_REFUSAL =
  "webhook target 10.0.0.5 is a non-public address ([webhooks] allow_private_targets = true overrides, and warns at startup)";

function renderSection(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <WebhooksSection />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchWebhooks.mockReset();
  createWebhook.mockReset();
  deleteWebhook.mockReset();
});

describe("the destination policy", () => {
  it("renders the daemon's own refusal, naming the policy and the config knob", async () => {
    const user = userEvent.setup();
    fetchWebhooks.mockResolvedValue([]);
    createWebhook.mockRejectedValue(new ApiError(400, "validation_failed", PRIVATE_TARGET_REFUSAL));

    renderSection();
    await screen.findByTestId("webhooks-empty");

    await user.type(screen.getByLabelText(/Destination URL/), "https://10.0.0.5/hook");
    await user.type(screen.getByLabelText(/Signing secret/), "s3cret");
    await user.click(screen.getByTestId("register-webhook"));

    const message = await screen.findByTestId("register-webhook-error-message");
    expect(message).toHaveTextContent(PRIVATE_TARGET_REFUSAL);
    // Specifically: the address class it objected to, and the knob.
    expect(message).toHaveTextContent("non-public address");
    expect(message).toHaveTextContent("[webhooks] allow_private_targets = true");
  });

  it("does not pre-empt the daemon with a client-side address check", async () => {
    // A browser-side copy of the policy would drift from the enforcement
    // point and would report a reason the daemon never gave. The request must
    // actually be attempted.
    const user = userEvent.setup();
    fetchWebhooks.mockResolvedValue([]);
    createWebhook.mockRejectedValue(new ApiError(400, "validation_failed", PRIVATE_TARGET_REFUSAL));

    renderSection();
    await screen.findByTestId("webhooks-empty");
    await user.type(screen.getByLabelText(/Destination URL/), "http://127.0.0.1:9000/hook");
    await user.type(screen.getByLabelText(/Signing secret/), "s3cret");
    await user.click(screen.getByTestId("register-webhook"));

    await screen.findByTestId("register-webhook-error-message");
    expect(createWebhook).toHaveBeenCalledWith({ url: "http://127.0.0.1:9000/hook", secret: "s3cret" });
  });
});

describe("the automation-capability gate", () => {
  it("explains a 403 by name instead of rendering an empty list", async () => {
    fetchWebhooks.mockRejectedValue(new ApiError(403, "forbidden", "missing capability: automation"));
    renderSection();

    const notice = await screen.findByTestId("webhooks-error");
    expect(notice).toHaveAttribute("data-refusal-kind", "forbidden");
    expect(notice).toHaveTextContent("automation");
    expect(notice).toHaveTextContent(/never derived from a Proxmox privilege/);
    // "You may not look" must not be rendered as "there is nothing here".
    expect(screen.queryByTestId("webhooks-empty")).toBeNull();
  });

  it("offers no registration form a 403 session could only fail with", async () => {
    fetchWebhooks.mockRejectedValue(new ApiError(403, "forbidden", "missing capability: automation"));
    renderSection();

    await screen.findByTestId("webhooks-error");
    expect(screen.queryByTestId("register-webhook")).toBeNull();
    expect(screen.getByTestId("webhooks-no-form")).toBeInTheDocument();
  });

  it("distinguishes a not-mounted route family from a refused one", async () => {
    fetchWebhooks.mockRejectedValue(new ApiError(404, "not_found", "no such API route"));
    renderSection();

    const notice = await screen.findByTestId("webhooks-error");
    expect(notice).toHaveAttribute("data-refusal-kind", "unavailable");
    expect(notice).toHaveTextContent(/does not mount the webhook routes/);
  });
});

describe("delivery health", () => {
  function webhook(partial: Partial<Webhook>): Webhook {
    return {
      id: "w1",
      url: "https://hooks.example/x",
      createdBy: "root@pam",
      createdAt: 1,
      consecutiveFailures: 0,
      ...partial,
    };
  }

  it("calls a never-attempted registration unattempted, not healthy", async () => {
    fetchWebhooks.mockResolvedValue([webhook({})]);
    renderSection();

    const cell = await screen.findByTestId("delivery-w1");
    expect(cell).toHaveAttribute("data-delivery-state", "unattempted");
    // consecutiveFailures === 0 before any attempt says nothing at all; it
    // must not read as "delivering".
    expect(cell).not.toHaveTextContent("Delivering");
  });

  it("shows the daemon's own last delivery error for a failing target", async () => {
    fetchWebhooks.mockResolvedValue([
      webhook({ consecutiveFailures: 3, lastAttemptAt: 200, lastError: PRIVATE_TARGET_REFUSAL }),
    ]);
    renderSection();

    expect(await screen.findByTestId("delivery-w1")).toHaveAttribute("data-delivery-state", "failing");
    expect(screen.getByTestId("delivery-error-w1")).toHaveTextContent("[webhooks] allow_private_targets");
  });
});
