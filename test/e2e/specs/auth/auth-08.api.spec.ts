import { psql } from "../../helpers/compose";
import { FLAGS } from "../../helpers/env";
import {
  authenticateAtIdP,
  beginOIDCLogin,
  mappedGroups,
  oidcLogin,
  tamperedStateCallback,
} from "../../helpers/oidc";
import { expect, test } from "../../fixtures/test";

// Every test on a flagged override stack skips with an explicit reason rather
// than passing silently when the stack is absent (Section 2.7.1).
test.describe("E2E-AUTH-08: OIDC login against a mock IdP", () => {
  test.skip(!FLAGS.oidc, "E2E_OIDC_ENABLED is unset: no mock IdP on the network");

  const groups = mappedGroups();

  test("E2E-AUTH-08: a mapped subject completes login and is created with the mapped role", async () => {
    const subject = "oidc-manager";
    const result = await oidcLogin(subject, { groups: [groups.manager], email: `${subject}@example.com` });

    expect(result.status, "a mapped subject completes the round trip").toBe(302);
    expect(result.location, "success lands on / rather than back at /login").toBe("/");

    // The positive control for the step-5 absence assertion below: this run
    // proves UpsertOIDCUser does create a row when the group maps.
    const role = await psql(
      `SELECT role FROM users WHERE username = '${subject}@example.com' AND auth_source = 'oidc'`,
    );
    expect(role, "UpsertOIDCUser created the row with the mapped role").toBe("manager");
  });

  test("E2E-AUTH-08: a tampered state is refused and creates no session", async () => {
    const subject = "oidc-tampered";
    const { ctx, authorizeURL } = await beginOIDCLogin();
    const callback = await authenticateAtIdP(authorizeURL, subject, { groups: [groups.manager] });

    const result = await tamperedStateCallback(ctx, callback);
    expect(result.status).toBe(302);
    expect(result.location, "a state mismatch goes back to /login").toBe("/login");

    // The flash is one-shot, so this GET both reads it and proves no session
    // was created: an authenticated jar would not be redirected off /.
    const afterwards = await ctx.get("/login");
    expect(await afterwards.text()).toContain("OIDC state mismatch");

    const home = await ctx.get("/", { maxRedirects: 0 });
    expect(home.status(), "no session exists, so / redirects to /login").toBe(302);
  });

  test("E2E-AUTH-08: an unmapped subject with no default role is denied and creates no user row", async () => {
    const subject = "oidc-unmapped-denied";
    const result = await oidcLogin(subject, { groups: ["a-group-nothing-maps"] });

    expect(result.location, "an unmapped subject is refused").toBe("/login");

    const afterwards = await result.ctx.get("/login");
    expect(await afterwards.text()).toContain("Access denied: your account is not in an authorised group");

    // Asserted directly against the table rather than inferred from the
    // redirect: a refusal that still created the row is the regression.
    const count = await psql(`SELECT count(*) FROM users WHERE username LIKE '${subject}%'`);
    expect(count, "a denied subject leaves no users row behind").toBe("0");
  });

  test("E2E-AUTH-08: admin takes precedence when a subject maps to several roles", async () => {
    const subject = "oidc-multi";
    const result = await oidcLogin(subject, {
      groups: [groups.viewer, groups.admin],
      email: `${subject}@example.com`,
    });
    expect(result.location).toBe("/");

    // mapGroupsToRole checks admin first, so the highest mapping wins rather
    // than whichever group happens to come first in the claim.
    const role = await psql(
      `SELECT role FROM users WHERE username = '${subject}@example.com' AND auth_source = 'oidc'`,
    );
    expect(role, "admin outranks the other mappings").toBe("admin");
  });

  test("E2E-AUTH-08: a later login re-synchronises a manually changed role", async ({ admin }) => {
    const subject = "oidc-resync";
    const username = `${subject}@example.com`;

    const first = await oidcLogin(subject, { groups: [groups.manager], email: username });
    expect(first.location).toBe("/");
    expect(await psql(`SELECT role FROM users WHERE username = '${username}'`)).toBe("manager");

    const token = await admin.csrf("/admin/users");
    const id = await psql(`SELECT id FROM users WHERE username = '${username}'`);
    const changed = await admin.send(() =>
      admin.ctx.post("/admin/users", {
        form: { action: "change_role", user_id: id, role: "viewer", csrf_token: token },
      }),
    );
    expect([302, 303]).toContain(changed.status());
    expect(await psql(`SELECT role FROM users WHERE username = '${username}'`)).toBe("viewer");

    // OIDC_SYNC_ROLE defaults to true, so the IdP is authoritative and the
    // manual demotion does not survive the next login.
    const second = await oidcLogin(subject, { groups: [groups.manager], email: username });
    expect(second.location).toBe("/");
    expect(
      await psql(`SELECT role FROM users WHERE username = '${username}'`),
      "the mapped role is restored on the next login",
    ).toBe("manager");
  });
});
