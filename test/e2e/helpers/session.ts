import { APIRequestContext, APIResponse, request } from "@playwright/test";
import { BASE_URL } from "./env";

export type Role = "viewer" | "manager" | "admin";

export interface Credentials {
  username: string;
  password: string;
}

/**
 * "Jar A" and "jar B" in Section 3 mean two independent contexts with their own
 * cookie storage. Several properties only hold across two of them (E2E-CSRF-05,
 * E2E-AUTH-15), so the isolation is part of the assertion.
 */
export async function newJar(baseURL = BASE_URL): Promise<APIRequestContext> {
  return request.newContext({ baseURL, ignoreHTTPSErrors: true, maxRedirects: 0 });
}

/** Every state-changing request carries the token rendered into the page it came from. */
export async function csrfTokenFrom(ctx: APIRequestContext, url: string): Promise<string> {
  const res = await ctx.get(url);
  const body = await res.text();
  return extractCSRF(body, url);
}

export function extractCSRF(html: string, whence = "<page>"): string {
  const m = /name="csrf_token"\s+value="([^"]+)"/.exec(html);
  if (!m || !m[1]) {
    throw new Error(`no csrf_token rendered in ${whence}; body starts: ${html.slice(0, 400)}`);
  }
  return m[1];
}

export async function login(ctx: APIRequestContext, creds: Credentials): Promise<APIResponse> {
  const token = await csrfTokenFrom(ctx, "/login");
  return ctx.post("/login", {
    form: { username: creds.username, password: creds.password, csrf_token: token },
  });
}

export async function loginOrThrow(ctx: APIRequestContext, creds: Credentials): Promise<void> {
  const res = await login(ctx, creds);
  // A successful login redirects; the failure path re-renders /login at 200
  // with the error inline, so a status check alone never identifies it.
  if (res.status() !== 302 && res.status() !== 303) {
    const body = await res.text();
    throw new Error(`login as ${creds.username} failed with ${res.status()}: ${body.slice(0, 400)}`);
  }
}

export async function logout(ctx: APIRequestContext): Promise<APIResponse> {
  const token = await csrfTokenFrom(ctx, "/dashboard");
  return ctx.post("/logout", { form: { csrf_token: token } });
}

/**
 * Standing rule from Section 4.1.4: on a 302 to /login from a fixture-backed
 * context, re-run the login once and retry the request once, rather than
 * surfacing the redirect as the test's own result. Nothing in Section 3 logs
 * the shared fixture users out, but a role change or a session-epoch bump on
 * one of them would otherwise break every later test in the same worker.
 */
export async function withLazyReauth<T extends APIResponse>(
  ctx: APIRequestContext,
  creds: Credentials,
  send: () => Promise<T>,
): Promise<T> {
  const first = await send();
  if (!redirectsToLogin(first)) return first;
  await loginOrThrow(ctx, creds);
  return send();
}

export function redirectsToLogin(res: APIResponse): boolean {
  if (res.status() !== 302 && res.status() !== 303) return false;
  const location = res.headers()["location"] ?? "";
  return location.startsWith("/login");
}

/** gorilla/sessions names the cookie after store.Get(r, "step-ui") (handlers/handler.go:261). */
export const SESSION_COOKIE_NAME = "step-ui";

/** The raw session cookie, which E2E-AUTH-12 captures and replays after a logout. */
export async function sessionCookie(ctx: APIRequestContext): Promise<string> {
  const state = await ctx.storageState();
  const cookie = state.cookies.find((c) => c.name === SESSION_COOKIE_NAME);
  if (!cookie) {
    throw new Error(`no ${SESSION_COOKIE_NAME} cookie in jar; cookies present: ${state.cookies.map((c) => c.name).join(", ")}`);
  }
  return `${cookie.name}=${cookie.value}`;
}
