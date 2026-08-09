// T-2409 AC2, first half: this spec writes something another spec must not
// be able to see.
//
// The pair (isolation-a-writes / isolation-b-cannot-see) is a test OF THE
// HARNESS, not of a feature. It is the only thing that distinguishes "each
// spec has its own store" from "each spec happens not to have collided yet".
//
// The two files are named so that Playwright's alphabetical file ordering
// runs the writer first. That ordering is asserted, not assumed: the reader
// checks that a run with a shared store would actually have had something to
// find (see its own comment).
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

test("isolation: this spec creates a changeset the next one must not see", async ({ request }) => {
  const login = await request.post(stackURL() + "/api/v1/auth/login", {
    data: { username: "root@pam", password: "vnprox-mock" },
  });
  expect(login.ok()).toBe(true);
  const csrf = await csrfFrom(request);

  const created = await request.post(stackURL() + "/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
    data: { title: CONTAMINATION_TITLE, ops: [] },
  });
  expect(created.status(), await created.text()).toBe(201);

  // Precondition: it really is in this stack's store. A canary nobody planted
  // proves nothing about whether the next spec can see it.
  const list = await request.get(stackURL() + "/api/v1/changesets", {
    headers: { "X-VNPROX-CSRF": csrf },
  });
  expect(list.ok()).toBe(true);
  const titles = ((await list.json()) as { title: string }[]).map((c) => c.title);
  expect(titles).toContain(CONTAMINATION_TITLE);
});
