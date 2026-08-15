import { expect, test } from "@playwright/test";
import { BASE_URL } from "../../helpers/env";
import { adminCredentials } from "../../fixtures/test";

/**
 * The CSP header is compared exactly in `api`. That proves the policy is the
 * intended string; it cannot prove the intended string does not block an asset
 * a page actually needs. A directive that breaks a stylesheet changes no
 * status code and no header, so it is invisible to every request-level check.
 *
 * This loads each page template with a browser and asserts zero console errors
 * and zero securitypolicyviolation events.
 */
const PAGES = [
  "/",
  "/certificates",
  "/issue",
  "/import",
  "/provisioners",
  "/history",
  "/profile",
  "/profile/2fa",
  "/admin",
  "/admin/users",
  "/admin/users-temp",
  "/admin/activity",
  "/admin/security",
  "/admin/console",
  "/admin/about",
  "/admin/integrity",
  "/admin/backup",
  "/admin/notifications",
  "/le",
  "/le/issue",
  "/le/settings",
  "/le/logs",
];

test.describe("E2E-CFG-01: the CSP blocks nothing a page needs", () => {
  test.beforeEach(async ({ page }) => {
    const { username, password } = adminCredentials();
    await page.goto(`${BASE_URL}/login`);
    await page.fill('input[name="username"]', username);
    await page.fill('input[name="password"]', password);
    await Promise.all([
      page.waitForURL(/\/(?!login)/),
      page.click('button[type="submit"], input[type="submit"]'),
    ]);
  });

  for (const path of PAGES) {
    test(`E2E-CFG-01: ${path} loads with no console error and no CSP violation`, async ({ page }) => {
      const consoleErrors: string[] = [];
      const violations: string[] = [];

      page.on("console", (msg) => {
        if (msg.type() === "error") consoleErrors.push(msg.text());
      });
      // pageerror catches an uncaught exception, which a blocked inline script
      // produces without ever reaching the console listener above.
      page.on("pageerror", (err) => consoleErrors.push(String(err)));

      // The violation event fires in the page, so the collector has to exist
      // before navigation rather than being read afterwards.
      await page.addInitScript(() => {
        (window as unknown as { __cspViolations: string[] }).__cspViolations = [];
        document.addEventListener("securitypolicyviolation", (e) => {
          const ev = e as SecurityPolicyViolationEvent;
          (window as unknown as { __cspViolations: string[] }).__cspViolations.push(
            `${ev.violatedDirective} blocked ${ev.blockedURI}`,
          );
        });
      });

      const res = await page.goto(`${BASE_URL}${path}`, { waitUntil: "networkidle" });
      expect(res?.status(), `${path} should render for an admin`).toBeLessThan(400);
      expect(page.url(), `${path} should not bounce to /login`).not.toContain("/login");

      violations.push(
        ...(await page.evaluate(
          () => (window as unknown as { __cspViolations?: string[] }).__cspViolations ?? [],
        )),
      );

      expect(violations, `${path} triggered CSP violations`).toEqual([]);
      expect(consoleErrors, `${path} logged console errors`).toEqual([]);
    });
  }
});
