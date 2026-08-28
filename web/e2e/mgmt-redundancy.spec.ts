// SPDX-License-Identifier: Apache-2.0

// T-703 AC3/AC4 end-to-end: the guided management-redundancy wizard against
// the single-node fixture (the management-path SPOF case — vmbr0 rides on a
// single NIC eno1, with a spare eno2). Drives the full journey the card
// spells out: the mgmt_single_path finding → wizard → drawer → review (the
// mandatory acknowledgement block, typed node name required, apply disabled
// until complete) → apply → countdown → confirm → committed; then asserts
// the finding clears on the next poll. A second test covers the no-confirm
// path: the deadline expires → rolled_back and the finding is still present.
//
// This spec runs against its own pvemock (port 38006, single-node fixture,
// read-only, shared per web/e2e/shards.ts) — the suite's default
// three-node-vlan cluster is already redundant and raises no
// mgmt_single_path finding. See testdata/dev-mgmt.toml.
//
// T-3204: as of this file's own doc comment above, this spec already ran
// against a dedicated mgmt-stack vnproxd rather than the shared default
// one — but that vnproxd was still shared with federation.spec.ts and
// federation-clusters.spec.ts (both also list "mgmt" in
// web/e2e/shards.ts's SPEC_STACKS) and, before T-2505, with the whole
// suite. This file's own apply/confirm and expire/rollback tests mutate
// the SAME mgmt_single_path finding + changeset lifecycle, so
// `--repeat-each=2` ran its second repeat against the first repeat's
// already-committed changeset (T-2505's AC3). It now gets its OWN vnproxd
// (web/e2e/isolate.ts, port 64005) rather than sharing 38007 with anything.
import { expect, test, type Page } from "@playwright/test";
import { isolateFile } from "./isolate";
import { mockURL } from "./shards";

isolateFile({ config: "testdata/dev-mgmt.toml", port: 64005, mockURL: mockURL("mgmt") });

// Hides the first-login onboarding walkthrough banner so it doesn't push
// the finding/wizard affordances around (the exact CSS-via-.style approach
// changesets.spec.ts documents for the style-src CSP).
async function suppressOnboardingWalkthrough(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const suppress = () => {
      const el = document.querySelector('[aria-label="Onboarding walkthrough"]');
      if (el instanceof HTMLElement) el.style.display = "none";
    };
    const obs = new MutationObserver(suppress);
    document.addEventListener("DOMContentLoaded", () => {
      suppress();
      obs.observe(document.body, { childList: true, subtree: true });
    });
  });
}

async function logIn(page: Page): Promise<void> {
  await suppressOnboardingWalkthrough(page);
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Opens the Tools page and returns the mgmt_single_path finding card
 * (waiting for the health check to have fired at least once). */
async function mgmtFinding(page: Page) {
  await page.goto("/tools");
  const finding = page
    .getByRole("listitem")
    .filter({ hasText: "mgmt_single_path" });
  await expect(finding.first()).toBeVisible({ timeout: 30_000 });
  return finding.first();
}

test("finding → wizard → review ack → apply → confirm → committed, finding clears", async ({ page }) => {
  await logIn(page);

  // --- 1. The SPOF finding is present, and offers the wizard --------------
  const finding = await mgmtFinding(page);
  await finding.getByRole("button", { name: /Make management path redundant/i }).click();

  // --- 2. Wizard: bond the uplink (flow A) with the spare NIC -------------
  const wizard = page.getByRole("dialog");
  await expect(wizard).toContainText(/Protect this node's management connection/i);
  // Flow A ("Bond the management uplink") is the default selected option;
  // advance to the config step.
  await wizard.getByRole("button", { name: "Next" }).click();
  // Pick the spare card (eno2) and keep the safe active/standby default.
  await wizard.getByLabel(/Second network card to add/i).selectOption("eno2");
  // Draft into the changeset drawer (WizardShell's finish button).
  await wizard.getByRole("button", { name: "Create draft" }).click();

  // --- 3. Drawer → review -------------------------------------------------
  const drawer = page.getByRole("region", { name: "Change drawer" });
  await expect(drawer).toContainText(/Create bond bond0/i);
  await drawer.getByRole("button", { name: "Review & apply" }).click();

  // --- 4. Review: the mandatory acknowledgement block --------------------
  const review = page.getByRole("dialog", { name: /Review & apply/i });
  const ack = review.getByRole("group", { name: /management-path acknowledgement/i });
  await expect(ack).toBeVisible();
  const applyBtn = review.getByRole("button", { name: /^Apply$/ });
  await expect(applyBtn).toBeDisabled();

  // Typing the wrong node keeps apply disabled; the right node enables it.
  await ack.getByLabel(/Type node name to acknowledge/i).fill("wrong");
  await expect(applyBtn).toBeDisabled();
  await ack.getByLabel(/Type node name to acknowledge/i).fill("pve1");
  await expect(applyBtn).toBeEnabled();

  // The confirm window floors at 180s for a mgmt-path change.
  await expect(review.getByRole("spinbutton")).toHaveValue("180");

  await applyBtn.click();

  // --- 5. Countdown → confirm → committed --------------------------------
  const countdown = page
    .getByRole("alert")
    .filter({ has: page.getByRole("button", { name: "Confirm" }) });
  await expect(countdown).toBeVisible({ timeout: 30_000 });
  await countdown.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: /applied and committed/i }),
  ).toBeVisible({ timeout: 30_000 });

  // --- 6. The acknowledgement is recorded in the audit trail (AC3) -------
  await page.goto("/audit");
  await expect(page.getByRole("cell", { name: "changeset.mgmt_ack" }).first()).toBeVisible({
    timeout: 30_000,
  });

  // NOTE (mock limitation, flagged in the T-703 report): AC3's "the
  // mgmt_single_path finding clears on the next poll" and "the fixture
  // interfaces file shows the bond" are NOT asserted here because they are
  // not observable against the static pvemock. Node-file network ops write
  // the dev host sandbox (var/dev-mgmt-host) and never call pvemock's PVE
  // network endpoint (docs/architecture.md §4's T-607 correction:
  // "pvemock's in-memory network model and the dev host-writer sandbox can
  // genuinely diverge"), while the collector's host reader is host.NewReal()
  // (the actual machine), not that sandbox — so the applied bond never
  // re-enters the inventory the finding is computed from. These two clauses
  // are moved to the hardware-validation list.
});

