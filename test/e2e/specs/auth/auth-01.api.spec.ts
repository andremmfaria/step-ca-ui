import { psqlRows } from "../../helpers/compose";
import { CSRF_FLASH } from "../../helpers/flash";
import { csrfTokenFrom, extractCSRF, newJar } from "../../helpers/session";
import { createUser, disposableName, expect, test } from "../../fixtures/test";

test("E2E-AUTH-01: successful local login rotates session content", async ({ admin }) => {
  // POST /logout bumps session_epoch for every session the acted-on user holds,
  // so this runs against a throwaway user rather than evicting the admin fixture.
  const username = disposableName("auth01");
  const password = "Auth01-e2e-Pass1!";
  await createUser(admin, username, password, "viewer");

  const jarA = await newJar();
  try {
    const pre = await jarA.get("/login");
    const tokenPre = extractCSRF(await pre.text(), "GET /login before login");

    const loginRes = await jarA.post("/login", {
      form: { username, password, csrf_token: tokenPre },
    });
    expect(loginRes.status()).toBe(302);
    expect(loginRes.headers()["location"]).toBe("/");

    // completeLogin resets s.Values and assigns a fresh token (handlers/auth.go:192-202).
    const tokenPost = await csrfTokenFrom(jarA, "/login");
    expect(tokenPost).not.toBe(tokenPre);

    // The observable consequence of the rotation: the pre-login token no longer
    // validates. Asserting "the cookie value changed" would be vacuous, since
    // securecookie re-encrypts with a fresh nonce on every Save.
    const replay = await jarA.post("/profile", {
      form: { action: "theme", theme: "dark", csrf_token: tokenPre },
    });
    expect(replay.status()).toBe(303);
    expect(replay.headers()["location"]).toBe("/profile");

    const flashed = await jarA.get("/profile");
    expect(await flashed.text()).toContain(CSRF_FLASH);

    const rows = await psqlRows(
      `SELECT success, coalesce(reason, '') FROM auth_log WHERE username = '${username}' ORDER BY id DESC LIMIT 1`,
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]).toBe("t|");

    const securityPage = await admin.send(() => admin.ctx.get("/admin/security"));
    expect(await securityPage.text()).toContain(username);
  } finally {
    // GET /logout is a bare redirect that ends nothing; only the CSRF-gated POST
    // bumps session_epoch (handlers/auth.go:209-211, :222).
    const token = await csrfTokenFrom(jarA, "/");
    await jarA.post("/logout", { form: { csrf_token: token } });
    await jarA.dispose();
  }
});
