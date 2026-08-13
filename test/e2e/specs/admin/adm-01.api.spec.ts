import { expect, test } from "../../fixtures/test";

test("E2E-ADM-01: app.version pins the certificates library", async ({ admin }) => {
  const page = await admin.send(() => admin.ctx.get("/admin/console"));
  expect(page.status()).toBe(200);
  const pageBody = await page.text();
  expect(pageBody, "app.version is offered as a native entry").toContain("app.version");
  expect(pageBody, "ca.health is offered as a native entry").toContain("ca.health");

  const token = await admin.csrf("/admin/console");
  const res = await admin.send(() =>
    admin.ctx.post("/admin/console", { form: { command_id: "app.version", csrf_token: token } }),
  );
  expect([200, 302, 303]).toContain(res.status());

  const result = res.status() === 200 ? await res.text() : await (await admin.ctx.get("/admin/console")).text();

  // The second line only. The Dockerfile passes no -X ldflags, so the first
  // line's Version/BuildDate/GitCommit are compile-time defaults, and asserting
  // them would be asserting a constant. A "!= unknown" check on the library
  // version would pass against a downgrade.
  expect(result, "Risk R3: the pinned certificates library in the built image").toMatch(
    /smallstep\/certificates v0\.30\.2/,
  );

  const security = await admin.send(() => admin.ctx.get("/admin/security"));
  const securityBody = await security.text();
  // The audit row is the black-box shadow of the allowlist invariant: a native
  // command special-cased outside the common wrapper would still print output
  // but would emit no row.
  expect(securityBody).toContain("console.run id=app.version");
  expect(securityBody).toMatch(/console\.run id=app\.version[^<]*duration=\S+/);
});
