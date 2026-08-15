import { expect, test } from "@playwright/test";
import { BASE_URL } from "../../helpers/env";
import { decodeQR, secretFromOtpauth } from "../../helpers/qr";
import { totpNow } from "../../helpers/totp";
import { TWOFA_SUBJECT } from "../../fixtures/test";

/**
 * The `api` steps of E2E-AUTH-04 deliberately take the plaintext path, reading
 * the pending secret from the page's readonly input. Nothing there would
 * notice a QR image that encoded the wrong value or failed to render at all,
 * because that is a property of a rendered PNG.
 *
 * The `ui` project runs after `api` completes, by which point E2E-AUTH-07's
 * mandatory teardown has disabled TOTP on the dedicated subject, so this
 * starts its own enrolment and tears it down again rather than depending on
 * or leaving behind a pending one.
 */
test("E2E-AUTH-04: the QR image encodes the same secret the page renders", async ({ page, request }) => {
  const { username, password } = TWOFA_SUBJECT;

  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', username);
  await page.fill('input[name="password"]', password);
  await Promise.all([
    page.waitForURL(/\/(?!login)/),
    page.click('button[type="submit"], input[type="submit"]'),
  ]);

  // A fresh enrolment of this test's own, so it does not depend on whatever
  // the api project left behind.
  await page.goto(`${BASE_URL}/profile/2fa`);
  const startToken = await page.inputValue('form[action="/profile/2fa/start"] input[name="csrf_token"]');
  await page.evaluate((token) => {
    const form = document.querySelector('form[action="/profile/2fa/start"]') as HTMLFormElement | null;
    if (!form) throw new Error("no enrolment form on /profile/2fa");
    (form.querySelector('input[name="csrf_token"]') as HTMLInputElement).value = token;
    form.submit();
  }, startToken);
  await page.waitForURL(/\/profile\/2fa/);

  const rendered = await page.inputValue("input[readonly]");
  expect(rendered, "the page renders a pending secret").not.toBe("");

  // The image is fetched through the page's own session, then decoded. A
  // broken render is a 200 with unreadable bytes, which no status assertion
  // would catch.
  const img = page.locator('img[src="/profile/2fa/qr"]');
  await expect(img, "the enrolment page renders the QR image").toHaveCount(1);
  await expect(img).toHaveJSProperty("complete", true);
  const naturalWidth = await img.evaluate((el) => (el as HTMLImageElement).naturalWidth);
  expect(naturalWidth, "the QR image actually decoded in the browser").toBeGreaterThan(0);

  const cookies = await page.context().cookies();
  const png = await request.get(`${BASE_URL}/profile/2fa/qr`, {
    headers: { cookie: cookies.map((c) => `${c.name}=${c.value}`).join("; ") },
  });
  expect(png.status()).toBe(200);

  const encoded = secretFromOtpauth(decodeQR(Buffer.from(await png.body())));
  // Two independent renderings of one secret must agree.
  expect(encoded, "the QR encodes the secret the page shows").toBe(rendered);

  // Tear down this test's own enrolment rather than leaving the subject
  // mid-enrolment: nothing later in the ui project depends on it staying.
  const code = await totpNow(rendered);
  await page.goto(`${BASE_URL}/profile/2fa`);
  const disableForm = page.locator('form[action="/profile/2fa/disable"]');
  if ((await disableForm.count()) > 0) {
    await disableForm.locator('input[name="password"]').fill(password);
    await disableForm.locator('input[name="code"]').fill(code);
    await disableForm.locator('button[type="submit"], input[type="submit"]').first().click();
    await page.waitForURL(/\/profile/);
  }
});