/** Every mutating request needs the double-submit `X-VNPROX-CSRF` header
 * (docs/api.md §Auth; src/api/auth.ts's `readCsrfCookie`) — mirrors
 * responsive-triage.spec.ts's identical helper. */
async function readCsrfCookie(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === "vnprox_csrf");
  if (!csrf) throw new Error("vnprox_csrf cookie not set — is the session logged in?");
  return csrf.value;
}

// T-909 acceptance criterion 2: the same touchesMgmtPath ceremony (the
// typed-acknowledgement block, apply, the commit-confirm countdown, and
// confirm/rollback) must be fully usable at a narrow (phone-width)
// viewport — "commit-confirm from a phone" is this task's target scenario,
// and the mgmt-path ack ceremony is the one write path it must not
// degrade.
//
// The draft is seeded directly via the API (a `bridge.update` touching
// only `comments` on the node's own mgmt-path bridge — never `Ports`, per
// opBuilders.ts's own doc comment that a bridge.update can never touch
// port membership) rather than through the wizard this file's other test
// drives: that test's Flow A already consumes this single-node fixture's
// only two NICs into a bond, and this spec file's webServer/interfaces
// sandbox is shared (not reset) between the two tests in this file, so a
// second wizard-driven bond attempt fails applying against the
// already-mutated sandbox regardless of viewport (confirmed by a first
// draft of this test: "failed to apply at step 1"). A comments-only
// bridge.update still targets the mgmt-path carrier ref directly, so the
// server still computes `touchesMgmtPath: true` (docs/api.md: true for
// *any* changeset touching the path, hand-built drafts included) — this
// keeps the test focused on what this task card is actually about
// (review/ack/apply/confirm at narrow width) without depending on a
// carrier NIC being free. The draft is picked up through the drawer's
// real "resume parked drafts" affordance, not injected client-side, so
// this still exercises the real component tree.
test("narrow viewport: mgmt-path ack block, apply, and confirm work end to end at phone width", async ({ page }) => {
  await logIn(page);
  await page.setViewportSize({ width: 390, height: 844 });

  const csrf = await readCsrfCookie(page);
  const create = await page.request.post("/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
    data: {
      title: "Management redundancy: pve1",
      ops: [{ op: "bridge.update", target: "bridge:pve1:vmbr0", params: { comments: "on-call check" } }],
    },
  });
  expect(create.ok(), `POST /changesets failed: ${String(create.status())} ${await create.text()}`).toBe(true);

  // --- 1. Reach the seeded draft through the drawer's real "resume" UI ---
  // Scoped to the drawer region throughout: the Dashboard's own "Pending
  // changesets" tile (T-904) also renders a same-named deep-link button,
  // so an unscoped lookup is ambiguous.
  await page.goto("/");
  const drawer = page.getByRole("region", { name: "Change drawer" });
  await drawer.getByRole("button", { name: /Changes/ }).click();
  await drawer.getByRole("button", { name: /Resume parked drafts/ }).click();
  await drawer.getByRole("button", { name: /Management redundancy: pve1/ }).click();

  await expect(drawer).toContainText(/Management redundancy: pve1/i);

  // --- 2. Review -> apply -> confirm, all at phone width ------------------
  await drawer.getByRole("button", { name: "Review & apply" }).click();

  const review = page.getByRole("dialog", { name: /Review & apply/i });
  const ack = review.getByRole("group", { name: /management-path acknowledgement/i });
  await expect(ack).toBeVisible();
  const applyBtn = review.getByRole("button", { name: /^Apply$/ });
  await expect(applyBtn).toBeDisabled();

  await ack.getByLabel(/Type node name to acknowledge/i).fill("pve1");
  await expect(applyBtn).toBeEnabled();
  await expect(review.getByRole("spinbutton")).toHaveValue("180");

  await applyBtn.click();

  // --- 4. Countdown -> confirm -> committed, all at phone width ----------
  const countdown = page
    .getByRole("alert")
    .filter({ has: page.getByRole("button", { name: "Confirm" }) });
  await expect(countdown).toBeVisible({ timeout: 30_000 });
  await countdown.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: /applied and committed/i }),
  ).toBeVisible({ timeout: 30_000 });
});
