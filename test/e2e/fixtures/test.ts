import { APIRequestContext, APIResponse, test as base } from "@playwright/test";
import { ADMIN_USERNAME, adminPassword, FLAGS } from "../helpers/env";
import { psql } from "../helpers/compose";
import {
  Credentials,
  Role,
  csrfTokenFrom,
  loginOrThrow,
  newJar,
  withLazyReauth,
} from "../helpers/session";

export const FIXTURE_USERS: Record<Exclude<Role, "admin">, Credentials> = {
  viewer: { username: "viewer_user", password: "Viewer-e2e-Pass1!" },
  manager: { username: "manager_user", password: "Manager-e2e-Pass1!" },
};

export const TWOFA_SUBJECT: Credentials = { username: "twofa_user", password: "Twofa-e2e-Pass1!" };

export function adminCredentials(): Credentials {
  return { username: ADMIN_USERNAME, password: adminPassword() };
}

export interface AuthedContext {
  ctx: APIRequestContext;
  creds: Credentials;
  /** GET/POST through this to get Section 4.1.4's lazy re-auth. */
  send(fn: () => Promise<APIResponse>): Promise<APIResponse>;
  csrf(url: string): Promise<string>;
}

async function authed(creds: Credentials): Promise<AuthedContext> {
  const ctx = await newJar();
  await loginOrThrow(ctx, creds);
  return {
    ctx,
    creds,
    send: (fn) => withLazyReauth(ctx, creds, fn),
    csrf: (url) => csrfTokenFrom(ctx, url),
  };
}

/**
 * Creates a user directly against the API as admin. Every test that logs a user
 * out, deletes one, or changes a role owns a disposable user of its own rather
 * than touching the shared fixtures (Section 4.1.4).
 */
export async function createUser(
  admin: AuthedContext,
  username: string,
  password: string,
  role: Role,
): Promise<Credentials> {
  const token = await admin.csrf("/admin/users");
  const res = await admin.send(() =>
    admin.ctx.post("/admin/users", {
      form: { action: "create", username, password, role, csrf_token: token },
    }),
  );
  if (res.status() !== 302 && res.status() !== 303) {
    throw new Error(`creating ${role} ${username} returned ${res.status()}: ${(await res.text()).slice(0, 300)}`);
  }
  return { username, password };
}

export async function ensureUser(
  admin: AuthedContext,
  creds: Credentials,
  role: Role,
): Promise<void> {
  const existing = await psql(`SELECT count(*) FROM users WHERE username = '${creds.username}'`);
  if (existing !== "0") return;
  await createUser(admin, creds.username, creds.password, role);
}

/** A name unique to one test run, so a re-run never collides with its own leftovers. */
export function disposableName(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
}

type Fixtures = {
  admin: AuthedContext;
  manager: AuthedContext;
  viewer: AuthedContext;
  jarB: APIRequestContext;
};

export const test = base.extend<{}, Fixtures>({
  admin: [
    async ({}, use) => {
      const a = await authed(adminCredentials());
      await use(a);
      await a.ctx.dispose();
    },
    { scope: "worker" },
  ],

  manager: [
    async ({}, use) => {
      const admin = await authed(adminCredentials());
      await ensureUser(admin, FIXTURE_USERS.manager, "manager");
      await admin.ctx.dispose();
      const m = await authed(FIXTURE_USERS.manager);
      await use(m);
      await m.ctx.dispose();
    },
    { scope: "worker" },
  ],

  viewer: [
    async ({}, use) => {
      const admin = await authed(adminCredentials());
      await ensureUser(admin, FIXTURE_USERS.viewer, "viewer");
      await admin.ctx.dispose();
      const v = await authed(FIXTURE_USERS.viewer);
      await use(v);
      await v.ctx.dispose();
    },
    { scope: "worker" },
  ],

  jarB: [
    async ({}, use) => {
      const ctx = await newJar();
      await use(ctx);
      await ctx.dispose();
    },
    { scope: "worker" },
  ],
});

export const expect = test.expect;

/** Section 3.0.2: an unavailable override stack skips with a reason, never passes silently. */
export function requireFlag(flag: keyof typeof FLAGS, tests: string): void {
  const envKey = { oidc: "E2E_OIDC_ENABLED", mail: "E2E_MAIL_ENABLED", le: "E2E_LE_ENABLED" }[flag];
  test.skip(!FLAGS[flag], `${tests} needs the ${flag} override stack; set ${envKey}=1 and compose it in`);
}

export { authed };
