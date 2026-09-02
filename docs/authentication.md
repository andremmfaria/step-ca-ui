# Authentication

Local login, OIDC SSO, session and CSRF cookie behaviour, two-factor authentication, and password reset.

Step-CA UI supports local username/password login and OIDC SSO. Both can be active at once. Roles (`viewer`, `manager`, `admin`) are described in [docs/architecture.md](architecture.md#roles).

## Local login

On by default (`LOCAL_LOGIN_ENABLED=true`). Keep it enabled while configuring OIDC, disabling it before SSO is verified working will lock you out. It doubles as a break-glass path if the IdP is unreachable.

Password policy (`backend/security/security.go`, `ValidatePassword`): 8 to 72 characters, at least one digit, one letter, and one of `` +!@#$%^&*()_-=[]{}|;:,.<>? ``.

**Login lockout.** 5 failed attempts from the same IP within a 5-minute sliding window blocks that IP from logging in (`backend/security/security.go`). The block lifts as soon as attempts age out of the window, not on a fixed timer. The login page's error text ("Too many attempts. Please wait 5 minutes.", `security.LockoutMessage`) is formatted from the `LimitWindow` constant, and the API's 429 `Retry-After` header is computed from the actual time remaining until the IP's oldest counted attempt ages out (`security.RateLimiter.RetryAfter`), not a fixed duration. A failed TOTP code or recovery code also registers as an attempt against the same limiter. An admin can clear a blocked IP early from that user's admin profile page, when it shows as blocked (the `unblock_ip` action, `backend/handlers/users.go:127-131`).

Passwords are hashed with bcrypt at the default cost. A legacy SHA-256 hash (from before the bcrypt migration) is still accepted on login and transparently rehashed to bcrypt afterward. `NeedsPasswordRehash` also flags a bcrypt hash whose cost has fallen below the current default, or a hash `bcrypt.Cost` cannot parse at all.

TOTP 2FA (below) applies to local accounts only. SSO users rely on their IdP's own MFA.

## OIDC SSO

Set `OIDC_ENABLED=true` to activate SSO. The implementation is authorization code + PKCE, and validates state, nonce, and the ID token signature against the issuer's JWKS.

**This repository's stock `docker-compose.yml` does not wire any `OIDC_*` variable, or `LOCAL_LOGIN_ENABLED`, into the `step-ui` container.** Setting them in `.env` has no effect until you add the matching `environment:` lines to the `step-ui` service yourself, `compose.e2e-oidc.yml` (used by CI against a mock IdP) is a working example of the full key list. See [docs/configuration.md](configuration.md#oidc-sso) for the complete variable table and defaults.

**Routes registered when `OIDC_ENABLED=true`** (`backend/main.go`):

| Route | Purpose |
|---|---|
| `GET /auth/oidc/login` | Initiates the authorization code + PKCE flow |
| `GET /auth/oidc/callback` | Receives the provider redirect and completes login |

### Group-to-role mapping

The ID token claim named by `OIDC_GROUP_CLAIM` (default `groups`) carries the user's group memberships. `mapGroupsToRole` (`backend/handlers/oidc.go`) checks them in this precedence order:

```
admin > manager > viewer
```

If no configured group name matches and `OIDC_DEFAULT_ROLE` is empty, access is denied and the login attempt is logged. Set `OIDC_DEFAULT_ROLE` to `viewer` only if every authenticated IdP user should get read access. `main.go` refuses to start if `OIDC_ENABLED=true` and `OIDC_DEFAULT_ROLE` is set to anything other than `viewer`, `manager`, `admin`, or empty, the check is skipped when `OIDC_ENABLED` is false.

An OIDC login whose username matches an existing **local** account is denied outright (`ErrOIDCLocalUser`, `backend/handlers/oidc.go:197-201`), rather than silently taking over that account: `UpsertOIDCUser`'s `INSERT ... ON CONFLICT` only updates a row whose `auth_source` is already `'oidc'` (`backend/db/users.go:286,298`), so a conflicting local username is left untouched and the login attempt fails with "that username belongs to a local account".

When `OIDC_SYNC_ROLE=true` (default), the role is updated on every login so IdP group membership stays authoritative. Set it `false` to preserve roles assigned manually inside the app.

### JumpCloud setup checklist

The application code is IdP-agnostic (any standards-compliant OIDC provider works), JumpCloud is simply what `.env.example` documents:

1. SSO > + Add New Application > Custom OIDC App
2. Set Redirect URI to `https://<your-host>/auth/oidc/callback`
3. Copy Client ID and Client Secret into your compose environment
4. Add a group attribute with Attribute Name `groups`
5. Set `OIDC_GROUP_ADMIN` / `OIDC_GROUP_MANAGER` / `OIDC_GROUP_VIEWER` to the JumpCloud group names

## Session and CSRF cookies

Cookie names carry the `__Host-` prefix whenever `SESSION_SECURE` is true (the default), which requires `Secure`, `Path=/`, and no `Domain` attribute, exactly what the cookie is already set with:

| Cookie | Base name | Served name (SESSION_SECURE=true) | `SameSite` |
|---|---|---|---|
| Session | `step-ui` | `__Host-step-ui` | `Strict` |
| CSRF (readable) | `step-ui-csrf` | `__Host-step-ui-csrf` | `Strict` |
| OIDC round-trip state | `step-ui-oidc` | `__Host-step-ui-oidc` | `Lax` (must survive the cross-site redirect back from the IdP) |

The session cookie is `HttpOnly`. The CSRF cookie is deliberately not, so client-side JavaScript can read it and echo it back as a request header on POST/PUT/DELETE.

The OIDC round-trip cookie carries the flow's state, nonce and PKCE verifier and expires after 300 seconds (`backend/handlers/handler.go:324`), long enough for a normal IdP redirect, short enough to limit a leaked cookie's usefulness. It is cleared once the callback consumes it.

The CSRF token itself is checked two ways depending on the caller: the JSON API requires the readable cookie's value echoed back in an `X-CSRF-Token` header on every unsafe method (`backend/api/middleware.go:133-155`), while the server-rendered forms carry it as a `csrf_token` form field instead (`backend/handlers/handler.go:409-416`).

**Timeouts** (`backend/middleware/middleware.go`): an 8-hour idle (sliding) window, `SessionTimeout`, and a 24-hour absolute cap from creation, `SessionMaxLifetime`, whichever is hit first. `GET /api/v1/session` is exempt from the sliding-window renewal so an open tab cannot keep a session alive indefinitely.

**Session epoch.** Each user row carries a `session_epoch` integer. Bumping it invalidates every session cookie already issued to that user on the next request, even ones inside their idle window, because the epoch stored in the cookie no longer matches the row. The true list of bump sites, confirmed against every `session_epoch`/`BumpSessionEpoch` reference in `backend/`:

- Logout, `POST /logout` (`appdb.BumpSessionEpoch`, `backend/handlers/auth.go:224`)
- An admin resetting another user's password (`backend/handlers/users.go:142`)
- A user changing their own password (`backend/handlers/users.go:272`)
- Completing an emailed password reset (`backend/handlers/password_reset.go:200`)
- An admin changing a user's role (`UpdateUserRole`, `backend/db/users.go:115`, increments `session_epoch` in the same `UPDATE` as the role change)
- An admin deactivating a user (`UpdateUserActive`, `backend/db/users.go:122`, same pattern)
- A temporary user's automatic expiry (`ExpireOverdueTempUsers`, `backend/db/users.go:371`, same pattern)

It is **not** bumped on a 2FA (TOTP) enrolment, confirmation, or disable, no code path touches `session_epoch` from `backend/handlers/totp.go`. A deactivated account is additionally caught on its own by the session middleware's `IsActive` reload (`backend/middleware/session.go:141`), independent of the epoch bump above.

Logging out clears cookies differently depending on the route: `GET /logout` only redirects to `/login` and revokes nothing (`backend/handlers/auth.go:210-212`). `POST /logout` bumps the session epoch and clears the session cookie, but leaves the CSRF cookie in place (`backend/handlers/auth.go:216-234`). Only `DELETE /api/v1/session` expires both the session and CSRF cookies (`backend/api/session.go:110-127`).

## TOTP 2FA

`backend/handlers/totp.go`, using `github.com/pquerna/otp`. Enrolment: generate a secret, show a QR code, confirm one code to activate, and receive 8 single-use recovery codes (hashed before storage). A pending, unconfirmed enrolment expires after 5 minutes. `totp_last_step` is stored per user to reject a replayed code within the same 30-second step.

## Password recovery

Self-service forgot-password flow (`backend/handlers/password_reset.go`):

- A single-use token is emailed, valid for 30 minutes.
- The token is SHA-256 hashed before it is stored, and invalidated on use.
- Requests are rate-limited per IP: 3 attempts per 15 minutes, tracked by a dedicated limiter separate from the login rate limiter.
- The response body is identical whether or not the account exists, so content alone cannot be used to enumerate users. Timing is **not** equalised, several checks (no email on file, SMTP not configured, token-creation failure, link-build failure) return before the token is created and before the 10-second SMTP dial, so a careful timing comparison can still distinguish some of these cases (`backend/handlers/password_reset.go:94-133`).
- Requires `PUBLIC_BASE_URL` to be set: emailed links are built from it, never from the inbound request's `Host` header, since that header is attacker-controlled. If `PUBLIC_BASE_URL` is unset, the reset email is silently not sent and the attempt is written to the auth log.
- Requires SMTP to be enabled on `/admin/notifications` with a host and a from address set (`settings.SMTPEnabled`, `settings.SMTPHost`, `settings.SMTPFrom`, `backend/handlers/password_reset.go:94`) for the email to actually go out. Port, TLS mode and credentials are also configured there and passed to the send, but only enabled/host/from are checked before attempting it.

See also: [docs/security.md](security.md), [docs/configuration.md](configuration.md), [docs/faq.md](faq.md).
