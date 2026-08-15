import { APIRequestContext, request } from "@playwright/test";
import { BASE_URL } from "./env";
import { newJar } from "./session";

/**
 * Drives the authorization-code + PKCE round trip against
 * ghcr.io/navikt/mock-oauth2-server (compose.e2e-oidc.yml).
 *
 * The round trip is three hops, and the middle one is not ours: step-ui
 * redirects to the IdP, the IdP renders an interactive sign-in form, and its
 * submission redirects back to /auth/oidc/callback. Nothing here talks to the
 * IdP through the application, so the helper needs its own request context
 * pointed at the issuer.
 *
 * The state, nonce and PKCE verifier ride in the separate step-ui-oidc cookie
 * rather than in the session (D6 of plans/frontend-backend-split.md): the
 * session cookie is SameSite=Strict and a browser would not send it on the
 * cross-site navigation back from the provider. Passing the same jar through
 * every hop is therefore load-bearing, not incidental.
 */

/** The issuer as both the application and this helper must resolve it. */
export const OIDC_ISSUER = process.env.OIDC_ISSUER_URL ?? "http://mock-oidc:8080/default";

export interface OIDCClaims {
  /** The group values the app maps to a role through OIDC_GROUP_*. */
  groups?: string[];
  email?: string;
  preferred_username?: string;
  [claim: string]: unknown;
}

export interface OIDCLoginResult {
  /** The jar that carried the round trip, authenticated when it succeeded. */
  ctx: APIRequestContext;
  /** The status of the final callback response. */
  status: number;
  /** Where the callback sent the caller: "/" on success, "/login" on refusal. */
  location: string;
  /** The callback response body, for flash assertions. */
  body: string;
}

async function idpContext(): Promise<APIRequestContext> {
  return request.newContext({ ignoreHTTPSErrors: true });
}

/**
 * Starts the flow and returns the IdP authorize URL step-ui redirected to,
 * with the jar holding the round-trip cookie.
 */
export async function beginOIDCLogin(
  ctx?: APIRequestContext,
): Promise<{ ctx: APIRequestContext; authorizeURL: string }> {
  const jar = ctx ?? (await newJar(BASE_URL));
  const res = await jar.get("/auth/oidc/login", { maxRedirects: 0 });
  if (res.status() !== 302) {
    throw new Error(
      `GET /auth/oidc/login returned ${res.status()}, want 302. ` +
        `A 404 means OIDC_ENABLED is not true, so the routes were never registered (main.go).`,
    );
  }
  const authorizeURL = res.headers()["location"] ?? "";
  if (!authorizeURL.startsWith(OIDC_ISSUER)) {
    throw new Error(`authorize redirect went to ${authorizeURL}, which is not under ${OIDC_ISSUER}`);
  }
  return { ctx: jar, authorizeURL };
}

/**
 * Signs in at the mock IdP and returns the callback URL it redirects to.
 *
 * The mock server runs with interactiveLogin, so this posts its form rather
 * than following a redirect. Supplying `claims` overrides the server's
 * configured tokenCallbacks for this one authentication, which is how each
 * test picks the group membership it needs.
 */
export async function authenticateAtIdP(
  authorizeURL: string,
  subject: string,
  claims: OIDCClaims = {},
): Promise<string> {
  const idp = await idpContext();
  try {
    const form: Record<string, string> = { username: subject };
    if (Object.keys(claims).length > 0) form["claims"] = JSON.stringify(claims);

    const res = await idp.post(authorizeURL, { form, maxRedirects: 0 });
    if (res.status() !== 302) {
      throw new Error(`mock IdP sign-in returned ${res.status()}, want 302`);
    }
    const callback = res.headers()["location"] ?? "";
    if (!callback) throw new Error("mock IdP sign-in produced no Location header");
    return callback;
  } finally {
    await idp.dispose();
  }
}

/** Follows the callback on the jar that started the flow. */
export async function completeCallback(
  ctx: APIRequestContext,
  callbackURL: string,
): Promise<OIDCLoginResult> {
  // Only the path and query matter: the jar is already bound to BASE_URL, and
  // OIDC_REDIRECT_URL may name a host this harness cannot resolve.
  const url = new URL(callbackURL);
  const res = await ctx.get(`${url.pathname}${url.search}`, { maxRedirects: 0 });
  return {
    ctx,
    status: res.status(),
    location: res.headers()["location"] ?? "",
    body: await res.text(),
  };
}

/** The whole round trip, for the tests that do not need to tamper mid-flow. */
export async function oidcLogin(
  subject: string,
  claims: OIDCClaims = {},
  ctx?: APIRequestContext,
): Promise<OIDCLoginResult> {
  const { ctx: jar, authorizeURL } = await beginOIDCLogin(ctx);
  const callback = await authenticateAtIdP(authorizeURL, subject, claims);
  return completeCallback(jar, callback);
}

/**
 * Replays a callback with a tampered state parameter, which is the assertion
 * E2E-AUTH-08 step 4 makes. The round-trip cookie is left intact so that the
 * mismatch is the only difference from the passing case.
 */
export async function tamperedStateCallback(
  ctx: APIRequestContext,
  callbackURL: string,
): Promise<OIDCLoginResult> {
  const url = new URL(callbackURL);
  const state = url.searchParams.get("state") ?? "";
  url.searchParams.set("state", `${state}-tampered`);
  return completeCallback(ctx, url.toString());
}

/** The group names this installation maps, read from the environment the stack runs with. */
export function mappedGroups(): { admin: string; manager: string; viewer: string } {
  return {
    admin: process.env.OIDC_GROUP_ADMIN ?? "e2e-admins",
    manager: process.env.OIDC_GROUP_MANAGER ?? "e2e-managers",
    viewer: process.env.OIDC_GROUP_VIEWER ?? "e2e-viewers",
  };
}
