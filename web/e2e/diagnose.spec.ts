// T-1307 acceptance criterion 6: "Diagnose" from the map on a
// three-node-vlan fixture guest (app01, pve1/200 — the same fixture T-1304's
// guest-interior.spec.ts already exercises) runs the guided diagnosis
// ladder end to end against the real stack (pvemock -> vnproxd -> the
// production SPA build) and renders the step-by-step ladder result plus
// one advisory verdict. app01's fixture data (three-node-vlan.yaml's own
// "T-1304 guest network interior inspector fixtures" comment) scripts a
// reachable icmp probe to vmbr0's own gateway (10.10.0.1), so the
// live-probe step here exercises the identical AgentExec path
// deterministically, the same reason guest-interior.spec.ts's own coverage
// is deterministic against this fixture.
//
// The "a linked fix opens the changeset drawer" half of this card's own
// wording is proven at two other levels rather than reproduced live here:
// a fixable finding only ever comes from a drift (declared-vs-live
// divergence) finding (internal/drift; every health-check producer is
// never fixable — see internal/findings' own Fixable usage), and
// manufacturing one live in this fixture would need staging+applying a
// spec pin first — a heavyweight, fixture-specific addition out of scope
// for proving THIS card's own ladder-orchestration contract. The
// suggestedFixRef -> POST /findings/{id}/fix round trip is instead proven
// at the Go HTTP-integration level (internal/api/diagnose_test.go's
// TestDiagnose_AllStepsEligible_GuestNicTarget) and the "click opens the
// drawer" interaction at the component level
// (web/src/diagnose/DiagnosisPage.test.tsx) — see this task's completion
// report for the explicit note.
import { expect, test, type Page } from "@playwright/test";

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

test("Diagnose: running the ladder against app01 renders every step and a verdict", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);

  // Same spotlight-search entry point guest-interior.spec.ts uses — "app01
  // guest" (kind "guest") is the entity the Diagnose action is offered on.
  await page.getByRole("button", { name: "Search ( / )" }).click();
  const searchDialog = page.getByRole("dialog");
  await searchDialog.getByPlaceholder(/web01/).fill("app01");
  await searchDialog.getByRole("button", { name: "app01 guest" }).click();

  const inspector = page.getByRole("dialog");
  const diagnoseButton = inspector.getByRole("button", { name: "Diagnose" });
  await expect(diagnoseButton).toBeVisible();
  await diagnoseButton.click();

  await page.waitForURL("**/diagnose?ref=guest%3Apve1%3A200");
  await expect(page.getByRole("heading", { name: "Diagnose" })).toBeVisible();
  await expect(page.getByText("guest:pve1:200")).toBeVisible();

  await page.getByRole("button", { name: "Run diagnosis" }).click();

  const steps = page.getByTestId("diagnose-steps");
  await expect(steps).toBeVisible();
  for (const name of ["config-check", "live-probe", "guest-interior", "conntrack", "capture"]) {
    await expect(page.getByTestId(`diagnose-step-${name}`)).toBeVisible();
  }

  // app01's own fixture-scripted gateway probe (see this file's own doc
  // comment) — deterministic: the live-probe step must have actually run
  // (never skipped) and reported the scripted reachable outcome.
  const liveProbe = page.getByTestId("diagnose-step-live-probe");
  await expect(liveProbe).toHaveAttribute("data-status", "ran");
  await expect(liveProbe).toContainText("reachable");

  // The config-check step (a guest source with a configured gateway) must
  // also have run — never skipped — for the same target.
  const configCheck = page.getByTestId("diagnose-step-config-check");
  await expect(configCheck).toHaveAttribute("data-status", "ran");

  // Capture was never escalated (the checkbox was left unchecked) — AC2's
  // own contract, visible here as the step reporting skipped with a
  // stated reason.
  const capture = page.getByTestId("diagnose-step-capture");
  await expect(capture).toHaveAttribute("data-status", "skipped");
  await expect(capture).toContainText("escalation was not requested");

  await expect(page.getByTestId("diagnose-verdict")).toBeVisible();
  await expect(page.getByTestId("diagnose-verdict")).toContainText("Confidence:");

  expect(pageErrors, `uncaught page errors: ${pageErrors.join(" | ")}`).toHaveLength(0);
});
