// T-2003 acceptance criterion 5's Playwright coverage of the change review
// flow: per-op/changeset comments, approve/reject, and the shareable
// review link — driven against the real stack (pvemock three-node-vlan
// fixture -> vnproxd -> production SPA build), the same shared 8006/8007
// stack changesets.spec.ts (T-207) already runs against.
//
// Scope note: this spec exercises the review UI end to end with this
// stack's default policy ([changesets] approval_required is unset in
// testdata/dev.toml, i.e. false) — approve/reject still record real
// decisions (POST /changesets/{id}/review/approve|reject) and the UI
// reflects them, but apply is never actually BLOCKED here, since that
// would need a second vnproxd instance configured with approval_required =
// true, and this phase's own report cautions against adding a new
// webServer unless truly needed. AC2's server-side refusal (the
// acceptance criterion that matters) is proven instead by Go tests at
// three independent layers — internal/change (service-level, direct Apply
// call), internal/api (HTTP, UI fully bypassed), and cmd/vnproxctl (CLI,
// via a real change.Service) — see planning/reports/T-2003.md.
import { type Page } from "@playwright/test";
import { expect, test, stackURL, isolatedStore } from "./isolated";

isolatedStore();


async function readCsrfCookie(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === "vnprox_csrf");
  if (!csrf) throw new Error("vnprox_csrf cookie not set — is the session logged in?");
  return csrf.value;
}

/** Discards a changeset directly via the API (federation.spec.ts's own
 * detachCluster precedent) rather than through the UI. This spec's
 * changeset is deliberately left in `draft` (approval isn't required in
 * this stack's config, and the test never applies it) — every non-terminal
 * changeset is visible to every user's "resume a parked draft" list
 * (T-207's own drawer never scopes it per-author), so leaving it behind
 * would make T-207's own walkthrough test's final "drawer is hidden after
 * dismiss" assertion see a stray "other draft" and fail — a real cross-spec
 * state leak this cleanup exists specifically to prevent. */
async function discardChangeset(page: Page, id: string): Promise<void> {
  const csrf = await readCsrfCookie(page);
  await page.request.delete(stackURL() + `/api/v1/changesets/${id}`, { headers: { "X-VNPROX-CSRF": csrf } });
}

async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) {
        el.style.setProperty("display", "none", "important");
      }
    };
    const start = () => {
      try {
        suppress();
        new MutationObserver(suppress).observe(document.documentElement, { childList: true, subtree: true });
      } catch {
        setTimeout(start, 0);
      }
    };
    start();
  });
}

