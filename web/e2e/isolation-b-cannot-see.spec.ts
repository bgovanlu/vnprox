// T-2409 AC2, second half: the changeset isolation-a-writes.spec.ts created
// must be invisible here.
//
// HOW TO SEE THIS TEST FAIL — which matters more than seeing it pass:
//
//     VNPROX_E2E_SHARED=1 npx playwright test e2e/isolation-
//
// That runs both files against ONE stack, which is exactly the arrangement
// this suite had before T-2409, and this test fails. Without the variable,
// each file gets its own store and it passes. A green run in only one of
// those two modes is what proves the isolation is real rather than
// coincidental.
import type { APIRequestContext } from "@playwright/test";
import { expect, isolatedStore, stackURL, test } from "./isolated";
import { CONTAMINATION_TITLE } from "./isolationCanary";

isolatedStore();

/** The CSRF token vnproxd sets as a cookie on login; every mutating request
 * must echo it in X-VNPROX-CSRF. Read from the cookie jar rather than the
 * login body, which is where the SPA reads it from too. */
async function csrfFrom(request: APIRequestContext): Promise<string> {
  const state = await request.storageState();
  const cookie = state.cookies.find((c) => c.name === "vnprox_csrf");
  if (cookie === undefined) throw new Error("vnprox_csrf cookie not set — did the login succeed?");
  return cookie.value;
}

test("isolation: the previous spec's changeset is not in this spec's store", async ({ request }) => {
  const login = await request.post(stackURL() + "/api/v1/auth/login", {
    data: { username: "root@pam", password: "vnprox-mock" },
  });
  expect(login.ok()).toBe(true);
  const csrf = await csrfFrom(request);

  const list = await request.get(stackURL() + "/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
  });
  expect(list.ok()).toBe(true);
  const titles = ((await list.json()) as { title: string }[]).map((c) => c.title);

  expect(
    titles,
    "isolation-a-writes.spec.ts's changeset is visible here, so the two specs share a store — " +
      "a spec's result now depends on which specs ran before it",
  ).not.toContain(CONTAMINATION_TITLE);

  // Non-vacuity. If this stack could not see ANY changeset, the assertion
  // above would pass for the wrong reason — a broken listing rather than a
  // clean store.
  const mine = await request.post(stackURL() + "/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
    data: { title: "t2409-own-changeset", ops: [] },
  });
  expect(mine.status(), await mine.text()).toBe(201);

  const after = await request.get(stackURL() + "/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
  });
  const afterTitles = ((await after.json()) as { title: string }[]).map((c) => c.title);
  expect(afterTitles, "this stack cannot see its own changeset; the check above proves nothing").toContain(
    "t2409-own-changeset",
  );
});
