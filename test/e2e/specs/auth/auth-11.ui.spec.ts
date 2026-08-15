import { expect, test } from "@playwright/test";
import { BASE_URL } from "../../helpers/env";
import { adminCredentials } from "../../fixtures/test";

/**
 * The `api` steps of E2E-AUTH-11 prove the route contract: a GET logs nobody
 * out, a token-less POST is refused, a tokened POST ends the session. This
 * companion proves an operator can actually reach it.
 *
 * Logout is a POST from an inline form in both base templates. A form that
 * renders but does not submit, or whose token is stale on a cached page, is
 * invisible to every request-level assertion.
 */
const PAGES_BY_TEMPLATE = [
  { template: "base.html", path: "/" },
  { template: "admin_base.html", path: "/admin" },
];

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  const { username, password } = adminCredentials();
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="username"]', username);
  await page.fill('input[name="password"]', password);
  await Promise.all([page.waitForURL(/\/(?!login)/), page.click('button[type="submit"], input[type="submit"]')]);
}

test.describe("E2E-AUTH-11: the logout control works from every base template", () => {
  for (const { template, path } of PAGES_BY_TEMPLATE) {
    test(`E2E-AUTH-11: logout submits from ${template} (${path})`, async ({ page }) => {
      await signIn(page);
      await page.goto(`${BASE_URL}${path}`);
      expect(page.url(), `${path} should render, not bounce to /login`).not.toContain("/login");

      // Asserted as a real form rather than a link: a GET cannot log anyone
      // out, so a regression to an <a href="/logout"> would silently do nothing.
      const form = page.locator('form[action="/logout"]');
      await expect(form, `${template} renders a logout form`).toHaveCount(1);
      await expect(
        form.locator('input[name="csrf_token"]'),
        "the logout form carries a CSRF token, or the POST is refused",
      ).toHaveCount(1);

      await Promise.all([
        page.waitForURL(/\/login/),
        form.locator('button[type="submit"], input[type="submit"]').first().click(),
      ]);
      expect(page.url(), "submitting the form lands on /login").toContain("/login");

      // The session really ended: a protected page bounces rather than rendering.
      await page.goto(`${BASE_URL}${path}`);
      expect(page.url(), "the session ended, so the page redirects to /login").toContain("/login");
    });
  }
});
