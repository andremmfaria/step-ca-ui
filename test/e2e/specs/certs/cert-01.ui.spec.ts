import { expect, test } from "@playwright/test";
import { BASE_URL } from "../../helpers/env";
import { FIXTURE_USERS } from "../../fixtures/test";

/**
 * The eleven-row issuance matrix lives in `api`. This companion exists for the
 * one property a request-level assertion cannot see: issue.html's template
 * picker sets the hidden `template`, `key_type` and `duration` inputs from
 * JavaScript, and a field-name drift between that script and
 * normalizeIssuePolicy would leave every `api` row green while the real form
 * silently submitted a default.
 *
 * The expected values are read from each card's own data attributes rather
 * than hardcoded, so the test asserts the wiring rather than restating the
 * catalogue and going stale the first time a duration changes.
 */
test("E2E-CERT-01: every template card sets the hidden inputs the handler reads", async ({ page }) => {
  const { username, password } = FIXTURE_USERS.manager;
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', username);
  await page.fill('input[name="password"]', password);
  await Promise.all([
    page.waitForURL(/\/(?!login)/),
    page.click('button[type="submit"], input[type="submit"]'),
  ]);

  await page.goto(`${BASE_URL}/issue`);
  const cards = page.locator(".template-card");
  const count = await cards.count();
  expect(count, "the picker renders its cards").toBeGreaterThan(1);

  const seen = new Set<string>();

  for (let i = 0; i < count; i++) {
    const card = cards.nth(i);
    const wanted = {
      template: await card.getAttribute("data-template"),
      keyType: await card.getAttribute("data-key"),
      duration: await card.getAttribute("data-duration"),
    };
    expect(wanted.template, `card ${i} declares a template`).toBeTruthy();

    await card.click();

    // Read the hidden inputs by the names the handler parses, not by id: an id
    // rename is cosmetic, a name rename silently changes what the server gets.
    const got = {
      template: await page.inputValue('input[name="template"]'),
      keyType: await page.inputValue('input[name="key_type"]'),
      duration: await page.inputValue('input[name="duration"]'),
    };

    expect(got.template, `clicking the ${wanted.template} card sets template`).toBe(wanted.template);
    expect(got.keyType, `clicking the ${wanted.template} card sets key_type`).toBe(wanted.keyType);
    expect(got.duration, `clicking the ${wanted.template} card sets duration`).toBe(wanted.duration);

    seen.add(`${got.template}|${got.keyType}|${got.duration}`);
  }

  // Distinct triples: if two cards produced the same one, a picker that had
  // stopped updating the inputs entirely would still pass every check above.
  expect(seen.size, "each card produces a distinct triple").toBe(count);
});
