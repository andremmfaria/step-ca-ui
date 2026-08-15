import { psql, recreate } from "../../helpers/compose";
import { FLAGS } from "../../helpers/env";
import { oidcLogin } from "../../helpers/oidc";
import { disposableName, expect, test } from "../../fixtures/test";

/**
 * mapGroupsToRole returning empty is not automatically a denial: the
 * configured OIDC_DEFAULT_ROLE is consulted first, and only an equally-empty
 * default falls through to the refusal branch. E2E-AUTH-08 covers that
 * empty-default denial; this covers the configured-default case, plus what
 * OIDC_SYNC_ROLE=false does to a later login for the same subject.
 */
test.describe("E2E-AUTH-16: the default role applies, and a disabled sync does not revert a change", () => {
  test.skip(!FLAGS.oidc, "E2E_OIDC_ENABLED is unset: no mock IdP on the network");

  // Recreating step-ui twice is minutes of stack time rather than test time.
  test.describe.configure({ timeout: 10 * 60_000 });

  test("E2E-AUTH-16: an unmapped subject gets the default role and keeps a manual promotion", async ({
    admin,
  }) => {
    const subject = disposableName("oidc-unmapped");
    const username = `${subject}@example.com`;

    // The leg's stock value is restored in teardown regardless of outcome, so
    // a failure here does not leave the next test running against a stack
    // whose sync is still disabled.
    let syncDisabled = false;
    try {
      await recreate("step-ui", { OIDC_DEFAULT_ROLE: "viewer", OIDC_SYNC_ROLE: "true" });

      // ── The default role applies where no group maps ─────────────────────
      const first = await oidcLogin(subject, {
        groups: ["a-group-nothing-maps"],
        email: username,
      });
      expect(first.status).toBe(302);
      expect(
        first.location,
        "an unmapped subject with a configured default is admitted, not refused",
      ).toBe("/");

      expect(await psql(`SELECT role FROM users WHERE username = '${username}'`)).toBe("viewer");
      expect(await psql(`SELECT auth_source FROM users WHERE username = '${username}'`)).toBe("oidc");

      // ── A manual promotion survives a login once sync is off ─────────────
      const id = await psql(`SELECT id FROM users WHERE username = '${username}'`);
      const token = await admin.csrf("/admin/users");
      const promoted = await admin.send(() =>
        admin.ctx.post("/admin/users", {
          form: { action: "change_role", user_id: id, role: "manager", csrf_token: token },
        }),
      );
      expect([302, 303]).toContain(promoted.status());
      expect(await psql(`SELECT role FROM users WHERE username = '${username}'`)).toBe("manager");

      // A restart would not apply the change: the value is read at startup, so
      // the container has to be recreated.
      syncDisabled = true;
      await recreate("step-ui", { OIDC_DEFAULT_ROLE: "viewer", OIDC_SYNC_ROLE: "false" });

      const second = await oidcLogin(subject, {
        groups: ["a-group-nothing-maps"],
        email: username,
      });
      expect(second.location, "the subject can still log in").toBe("/");

      // With OIDC_SYNC_ROLE=false, UpsertOIDCUser's DO UPDATE omits role from
      // its SET list entirely, so a second login for a still-unmapped subject
      // does not walk the admin's promotion back to the default.
      expect(
        await psql(`SELECT role FROM users WHERE username = '${username}'`),
        "a disabled sync leaves the manual promotion in place",
      ).toBe("manager");
    } finally {
      if (syncDisabled) {
        await recreate("step-ui", { OIDC_DEFAULT_ROLE: "viewer", OIDC_SYNC_ROLE: "true" });
      }
    }
  });
});
