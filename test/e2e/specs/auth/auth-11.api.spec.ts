import { psqlRows } from "../../helpers/compose";
import { CSRF_FLASH } from "../../helpers/flash";
import { csrfTokenFrom, extractCSRF, loginOrThrow, newJar } from "../../helpers/session";
import { createUser, disposableName, expect, test } from "../../fixtures/test";

test("E2E-AUTH-11: logout is a POST, and a GET to the same path logs nobody out", async ({ admin }) => {
  const username = disposableName("auth11");
  const password = "Auth11-e2e-Pass1!";
  await createUser(admin, username, password, "viewer");

  const jarA = await newJar();
  try {
    await loginOrThrow(jarA, { username, password });
    expect((await jarA.get("/")).status()).toBe(200);

    // LogoutGet is a bare redirect kept so an old bookmark degrades rather than
    // breaking; a regression to the V10 shape would end the session here.
    const getLogout = await jarA.get("/logout");
    expect(getLogout.status()).toBe(302);
    expect(getLogout.headers()["location"]).toBe("/login");
    expect((await jarA.get("/")).status()).toBe(200);

    const noToken = await jarA.post("/logout", { form: {} });
    expect(noToken.status()).toBe(303);
    expect(noToken.headers()["location"]).toBe("/");

    // Flashes are one-shot, so this single GET both proves the session survived
    // and carries the session-error message.
    const home = await jarA.get("/");
    expect(home.status(), "the session survives a token-less logout").toBe(200);
    const homeBody = await home.text();
    expect(homeBody).toContain(CSRF_FLASH);
    const token = extractCSRF(homeBody, "inline logout form on /");

    const postLogout = await jarA.post("/logout", { form: { csrf_token: token } });
    expect(postLogout.status()).toBe(302);
    expect(postLogout.headers()["location"]).toBe("/login");
    // The plan says "Max-Age=-1", which is the Go-side MaxAge value; net/http
    // serialises any negative MaxAge as "Max-Age=0" plus a 1970 Expires, so the
    // wire form is what gets asserted here.
    const setCookie = postLogout.headers()["set-cookie"] ?? "";
    expect(setCookie).toContain("Max-Age=0");
    expect(setCookie).toContain("Expires=Thu, 01 Jan 1970");

    // RequireLogin redirects rather than forbidding; a 403 here means the two
    // middlewares have been transposed.
    const after = await jarA.get("/");
    expect(after.status()).toBe(302);
    expect(after.headers()["location"]).toBe("/login");

    const reasons = await psqlRows(
      `SELECT coalesce(reason, '') FROM auth_log WHERE username = '${username}' ORDER BY id DESC LIMIT 1`,
    );
    expect(reasons[0]).toBe("Logout");
  } finally {
    await jarA.dispose();
  }
});

test("E2E-AUTH-11: both base templates render an inline logout form carrying a token", async ({ admin }) => {
  // base.html and admin_base.html each render the form, so the POST is reachable
  // from every authenticated page rather than from one hand-picked route.
  for (const route of ["/", "/admin"]) {
    const res = await admin.send(() => admin.ctx.get(route));
    expect(res.status(), `${route} should render for admin`).toBe(200);
    const body = await res.text();
    expect(body, `${route} renders a logout form`).toMatch(/<form[^>]+action="\/logout"/);
    expect(extractCSRF(body, route).length).toBeGreaterThan(0);
  }
  await csrfTokenFrom(admin.ctx, "/");
});
