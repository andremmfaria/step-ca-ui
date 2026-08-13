import { psql } from "../../helpers/compose";
import { csrfTokenFrom, extractCSRF, loginOrThrow, newJar, sessionCookie } from "../../helpers/session";
import { AuthedContext, createUser, disposableName, expect, test } from "../../fixtures/test";

async function userID(username: string): Promise<string> {
  const id = await psql(`SELECT id FROM users WHERE username = '${username}'`);
  if (!id) throw new Error(`no users row for ${username}`);
  return id;
}

async function adminUserAction(admin: AuthedContext, form: Record<string, string>): Promise<void> {
  const token = await admin.csrf("/admin/users");
  const res = await admin.send(() => admin.ctx.post("/admin/users", { form: { ...form, csrf_token: token } }));
  expect([200, 302, 303], `POST /admin/users ${form["action"]}`).toContain(res.status());
}

test("E2E-AUTH-12: logout revokes a cookie captured before it", async ({ admin }) => {
  // Its own admin-role subject: POST /logout bumps session_epoch for every
  // session the acted-on user holds, which would evict the shared fixture.
  const username = disposableName("auth12");
  const password = "Auth12-e2e-Pass1!";
  await createUser(admin, username, password, "admin");

  const jarA = await newJar();
  try {
    await loginOrThrow(jarA, { username, password });
    const captured = await sessionCookie(jarA);

    // Positive control: without it, step 4's rejection is satisfied by a cookie
    // that never worked in the first place.
    const replay = await newJar();
    const before = await replay.get("/admin", { headers: { Cookie: captured } });
    expect(before.status(), "the captured cookie is a working credential").toBe(200);

    const token = extractCSRF(await (await jarA.get("/")).text(), "inline logout form");
    const loggedOut = await jarA.post("/logout", { form: { csrf_token: token } });
    expect(loggedOut.status()).toBe(302);

    // Logout bumps session_epoch, and RequireLogin re-reads the row on every
    // request and rejects a cookie whose stamped epoch no longer matches.
    const after = await replay.get("/admin", { headers: { Cookie: captured } });
    expect(after.status(), "the captured copy is revoked too").toBe(302);
    expect(after.headers()["location"]).toBe("/login");
    // rejectSession empties s.Values and Saves, which re-issues the cookie with
    // the store's normal Max-Age rather than deleting it. The observable is that
    // the value was overwritten, not that the cookie is gone.
    const reissued = after.headers()["set-cookie"] ?? "";
    expect(reissued, "the response overwrites the session cookie").toContain("step-ui=");
    expect(reissued.includes(captured.split("=").slice(1).join("=")), "with a different value").toBe(false);
    await replay.dispose();
  } finally {
    await jarA.dispose();
  }
});

test("E2E-AUTH-14: deactivation, demotion and deletion take effect on the next request", async ({ admin }) => {
  test.setTimeout(180_000);

  // Each round owns its own manager-role subject: deactivate and delete would
  // otherwise break every later test that needs manager_user usable.
  for (const round of ["deactivate", "demote", "delete"] as const) {
    const username = disposableName(`auth14-${round}`);
    const password = "Auth14-e2e-Pass1!";
    await createUser(admin, username, password, "manager");
    const uid = await userID(username);

    const jarA = await newJar();
    try {
      await loginOrThrow(jarA, { username, password });
      expect((await jarA.get("/issue")).status(), `${round}: the subject starts as a working manager`).toBe(200);

      if (round === "deactivate") {
        await adminUserAction(admin, { action: "toggle_active", uid });
        const res = await jarA.get("/issue");
        expect(res.status(), "deactivate ends the session").toBe(302);
        expect(res.headers()["location"]).toBe("/login");
        expect(res.headers()["set-cookie"] ?? "", "the session cookie is overwritten").toContain("step-ui=");
      }

      if (round === "demote") {
        await adminUserAction(admin, { action: "change_role", uid, role: "viewer" });

        // UpdateUserRole bumps the epoch, so the old session is rejected first.
        const rejected = await jarA.get("/");
        expect(rejected.status(), "the pre-demotion session is rejected").toBe(302);
        expect(rejected.headers()["location"]).toBe("/login");

        // On a new session RequireRole reads the role from the row RequireLogin
        // cached in the request context, not from the cookie.
        await loginOrThrow(jarA, { username, password });
        expect((await jarA.get("/")).status(), "the demoted user can still sign in").toBe(200);
        const forbidden = await jarA.get("/issue");
        expect(forbidden.status(), "but no longer reaches a manager route").toBe(403);
        expect(await forbidden.text()).toBe("403 Forbidden\n");
      }

      if (round === "delete") {
        await adminUserAction(admin, { action: "delete", uid });
        const res = await jarA.get("/issue");
        // loadUser finds no row, so RequireLogin rejects on the existence check
        // before the epoch comparison. A 500 here would be the regression.
        expect(res.status(), "delete ends the session without a 500").toBe(302);
        expect(res.headers()["location"]).toBe("/login");
        expect(res.headers()["set-cookie"] ?? "", "the session cookie is overwritten").toContain("step-ui=");
      }
    } finally {
      await jarA.dispose();
    }
  }
});

test("E2E-AUTH-15: a password change evicts other sessions but not the acting one", async ({ admin }) => {
  test.setTimeout(180_000);

  const password = "Auth15-e2e-Pass1!";
  const changed = "Auth15-e2e-Changed1!";

  // Round 1: the user changes their own password. Two independent jars, since
  // the property only holds across two of them.
  const self = disposableName("auth15-self");
  await createUser(admin, self, password, "viewer");
  const jarA = await newJar();
  const jarB = await newJar();
  try {
    await loginOrThrow(jarA, { username: self, password });
    await loginOrThrow(jarB, { username: self, password });
    expect((await jarA.get("/")).status()).toBe(200);
    expect((await jarB.get("/")).status()).toBe(200);

    const token = await csrfTokenFrom(jarA, "/profile");
    const res = await jarA.post("/profile", {
      form: {
        action: "change_password",
        current_password: password,
        new_password: changed,
        confirm_password: changed,
        csrf_token: token,
      },
    });
    expect([302, 303]).toContain(res.status());

    // The handler re-stamps the acting session with the freshly bumped epoch, so
    // a user is not thrown out of the page they changed their password on.
    expect((await jarA.get("/profile")).status(), "the acting session survives").toBe(200);

    const evicted = await jarB.get("/");
    expect(evicted.status(), "every other session is revoked").toBe(302);
    expect(evicted.headers()["location"]).toBe("/login");
  } finally {
    await jarA.dispose();
    await jarB.dispose();
  }

  // Round 2: an administrator resets someone else's password. No acting session
  // to preserve, so every session for the target is revoked.
  const target = disposableName("auth15-reset");
  await createUser(admin, target, password, "viewer");
  const uid = await userID(target);
  const victim = await newJar();
  try {
    await loginOrThrow(victim, { username: target, password });
    expect((await victim.get("/")).status()).toBe(200);

    await adminUserAction(admin, { action: "reset_password", uid, new_password: changed });

    const res = await victim.get("/");
    expect(res.status(), "an admin reset evicts the target's sessions").toBe(302);
    expect(res.headers()["location"]).toBe("/login");

    const reauth = await newJar();
    await loginOrThrow(reauth, { username: target, password: changed });
    expect((await reauth.get("/")).status(), "and the new password works").toBe(200);
    await reauth.dispose();
  } finally {
    await victim.dispose();
  }
});
