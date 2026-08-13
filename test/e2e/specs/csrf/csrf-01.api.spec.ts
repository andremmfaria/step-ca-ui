import { APIResponse } from "@playwright/test";
import { logs, psql } from "../../helpers/compose";
import { CSRF_FLASH } from "../../helpers/flash";
import { postRoutes, withID } from "../../helpers/routes";
import { csrfTokenFrom, loginOrThrow, newJar } from "../../helpers/session";
import { AuthedContext, createUser, disposableName, expect, test } from "../../fixtures/test";

const INLINE_200 = new Map<string, string>([
  ["/login", CSRF_FLASH],
  ["/reset-password", CSRF_FLASH],
  ["/forgot-password", "Session error. Please refresh the page and try again."],
]);

/** The control certificate the /issue, /renew and /revoke rows all act against. */
const CONTROL_NAME = disposableName("csrf-control");

interface RouteCase {
  pattern: string;
  role: "anon" | "manager" | "admin";
  form: Record<string, string>;
}

function roleFor(pattern: string): RouteCase["role"] {
  if (["/login", "/forgot-password", "/reset-password"].includes(pattern)) return "anon";
  if (pattern.startsWith("/admin") || pattern.startsWith("/revoke")) return "admin";
  return "manager";
}

function formFor(pattern: string): Record<string, string> {
  switch (pattern) {
    case "/login":
      return { username: "admin", password: "irrelevant-because-csrf-runs-first" };
    case "/forgot-password":
      return { username: "admin" };
    case "/reset-password":
      return { token: "fabricated", password: "Reset-e2e-Pass1!", confirm_password: "Reset-e2e-Pass1!" };
    case "/issue":
      return { name: "csrf-should-not-exist", domain: "csrf-should-not-exist.e2e.invalid", template: "server", key_type: "EC:P-256", duration: "24h" };
    case "/import":
      return { name: "csrf-import" };
    case "/admin/users":
      return { action: "create", username: "csrf-should-not-exist", password: "Csrf-e2e-Pass1!", role: "viewer" };
    case "/admin/users-temp":
      return { action: "create", username: "csrf-temp-should-not-exist", role: "viewer", ttl: "1h" };
    case "/admin/console":
      return { command_id: "ca.health" };
    case "/admin/notifications":
      return { smtp_host: "mailpit", smtp_port: "1025" };
    case "/profile":
      return { action: "theme", theme: "dark" };
    case "/le/issue":
      return { domain: "csrf-le.e2e.invalid", provider: "http01" };
    case "/le/settings":
      return { email: "csrf@e2e.invalid", provider: "http01" };
    default:
      return {};
  }
}

async function assertRejected(pattern: string, res: APIResponse): Promise<void> {
  const inline = INLINE_200.get(pattern);
  const body = await res.text();
  if (inline) {
    expect(res.status(), `${pattern} renders its CSRF error inline`).toBe(200);
    expect(body, `${pattern} inline error text`).toContain(inline);
  } else {
    expect(res.status(), `${pattern} uses requireCSRF`).toBe(303);
    expect(res.headers()["location"], `${pattern} redirect target`).toBeTruthy();
  }
}

