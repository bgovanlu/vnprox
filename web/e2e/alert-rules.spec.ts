// T-1005 AC5's e2e scenario, against the real stack (pvemock
// testdata/clusters/sim-lab.yaml -> vnproxd -> the production SPA build,
// this task's own webServer pair — web/playwright.config.ts, ports
// 48006/48007, see testdata/dev-alert.toml's doc comment for why a
// dedicated stack rather than sharing simulator.spec.ts's): create an
// alert rule pointed at a local test receiver (a plain Node http server
// this test spins up itself), trigger a genuinely new finding transition
// via pvemock's scripted live-probe divergence (T-806's "Verify live",
// vm-a -> vm-c tcp/2222 — sim-lab.yaml's own fixture doc comment), and
// assert a delivery appears both at the receiver and in the Settings
// page's delivery log.
//
// Rule/severity/source-filter precision (AC2) and payload-shape-per-target
// (AC1) are already covered by table-driven Go tests
// (internal/findings/webhook_test.go) and the Vitest suite
// (AlertRules.test.tsx); this scenario's job is proving the real
// Engine -> WebhookNotifier -> outbound-HTTP wiring end to end, which
// nothing else in this task's test suite exercises against a live daemon.
import { createServer, type IncomingMessage, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { expect, test, type Page } from "@playwright/test";
import { isolateFile } from "./isolate";
import { mockURL } from "./shards";

// T-3204: this test's whole premise is "a genuinely NEW finding
// transition" (see the doc comment above) — a transition that, by
// definition, fires once. Run twice in a row against a shared daemon
// (`--repeat-each=2`), the second repeat's finding was no longer new
// (T-2505's AC3). Its own vnproxd (web/e2e/isolate.ts) gives it a fresh
// findings engine every run instead.
isolateFile({ config: "testdata/dev-alert.toml", port: 64008, mockURL: mockURL("alert") });

async function logIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill("root");
  await page.getByLabel("Password", { exact: true }).fill("vnprox-mock");
  await page.getByLabel("Realm").fill("pam");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/topology");
}

/** Mirrors simulator.spec.ts's own helper: types into one endpoint
 * picker's guest-NIC search box and clicks the (single) matching result. */
async function pickGuestNic(page: Page, side: "Source" | "Destination", query: string): Promise<void> {
  await page.getByLabel(`${side} guest NIC search`).fill(query);
  const results = page.getByRole("list", { name: `${side} guest NIC results` });
  const result = results.getByRole("button").first();
  await expect(result).toBeVisible();
  await result.click();
}

interface CapturedRequest {
  body: string;
  headers: IncomingMessage["headers"];
}

/** A minimal local webhook receiver: collects every POST body/headers it
 * gets. Started on an OS-assigned free port so this test never collides
 * with another spec's own local server. */
function startReceiver(): Promise<{ server: Server; url: string; received: CapturedRequest[] }> {
  const received: CapturedRequest[] = [];
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => chunks.push(chunk));
    req.on("end", () => {
      received.push({ body: Buffer.concat(chunks).toString("utf8"), headers: req.headers });
      res.writeHead(200);
      res.end();
    });
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as AddressInfo;
      resolve({ server, url: `http://127.0.0.1:${String(port)}/webhook`, received });
    });
  });
}

test.describe("T-1005 alert rules", () => {
  test("a rule delivers a real finding transition to a local receiver, and it appears in the delivery log", async ({
    page,
  }) => {
    const { server, url: receiverUrl, received } = await startReceiver();

    try {
      const ruleName = `e2e local receiver ${String(Date.now())}`;
      await logIn(page);

      // Create the rule, filtered to source=probe: sim-lab.yaml's
      // three-node topology also raises a structural mgmt_single_path
      // (source=health) finding that fires at daemon startup, before this
      // test creates its rule — an unfiltered rule would non-
      // deterministically also catch that unrelated transition (observed:
      // Go map iteration order made it arrive first in an early run of
      // this test). Severity/source filter precision itself is already
      // covered by internal/findings/webhook_test.go and
      // AlertRules.test.tsx; this scenario's own job is proving delivery
      // wiring end to end.
      await page.goto("/settings/alert-rules");
      await page.getByRole("button", { name: "New rule" }).click();
      await page.getByLabel("Name").fill(ruleName);
      await page.getByLabel("Target URL").fill(receiverUrl);
      await page.getByRole("checkbox", { name: "probe" }).check();
      await page.getByRole("button", { name: "Create" }).click();
      await expect(page.getByRole("button", { name: new RegExp(ruleName) })).toBeVisible();

      // Trigger a genuinely new finding transition: sim-lab.yaml's scripted
      // vm-a -> vm-c tcp/2222 live-probe divergence (T-806) — this
      // dedicated daemon has never probed this tuple before, so it is a
      // fresh "new" transition.
      await page.goto("/tools");
      await page.getByRole("radio", { name: "Guest NIC" }).first().waitFor();
      await pickGuestNic(page, "Source", "vm-a");
      await pickGuestNic(page, "Destination", "vm-c");
      await page.getByLabel("Protocol").selectOption("tcp");
      // getByLabel("Port") is ambiguous against AppShell's "Ports" nav link
      // (getByLabel substring-matches accessible names) — scope to the
      // spinbutton role, unlike simulator.spec.ts's older same-named
      // queries (pre-dating that nav link; flagged in this task's report
      // rather than touched here, out of this task's scope).
      await page.getByRole("spinbutton", { name: "Port" }).fill("2222");
      await expect(page.getByText("Blocked", { exact: true })).toBeVisible();

      const verifyButton = page.getByRole("button", { name: "Verify live" });
      await expect(verifyButton).toBeEnabled();
      await verifyButton.click();
      await expect(page.getByText("Live result disagrees with the simulated verdict")).toBeVisible();

      // The finding now exists; Engine's next findings cycle (<=30s,
      // internal/findings.DefaultInterval) delivers it to the webhook.
      await expect
        .poll(() => received.length, { timeout: 45_000, message: "local receiver never got a webhook delivery" })
        .toBeGreaterThan(0);

      const first = received[0];
      if (!first) throw new Error("unreachable: poll above guarantees received.length > 0");
      const payload = JSON.parse(first.body) as { source: string; check: string; severity: string };
      expect(payload.source).toBe("probe");
      expect(payload.check).toBe("sim_divergence");
      expect(first.headers["x-vnprox-transition"]).toBe("new");

      // The Settings page's delivery log shows the same outcome.
      await page.goto("/settings/alert-rules");
      await page.getByRole("button", { name: new RegExp(ruleName) }).click();
      const log = page.getByTestId("delivery-log");
      await expect(log).toContainText("delivered");
    } finally {
      await new Promise<void>((resolve) => {
        server.close(() => {
          resolve();
        });
      });
    }
  });
});
