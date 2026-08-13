import { psql } from "../../helpers/compose";
import { csrfTokenFrom, loginOrThrow, newJar } from "../../helpers/session";
import { certificateID } from "../../helpers/certs";
import { createUser, disposableName, expect, test } from "../../fixtures/test";

const PAGE_SIZE = 30;

function countRows(html: string): number {
  return (html.match(/<span class="badge [^"]*">(Login|Logout|Audit|Denied|2FA|Reset)<\/span>/g) ?? []).length;
}

function labelsIn(html: string): string[] {
  return [...html.matchAll(/<span class="badge [^"]*">(Login|Logout|Audit|Denied|2FA|Reset)<\/span>/g)].map((m) => m[1]!);
}

test("E2E-SEC-01: security-log pagination, search and filter", async ({ admin }) => {
  test.setTimeout(180_000);

  // This test owns its subject. Seeding rows by logging the shared admin
  // fixture in and out would bump its session_epoch and evict the worker-scoped
  // fixture the rest of the run depends on.
  const username = disposableName("sec01");
  const password = "Sec01-e2e-Pass1!";
  await createUser(admin, username, password, "viewer");

  const jar = await newJar();
  try {
    // One deliberate failure, so a success=false row is guaranteed to exist at
    // this slot. E2E-AUTH-02 makes five of these later against its own user and
    // its own five-attempt budget; this one does not touch either.
    const token = await csrfTokenFrom(jar, "/login");
    const denied = await jar.post("/login", { form: { username, password: "wrong-on-purpose", csrf_token: token } });
    expect(denied.status()).toBe(302);

    let total = Number(await psql("SELECT count(*) FROM auth_log"));
    // A second page has to exist for ?page=2 to mean anything, and nothing so
    // far in the run guarantees 31 rows.
    while (total < PAGE_SIZE + 1) {
      await loginOrThrow(jar, { username, password });
      const t = await csrfTokenFrom(jar, "/");
      await jar.post("/logout", { form: { csrf_token: t } });
      total = Number(await psql("SELECT count(*) FROM auth_log"));
    }

    const okTotal = Number(await psql("SELECT count(*) FROM auth_log WHERE success"));
    const failTotal = Number(await psql("SELECT count(*) FROM auth_log WHERE NOT success"));
    expect(okTotal + failTotal, "TotalOK and TotalFail sum to the unfiltered total").toBe(total);

    const page1 = await admin.send(() => admin.ctx.get("/admin/security"));
    expect(page1.status()).toBe(200);
    const body1 = await page1.text();
    expect(countRows(body1), "page 1 is full").toBe(PAGE_SIZE);

    const page2 = await admin.send(() => admin.ctx.get("/admin/security?page=2"));
    const body2 = await page2.text();
    expect(countRows(body2), "page 2 carries the remainder, never an empty page").toBe(
      Math.min(PAGE_SIZE, total - PAGE_SIZE),
    );

    const searched = await admin.send(() => admin.ctx.get(`/admin/security?q=${username}`));
    const searchedBody = await searched.text();
    expect(countRows(searchedBody), "the username search matches this test's own rows").toBeGreaterThan(0);
    expect(searchedBody).toContain(username);

    const ok = await admin.send(() => admin.ctx.get("/admin/security?filter=ok"));
    const okBody = await ok.text();
    expect(countRows(okBody)).toBeGreaterThan(0);
    expect(labelsIn(okBody), "a success-only view carries no Denied row").not.toContain("Denied");

    const fail = await admin.send(() => admin.ctx.get("/admin/security?filter=fail"));
    const failBody = await fail.text();
    expect(countRows(failBody), "the failure this test produced is here").toBeGreaterThan(0);
    expect(new Set(labelsIn(failBody)), "every row in a failure-only view is Denied").toEqual(new Set(["Denied"]));

    // db/authlog.go accepts only "ok" and "fail"; anything else must behave as
    // unfiltered rather than erroring or returning nothing.
    const garbage = await admin.send(() => admin.ctx.get("/admin/security?filter=garbage"));
    expect(garbage.status()).toBe(200);
    expect(countRows(await garbage.text()), "an unrecognised filter is unfiltered").toBe(countRows(body1));

    // Only the labels this slot can produce: Login and Logout from the fixtures
    // and the auth smoke tests, Audit from E2E-CERT-09's key download. 2FA,
    // Reset and Denied-in-bulk come from tests that run later.
    const labels = new Set(labelsIn(body1).concat(labelsIn(searchedBody)));
    expect(labels.has("Login") || labels.has("Logout"), `saw labels: ${[...labels].join(", ")}`).toBe(true);
  } finally {
    await jar.dispose();
  }
});

test("E2E-SEC-02: audited privileged actions carry the Audit prefix and the right payload", async ({ admin, manager }) => {
  const name = "e2e-server-ec-p256";
  const id = await certificateID(name);
  const domain = await psql(`SELECT domain FROM certificates WHERE id = ${id}`);

  const download = await manager.send(() => manager.ctx.get(`/download/key/${id}`));
  expect(download.status(), "the key download that writes the audit row").toBe(200);

  const token = await admin.csrf("/admin/console");
  const console_ = await admin.send(() =>
    admin.ctx.post("/admin/console", { form: { command_id: "app.version", csrf_token: token } }),
  );
  expect([200, 302, 303]).toContain(console_.status());

  const page = await admin.send(() => admin.ctx.get("/admin/security"));
  const body = await page.text();

  // Field by field, not a prefix match: a HasPrefix check round-trips and would
  // pass against a row written for a different certificate entirely.
  expect(body).toContain(`certificate.key_download id=${id} name=${name} domain=${domain}`);
  expect(body).toMatch(/console\.run id=app\.version command=[^<]*exit=0 timeout=false duration=\S+/);

  const auditRows = [...body.matchAll(/<span class="badge ([^"]*)">Audit<\/span>/g)].map((m) => m[1]!);
  expect(auditRows.length, "both rows render under the Audit label").toBeGreaterThanOrEqual(2);
  expect(auditRows.every((cls) => cls.includes("warn")), `audit badges were: ${auditRows.join(", ")}`).toBe(true);
});