async function logIn(page: Page, username = "root", password = "vnprox-mock", realm = "pam"): Promise<void> {
  await suppressOnboardingWalkthrough(page);
  await page.goto("/login");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Realm").fill(realm);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("change review: comments, approve/reject, and the shareable review link", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await logIn(page);

  // --- 1. Stage a bridge.create draft (own name so this spec's changeset
  // never collides with changesets.spec.ts's own vmbr77) --------------
  await page.getByRole("button", { name: "New ▾" }).click();
  await page.getByRole("menuitem", { name: "Bridge" }).hover();
  await page.getByRole("menuitem", { name: "pve1" }).click();
  await page.getByPlaceholder("vmbr1").fill("vmbr88");
  await page.getByRole("button", { name: "Add to changeset" }).click();
  await expect(page.getByText("Added to changeset").first()).toBeHidden({ timeout: 10_000 });

  await page.getByRole("button", { name: "Review & apply" }).click();
  const review = page.getByRole("dialog");
  await expect(review).toContainText("Create bridge vmbr88");

  // --- 2. Approval panel starts unreviewed -------------------------------
  const approvalPanel = review.getByRole("group", { name: "Review approval" });
  await expect(approvalPanel).toContainText("Not yet reviewed");
  // approval_required is off in this stack's config, so the panel says so
  // rather than claiming a requirement it doesn't have.
  await expect(approvalPanel).toContainText("does not require approval");

  // --- 3. Discussion tab: a changeset-level comment ----------------------
  await page.getByRole("tab", { name: /Discussion/ }).click();
  // exact: true — Playwright's default name match is substring/case-
  // insensitive, and "Delete comment" (each comment's own × button) would
  // otherwise also match a bare "Comment" filter once the first comment
  // exists, shifting these nth() indices out from under this test.
  const commentButtons = review.getByRole("button", { name: "Comment", exact: true });
  await review.getByLabel("Add a changeset-level comment").fill("double-check the uplink before this lands");
  await commentButtons.nth(0).click();
  await expect(review.getByText("double-check the uplink before this lands")).toBeVisible();

  // --- 4. A per-op comment -------------------------------------------------
  const opCommentBox = review.getByLabel("Comment on this operation");
  await opCommentBox.fill("MTU looks right");
  await commentButtons.nth(1).click();
  await expect(review.getByText("MTU looks right")).toBeVisible();

  // Both comments survive a page reload (persisted server-side, not local
  // component state) — the review screen re-fetches GET /changesets/{id}.
  await page.reload();
  const reviewAfterReload = page.getByRole("dialog");
  await expect(reviewAfterReload).toBeVisible();
  await reviewAfterReload.getByRole("tab", { name: /Discussion/ }).click();
  await expect(reviewAfterReload.getByText("double-check the uplink before this lands")).toBeVisible();
  await expect(reviewAfterReload.getByText("MTU looks right")).toBeVisible();

  // --- 5. Approve, then re-open and confirm it stuck ---------------------
  await reviewAfterReload.getByRole("button", { name: "Approve" }).click();
  const approvalPanelAfter = reviewAfterReload.getByRole("group", { name: "Review approval" });
  await expect(approvalPanelAfter).toContainText("Approved");
  await expect(approvalPanelAfter).toContainText("by root");

  // --- 6. Copy the shareable review link, then navigate to it directly ---
  await reviewAfterReload.getByRole("button", { name: "Copy review link" }).click();
  await expect(page.getByText("Review link copied").first()).toBeVisible();

  // Read the drawer's own persisted active-draft id (localStorage, the same
  // mechanism T-207's reload-survival relies on) rather than guessing which
  // draft is "ours" from a shared-stack list — robust regardless of what
  // other specs' changesets happen to also be sitting in `draft` status.
  const changesetId = await page.evaluate(() => {
    const raw = window.localStorage.getItem("vnprox.changesetDrawer");
    if (!raw) return undefined;
    const parsed = JSON.parse(raw) as { state?: { activeId?: string } };
    return parsed.state?.activeId;
  });
  expect(changesetId).toBeTruthy();
  if (!changesetId) throw new Error("unreachable: asserted above");

  await page.goto(`/changesets/${changesetId}/review`);
  const directReview = page.getByRole("dialog");
  await expect(directReview).toContainText("Create bridge vmbr88");
  await expect(directReview.getByRole("group", { name: "Review approval" })).toContainText("Approved");

  // --- 7. Reject, with a reason, overrides the prior approval -------------
  await directReview.getByRole("button", { name: "Reject" }).click();
  await directReview.getByLabel("Rejection reason").fill("actually let's hold off");
  await directReview.getByRole("button", { name: "Confirm reject" }).click();
  const approvalPanelRejected = directReview.getByRole("group", { name: "Review approval" });
  await expect(approvalPanelRejected).toContainText("Rejected");
  await expect(approvalPanelRejected).toContainText("actually let's hold off");

  // --- 8. Clean up: this draft is never applied or discarded through the
  // UI, so discard it directly via the API. Every non-terminal changeset is
  // visible to every user's "resume a parked draft" list (T-207's drawer
  // never scopes it per-author), so leaving it behind would make
  // changesets.spec.ts's own T-207 walkthrough see a stray "other draft" and
  // fail its "drawer is hidden after dismiss" assertion.
  await discardChangeset(page, changesetId);
});