test.describe("E2E-CSRF-01: every POST route rejects a missing and a wrong token", () => {
  let controlID = "";

  test.beforeAll(async ({ manager }) => {
    // The /renew and /revoke rows need a real certificate to act against, and
    // CSRF-01 runs before the certificate suite in the Section 3.0.3 order.
    const token = await manager.csrf("/issue");
    const res = await manager.send(() =>
      manager.ctx.post("/issue", {
        form: {
          name: CONTROL_NAME,
          domain: `${CONTROL_NAME}.e2e.invalid`,
          template: "server",
          key_type: "EC:P-256",
          duration: "24h",
          csrf_token: token,
        },
      }),
    );
    expect([302, 303], "control certificate issuance is this test's positive control").toContain(res.status());
    controlID = await psql(`SELECT id FROM certificates WHERE name = '${CONTROL_NAME}' ORDER BY id DESC LIMIT 1`);
    expect(controlID, "control certificate row").not.toBe("");
  });

  test("E2E-CSRF-01: the derived route list covers every registered POST route", () => {
    const patterns = postRoutes().map((r) => r.pattern);
    expect(patterns.length, "derived from main.go's router, not hardcoded").toBeGreaterThanOrEqual(23);
    for (const expected of ["/login", "/logout", "/issue", "/revoke/{id}", "/admin/console", "/le/settings"]) {
      expect(patterns, `${expected} is registered`).toContain(expected);
    }
  });

  test("E2E-CSRF-01: missing and wrong tokens are both rejected on every POST route", async ({ manager, admin }) => {
    const anon = await newJar();
    try {
      for (const { pattern } of postRoutes()) {
        const role = roleFor(pattern);
        const ctx = role === "anon" ? anon : ({ manager, admin } as Record<string, AuthedContext>)[role]!.ctx;
        const url = withID(pattern, pattern.startsWith("/le/") ? 999999 : controlID);
        const form = formFor(pattern);

        const missing = await ctx.post(url, { form });
        await assertRejected(pattern, missing);

        const wrong = await ctx.post(url, { form: { ...form, csrf_token: "wrong-value" } });
        await assertRejected(pattern, wrong);
      }
    } finally {
      await anon.dispose();
    }
  });

  test("E2E-CSRF-01: /login is refused even with correct credentials", async ({ admin }) => {
    const jar = await newJar();
    try {
      const res = await jar.post("/login", { form: { username: admin.creds.username, password: admin.creds.password } });
      expect(res.status()).toBe(200);
      expect(await res.text()).toContain(CSRF_FLASH);

      const home = await jar.get("/");
      expect(home.status(), "no session was established").toBe(302);
      expect(home.headers()["location"]).toBe("/login");
    } finally {
      await jar.dispose();
    }
  });

  test("E2E-CSRF-01: /logout leaves the session alive", async ({ admin }) => {
    const username = disposableName("csrf-logout");
    const password = "CsrfLogout-e2e-Pass1!";
    await createUser(admin, username, password, "viewer");
    const jar = await newJar();
    try {
      await loginOrThrow(jar, { username, password });

      const res = await jar.post("/logout", { form: { csrf_token: "wrong-value" } });
      expect(res.status()).toBe(303);
      expect(res.headers()["location"], "requireCSRF sends this one to /, not /login").toBe("/");
      expect((await jar.get("/")).status(), "the session survives").toBe(200);
    } finally {
      const token = await csrfTokenFrom(jar, "/");
      await jar.post("/logout", { form: { csrf_token: token } });
      await jar.dispose();
    }
  });

  test("E2E-CSRF-01: a rejected /issue creates nothing and never reaches the CA", async ({ manager }) => {
    const name = disposableName("csrf-noissue");
    // Section 3.0.5: scope the absence to this test's own window, since a whole-
    // log grep would match a /sign another test legitimately produced.
    const since = new Date().toISOString();
    const before = await psql("SELECT count(*) FROM certificates");

    const res = await manager.ctx.post("/issue", {
      form: { name, domain: `${name}.e2e.invalid`, template: "server", key_type: "EC:P-256", duration: "24h" },
    });
    expect(res.status()).toBe(303);

    expect(await psql("SELECT count(*) FROM certificates"), "no row was created").toBe(before);
    expect(await psql(`SELECT count(*) FROM certificates WHERE name = '${name}'`)).toBe("0");

    const caLog = await logs("step-ca", { since });
    const signs = caLog.split("\n").filter((l) => l.includes("/sign")).length;
    expect(signs, `no /sign reached the CA in this window; log was:\n${caLog.slice(-1500)}`).toBe(0);
  });

  test("E2E-CSRF-01: a rejected /admin/console writes no console row", async ({ admin }) => {
    // The id bound keeps the oracle sound whenever this runs: E2E-ADM-01 and
    // E2E-ADM-05 write rows of exactly this shape later in the same run.
    const maxBefore = await psql("SELECT coalesce(max(id), 0) FROM auth_log");

    const res = await admin.ctx.post("/admin/console", { form: { command_id: "ca.health" } });
    expect(res.status()).toBe(303);

    const rows = await psql(
      `SELECT count(*) FROM auth_log WHERE id > ${maxBefore} AND coalesce(reason, '') IN ('console.run', 'console.denied')`,
    );
    expect(rows, "neither console.run nor console.denied was written").toBe("0");
  });

  test("E2E-CSRF-01: a rejected backup download is not a gzip stream", async ({ admin }) => {
    const res = await admin.ctx.post("/admin/backup/download", { form: {} });
    expect(res.status()).toBe(303);
    const body = await res.body();
    expect(body.length < 2 || !(body[0] === 0x1f && body[1] === 0x8b), "body is not gzip").toBe(true);
  });

  test("E2E-CSRF-01: rejected /renew and /revoke leave the control certificate untouched", async ({ admin, manager }) => {
    const before = await psql(
      `SELECT coalesce(serial, '') || '|' || coalesce(status, '') || '|' || coalesce(expires_at::text, '') FROM certificates WHERE id = ${controlID}`,
    );
    const since = new Date().toISOString();

    const renew = await manager.ctx.post(`/renew/${controlID}`, { form: {} });
    expect(renew.status()).toBe(303);
    const revoke = await admin.ctx.post(`/revoke/${controlID}`, { form: {} });
    expect(revoke.status()).toBe(303);

    const after = await psql(
      `SELECT coalesce(serial, '') || '|' || coalesce(status, '') || '|' || coalesce(expires_at::text, '') FROM certificates WHERE id = ${controlID}`,
    );
    expect(after, "the row is unchanged").toBe(before);

    const caLog = await logs("step-ca", { since });
    const touched = caLog.split("\n").filter((l) => l.includes("/sign") || l.includes("/revoke")).length;
    expect(touched, `the CA was never contacted; log was:\n${caLog.slice(-1500)}`).toBe(0);
  });
});

test("E2E-CSRF-05: a token from a different session is rejected", async ({ manager, jarB }) => {
  // Two independent jars: no single-session bug and no globally shared token can
  // satisfy this property.
  await loginOrThrow(jarB, manager.creds);
  const tokenA = await manager.csrf("/issue");
  const name = disposableName("csrf05");

  const res = await jarB.post("/issue", {
    form: { name, domain: `${name}.e2e.invalid`, template: "server", key_type: "EC:P-256", duration: "24h", csrf_token: tokenA },
  });
  expect(res.status()).toBe(303);
  expect(res.headers()["location"]).toBe("/issue");

  const page = await jarB.get("/issue");
  expect(await page.text()).toContain(CSRF_FLASH);
  expect(await psql(`SELECT count(*) FROM certificates WHERE name = '${name}'`), "nothing was issued").toBe("0");
});
