# Authentication

Local login, OIDC SSO, session and CSRF cookie behaviour, two-factor authentication, and password reset.

Step-CA UI supports local username/password login and OIDC SSO. Both can be active at once. Roles (`viewer`, `manager`, `admin`) are described in [docs/architecture.md](architecture.md#roles).

## Local login

On by default (`LOCAL_LOGIN_ENABLED=true`). Keep it enabled while configuring OIDC, disabling it before SSO is verified working will lock you out. It doubles as a break-glass path if the IdP is unreachable.

Password policy (`backend/security/security.go`, `ValidatePassword`): 8 to 72 characters, at least one digit, one letter, and one of `` +!@#$%^&*()_-=[]{}|;:,.<>? ``.

Passwords are hashed with bcrypt at the default cost. A legacy SHA-256 hash (from before the bcrypt migration) is still accepted on login and transparently rehashed to bcrypt afterward. `NeedsPasswordRehash` also flags a bcrypt hash whose cost has fallen below the current default.

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

If no configured group name matches and `OIDC_DEFAULT_ROLE` is empty, access is denied and the login attempt is logged. Set `OIDC_DEFAULT_ROLE` to `viewer` only if every authenticated IdP user should get read access. `main.go` refuses to start if `OIDC_DEFAULT_ROLE` is set to anything other than `viewer`, `manager`, `admin`, or empty.

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

**Timeouts** (`backend/middleware/middleware.go`): an 8-hour idle (sliding) window, `SessionTimeout`, and a 24-hour absolute cap from creation, `SessionMaxLifetime`, whichever is hit first. `GET /api/v1/session` is exempt from the sliding-window renewal so an open tab cannot keep a session alive indefinitely.

**Session epoch.** Each user row carries a `session_epoch` integer. Bumping it (on password change, 2FA change, forced logout, or account deactivation, see `appdb.BumpSessionEpoch`) invalidates every session cookie already issued to that user on the next request, even ones inside their idle window, because the epoch stored in the cookie no longer matches the row.

Logging out (`GET /logout`, `POST /logout`, or `DELETE /api/v1/session`) clears both cookies.

## TOTP 2FA

`backend/handlers/totp.go`, using `github.com/pquerna/otp`. Enrolment: generate a secret, show a QR code, confirm one code to activate, and receive 8 single-use recovery codes (hashed before storage). A pending, unconfirmed enrolment expires after 5 minutes. `totp_last_step` is stored per user to reject a replayed code within the same 30-second step.

## Password recovery

Self-service forgot-password flow (`backend/handlers/password_reset.go`):

- A single-use token is emailed, valid for 30 minutes.
- The token is SHA-256 hashed before it is stored, and invalidated on use.
- Requests are rate-limited per IP: 3 attempts per 15 minutes, tracked by a dedicated limiter separate from the login rate limiter.
- The response is identical whether or not the account exists, so no user enumeration is possible from timing or content.
- Requires `PUBLIC_BASE_URL` to be set: emailed links are built from it, never from the inbound request's `Host` header, since that header is attacker-controlled. If `PUBLIC_BASE_URL` is unset, the reset email is silently not sent and the attempt is written to the auth log.
- Requires SMTP to be configured on `/admin/notifications` (host, port, TLS mode, credentials, from address) for the email to actually go out.

See also: [docs/security.md](security.md), [docs/configuration.md](configuration.md), [docs/faq.md](faq.md).
