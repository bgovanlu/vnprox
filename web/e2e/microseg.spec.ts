// T-1603 acceptance criteria 4 + 5's e2e half: the microsegmentation
// review/dry-run UX, against the real stack (pvemock three-node-vlan
// fixture -> vnproxd -> the production SPA build).
//
// FIXTURE DEPENDENCY (needs dev-host wiring — see T-1603 report): the full
// propose -> dry-run -> stage -> apply happy path (AC4) and the held-out
// negative case (AC5) both require the daemon's `flow_samples` to be seeded
// with T-1602's NAS-guest flow-baseline corpus (internal/baseline/testdata)
// for a guest that also exists in the three-node-vlan inventory. There is no
// `[flows]` dev-fixture loader today (unlike `[firewalllog].dev_fixture_dir`,
// which fwlog-analytics.spec.ts relies on), so those two tests are written
// against the intended contract but are SKIPPED until that seeding lands.
// The first test below needs no seeded flows and runs today: it proves the
// planner is reachable from the guest firewall inspector, is accessible
// (axe: zero serious/critical), and that a guest with no observed flows
// degrades honestly rather than erroring.
import AxeBuilder from "@axe-core/playwright";
import { type Page } from "@playwright/test";
import { expect, test, isolatedStore } from "./isolated";

isolatedStore();

/** The guest whose flow-baseline corpus the AC4/AC5 tests expect seeded.
 * Overridable so the dev-host fixture can point at whichever guest carries
 * the NAS corpus once `[flows]` seeding exists. */
const NAS_GUEST_REF = process.env.MICROSEG_NAS_GUEST ?? "guest:pve1:200";
const EXPECTED_RULE_COUNT = Number(process.env.MICROSEG_NAS_RULES ?? "6");

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

async function openGuestFirewall(page: Page): Promise<void> {
  await page.goto("/firewall");
  await expect(page.getByRole("heading", { name: "Firewall", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Guests" }).click();
  // The guest selector populates from GET /firewall/rulesets?scope=guest.
  await expect(page.getByLabel("Select guest")).toBeVisible();
}

test("microseg planner is reachable from the guest firewall inspector and accessible", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await logIn(page);
  await openGuestFirewall(page);

  // The planner is embedded in the guest inspector.
  await expect(page.getByRole("heading", { name: "Microsegmentation planner" })).toBeVisible();
  const proposeButton = page.getByRole("button", { name: "Propose policy" });
  await expect(proposeButton).toBeVisible();

  // axe: zero serious/critical violations with the planner rendered.
  const results = await new AxeBuilder({ page }).analyze();
  const blocking = results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
  expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([]);

  // A guest with no observed flows must degrade honestly (a 404 from
  // /microseg/propose surfaces the "no observed flows" copy), never crash.
  await proposeButton.click();
  await expect(
    page.getByText(/No observed flows for this guest|rules cover/i),
  ).toBeVisible();

  expect(pageErrors, pageErrors.join("\n")).toEqual([]);
});

// AC4: propose returns the golden N-rule policy, dry-run shows zero
// would-have-blocked, staging opens the drawer with exactly those fw.* ops,
// apply succeeds. Requires the seeded NAS corpus (see file header).
test.skip("AC4: propose -> dry-run (zero would-block) -> stage -> drawer -> apply", async ({ page }) => {
  await logIn(page);
  await openGuestFirewall(page);
  await page.getByLabel("Select guest").selectOption(NAS_GUEST_REF);

  await page.getByRole("button", { name: "Propose policy" }).click();
  const summary = page.getByTestId("coverage-summary");
  await expect(summary).toBeVisible();
  // Golden rule count from T-1602's NAS-guest fixture.
  await expect(page.getByRole("table", { name: "Proposed rules" }).getByRole("row")).toHaveCount(
    EXPECTED_RULE_COUNT + 1, // + header row
  );

  await page.getByRole("button", { name: "Run dry-run" }).click();
  const blockSection = page.getByTestId("would-block-section");
  await expect(blockSection).toBeVisible();
  await expect(blockSection.getByText("Would-have-blocked flows (0)")).toBeVisible();

  // Stage -> the ordinary changeset drawer opens with the fw.rule.create ops.
  await page.getByRole("button", { name: "Stage as changeset" }).click();
  const drawer = page.getByRole("region", { name: "Change drawer" });
  await expect(drawer).toContainText("Add firewall rule");

  await page.getByRole("button", { name: "Review & apply" }).click();
  const review = page.getByRole("dialog");
  const applyButton = review.getByRole("button", { name: "Apply", exact: true });
  await expect(applyButton).toBeEnabled();
  await applyButton.click();
  await expect(page.getByText(/applied|committed/i).first()).toBeVisible({ timeout: 15_000 });
});

// AC5: a dry-run against a held-out corpus with a nonzero would-block count
// renders those flows distinctly (visually flagged) before staging.
// Requires the seeded NAS corpus with a held-out day (see file header).
test.skip("AC5: held-out dry-run with nonzero would-block flags those flows distinctly", async ({ page }) => {
  await logIn(page);
  await openGuestFirewall(page);
  await page.getByLabel("Select guest").selectOption(NAS_GUEST_REF);

  await page.getByRole("button", { name: "Propose policy" }).click();
  await expect(page.getByTestId("coverage-summary")).toBeVisible();

  await page.getByRole("button", { name: "Dry-run against held-out window" }).click();
  const blockSection = page.getByTestId("would-block-section");
  await expect(blockSection).toBeVisible();
  // Nonzero would-block: an alert marks it distinctly and the flow table
  // renders the offending rows.
  await expect(blockSection.getByRole("alert")).toBeVisible();
  await expect(blockSection.getByRole("table", { name: "Would-have-blocked flows table" }).getByRole("row")).not.toHaveCount(1);
});
