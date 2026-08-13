import { compose, psql } from "../../helpers/compose";
import { expect, test } from "../../fixtures/test";

const PAGE_SIZE = 30;

/** One <tr> per entry inside the history table body, counted by its badge cell. */
function countEntries(html: string): number {
  return (html.match(/<span class="badge [^"]*">(issue|renew|revoke|import)<\/span>/g) ?? []).length;
}

function actionsIn(html: string): string[] {
  return [...html.matchAll(/<span class="badge [^"]*">(issue|renew|revoke|import)<\/span>/g)].map((m) => m[1]!);
}

test("E2E-HIST-01: history pagination", async ({ admin }) => {
  // The total is computed, never assumed: the genuine rows the certificate
  // suite wrote are what HIST-02 and HIST-03 depend on, so the table is seeded
  // rather than truncated.
  const before = Number(await psql("SELECT count(*) FROM cert_history"));
  const seed = PAGE_SIZE + 5 - (before % PAGE_SIZE);
  // The tag makes seeded names unique per run: generate_series restarts at 1
  // every time, so a bare index would look like an overlap between pages.
  const tag = Date.now().toString(36);
  const seeded = await compose(
    "exec",
    "-T",
    "postgres",
    "psql",
    "-v",
    "ON_ERROR_STOP=1",
    "-U",
    "stepui",
    "-d",
    "stepui",
    "-c",
    `INSERT INTO cert_history (action, cert_name, domain, details, username, role)
     SELECT 'issue', 'e2e-seed-${tag}-' || g, 'seed' || g || '.e2e.invalid', 'synthetic', 'e2e-seed', 'admin'
     FROM generate_series(1, ${seed}) AS g;`,
  );
  expect(seeded.code, `seeding ${seed} rows: ${seeded.stderr}`).toBe(0);

  const total = Number(await psql("SELECT count(*) FROM cert_history"));
  const expectedPages = Math.ceil(total / PAGE_SIZE);

  const page1 = await admin.send(() => admin.ctx.get("/history"));
  expect(page1.status()).toBe(200);
  const body1 = await page1.text();
  expect(countEntries(body1), "page 1 is full").toBe(PAGE_SIZE);

  const page2 = await admin.send(() => admin.ctx.get("/history?page=2"));
  const body2 = await page2.text();
  const expectedOnPage2 = Math.min(PAGE_SIZE, total - PAGE_SIZE);
  expect(countEntries(body2), `page 2 holds the remainder of ${total} rows`).toBe(expectedOnPage2);
  expect(expectedPages, "more than one page exists, so page 2 is meaningful").toBeGreaterThan(1);

  // The two pages must be disjoint: a page parameter that is silently ignored
  // returns a full, identical first page and would pass a count-only check.
  const seedName = new RegExp(`e2e-seed-${tag}-\\d+`, "g");
  const names1 = [...body1.matchAll(seedName)].map((m) => m[0]);
  const names2 = [...body2.matchAll(seedName)].map((m) => m[0]);
  const overlap = names1.filter((n) => names2.includes(n));
  expect(overlap, "page 1 and page 2 share no entries").toEqual([]);
});

test("E2E-HIST-02: the history action filter", async ({ admin }) => {
  const filtered = await admin.send(() => admin.ctx.get("/history?action=issue&action=revoke"));
  const filteredBody = await filtered.text();
  const filteredActions = actionsIn(filteredBody);

  // The control is scoped by certificate name rather than left wholly
  // unfiltered: E2E-HIST-01's seeded rows are newer than E2E-CERT-04's renew
  // and would push it off page 1, which would make this clause fail for a
  // reason unrelated to the action filter. It applies no action filter, which
  // is the part under test here.
  const control = await admin.send(() => admin.ctx.get("/history?cert=e2e-dur-8760h"));
  const controlActions = actionsIn(await control.text());

  expect(filteredActions.length, "zero rows would satisfy the exclusion trivially").toBeGreaterThan(0);
  expect(filteredActions.filter((a) => a === "renew"), "no renew survives the filter").toEqual([]);
  // Without this third clause the exclusion is unfalsifiable: GetHistory's
  // error is discarded (handlers/history.go:30), so a broken query and a
  // working filter look identical.
  expect(controlActions, "a renew row is visible when no action filter is applied").toContain("renew");
});

test("E2E-HIST-03: the history certificate-name filter", async ({ admin }) => {
  const subject = "e2e-server-ec-p256";

  const filtered = await admin.send(() => admin.ctx.get(`/history?cert=${subject}`));
  const filteredBody = await filtered.text();
  const names = [...filteredBody.matchAll(/<td[^>]*>\s*([A-Za-z0-9._-]+)\s*<\/td>/g)].map((m) => m[1]!);

  expect(countEntries(filteredBody), "the filter returns something").toBeGreaterThan(0);
  expect(filteredBody).toContain(subject);
  expect(names.filter((n) => n.startsWith("e2e-") && n !== subject), "no other certificate survives").toEqual([]);

  const unfiltered = await admin.send(() => admin.ctx.get("/history"));
  const unfilteredBody = await unfiltered.text();
  expect(unfilteredBody.includes("e2e-seed-") || /e2e-(dur|client|wildcard|internal)/.test(unfilteredBody),
    "the unfiltered view carries entries for a different certificate").toBe(true);
});
