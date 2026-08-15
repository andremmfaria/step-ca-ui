import { psql } from "../../helpers/compose";
import { FLAGS } from "../../helpers/env";
import { mappedGroups, oidcLogin } from "../../helpers/oidc";
import { loginOrThrow, newJar } from "../../helpers/session";
import { createUser, disposableName, expect, test } from "../../fixtures/test";

/**
 * Walks the former V1 escalation chain and asserts it is refused at both of
 * the gates that now close it: the profile rename, and the OIDC upsert's
 * refusal to touch a local row.
 */
test.describe("E2E-AUTH-13: the viewer-to-admin escalation chain is refused at both gates", () => {
  test.skip(!FLAGS.oidc, "E2E_OIDC_ENABLED is unset: no mock IdP on the network");

  test("E2E-AUTH-13: neither the rename nor the upsert escalates a viewer", async ({ admin }) => {
    const groups = mappedGroups();
    const victim = disposableName("victim-viewer");
    const victimPassword = "Auth13-e2e-Pass1!";
    // The OIDC subject the attacker would like to become. Never logged in, so
    // no users row exists for it when the chain starts.
    const collidingName = disposableName("oidc-admin");
    const localPassword = "Auth13-e2e-Local1!";

    await createUser(admin, victim, victimPassword, "viewer");

    // ── Gate one: the profile rename ────────────────────────────────────────
    const viewerJar = await newJar();
    await loginOrThrow(viewerJar, { username: victim, password: victimPassword });

    const profile = await viewerJar.get("/profile");
    const csrf = /name="csrf_token"\s+value="([^"]+)"/.exec(await profile.text())?.[1] ?? "";
    expect(csrf, "the profile form carries a CSRF token").not.toBe("");

    const renamed = await viewerJar.post("/profile", {
      form: {
        action: "update_info",
        display_name: "Renamed By Test",
        username: collidingName,
        csrf_token: csrf,
      },
    });
    expect([302, 303]).toContain(renamed.status());

    const afterRename = await viewerJar.get("/profile");
    const afterRenameBody = await afterRename.text();
    expect(afterRenameBody, "the display name did change").toContain("Renamed By Test");

    // ProfilePost action=update_info reads no username field at all, so the
    // submitted value is ignored rather than rejected. Assert the username is
    // unchanged rather than looking for an error that is never shown.
    const stillNamed = await psql(
      `SELECT count(*) FROM users WHERE username = '${victim}' AND auth_source = 'local'`,
    );
    expect(stillNamed, "the rename did not take: the username is still the original").toBe("1");
    expect(
      await psql(`SELECT count(*) FROM users WHERE username = '${collidingName}'`),
      "no row was created under the attacker's chosen name",
    ).toBe("0");

    // ── Gate two: the OIDC upsert against a local row ───────────────────────
    await createUser(admin, collidingName, localPassword, "viewer");
    const hashBefore = await psql(`SELECT password_hash FROM users WHERE username = '${collidingName}'`);

    const result = await oidcLogin(collidingName, {
      groups: [groups.admin],
      preferred_username: collidingName,
    });
    expect(result.status).toBe(302);
    expect(result.location, "the colliding OIDC login does not log in").toBe("/login");
    expect(await (await result.ctx.get("/login")).text()).toContain(
      "Access denied: that username belongs to a local account",
    );

    // UpsertOIDCUser carries WHERE users.auth_source = 'oidc' on both DO UPDATE
    // branches, so the collision updates nothing. A silent promotion is exactly
    // what this test exists to catch, so the role is asserted explicitly rather
    // than inferred from the denial.
    expect(
      await psql(`SELECT role FROM users WHERE username = '${collidingName}'`),
      "the local account keeps its role",
    ).toBe("viewer");
    expect(
      await psql(`SELECT auth_source FROM users WHERE username = '${collidingName}'`),
      "the local account is not converted to an OIDC one",
    ).toBe("local");
    expect(
      await psql(`SELECT password_hash FROM users WHERE username = '${collidingName}'`),
      "the local password hash is untouched",
    ).toBe(hashBefore);

    // The denial is recorded, not merely enacted.
    const security = await admin.send(() => admin.ctx.get("/admin/security"));
    expect(await security.text()).toContain("OIDC: username collides with a local account");

    // ── The end of the chain: the local account is still only a viewer ──────
    const localJar = await newJar();
    await loginOrThrow(localJar, { username: collidingName, password: localPassword });
    const adminPage = await localJar.get("/admin");
    expect(adminPage.status(), "the account reached by local login is still a viewer").toBe(403);
  });
});
