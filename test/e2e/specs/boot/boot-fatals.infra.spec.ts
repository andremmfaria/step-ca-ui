import * as fs from "node:fs/promises";
import * as path from "node:path";
import { expect, test } from "@playwright/test";
import { composeOrThrow, logs, run, upStack, waitExited, waitHealthy } from "../../helpers/compose";
import { REPO_ROOT } from "../../helpers/env";
import { getEnvKey, setEnvKeys } from "../../helpers/envfile";

const SECRET_KEY_PATH = path.join(REPO_ROOT, "secrets", "secret_key");
const ADMIN_PW = "Boot07-e2e-Pass1!";

/**
 * Cases (a), (d) and (e) fail before the DB connect, so compose.e2e-nodeps.yml
 * (already applied for this whole scenario) lets step-ui start without step-ca
 * at all — but postgres still needs to be up and reachable, or entrypoint.sh's
 * own 60x1s wait-for-postgres loop (which every case runs through first) hangs
 * on DNS resolution for a service with no running container, adding far more
 * than 60s rather than failing each attempt fast.
 */
async function freshPostgres(): Promise<void> {
  await composeOrThrow("down", "-v");
  await composeOrThrow("up", "-d", "--wait", "postgres");
}

async function expectFatal(logContains: string | string[]): Promise<void> {
  await upStack("step-ui");
  // The scenario's own compose.e2e-fatals.yml sets restart:"no" for every
  // case in this file, not only (c) — against the stock restart policy a
  // fatal exit restarts into the same fatal, and reading ExitCode mid-loop
  // races the next restart.
  const state = await waitExited("step-ui", 90_000);
  expect(state.ExitCode, "a deliberate fatal must exit non-zero").not.toBe(0);
  const log = await logs("step-ui");
  for (const needle of Array.isArray(logContains) ? logContains : [logContains]) {
    expect(log, `missing expected fatal message: ${needle}`).toContain(needle);
  }
  const port = getEnvKey("UI_HTTPS_PORT") ?? "443";
  const curl = await run(["curl", "-sk", "-o", "/dev/null", "--max-time", "3", `https://localhost:${port}/login`]);
  expect(curl.code, "a fatally-exited container must not still be serving").not.toBe(0);
}

test.describe.serial("bootstrap: fatals scenario", () => {
  // Only case (a)'s first sub-case was confirmed by a manual, out-of-harness
  // dry run on 2026-09-02, after discovering that entrypoint.sh's wait-for-
  // postgres loop hangs well past its nominal 60s when "postgres" has no
  // running container at all (fixed here by bringing postgres up healthy for
  // every case, not just (b) and (c)) — that fix is applied below but the
  // full file, in order, including case (c)'s wait against a stopped (not
  // absent) postgres, was not re-run end to end before this pass ended.
  test.fixme(true, "E2E-BOOT-07 not yet re-verified end to end after the freshPostgres() fix — see comment above");

  test("E2E-BOOT-07 (a): weak SECRET_KEY", async () => {
    test.setTimeout(3 * 60_000);
    const original = await fs.readFile(SECRET_KEY_PATH);
    try {
      await freshPostgres();
      setEnvKeys({ STEPUI_ADMIN_PASSWORD: ADMIN_PW });

      await fs.writeFile(SECRET_KEY_PATH, "change-me-in-production-32chars!");
      await expectFatal("FATAL: SECRET_KEY is the default or shorter than 32 chars");

      await freshPostgres();
      await fs.writeFile(SECRET_KEY_PATH, "too-short");
      await expectFatal("FATAL: SECRET_KEY is the default or shorter than 32 chars");
    } finally {
      await fs.writeFile(SECRET_KEY_PATH, original);
    }
  });

  test("E2E-BOOT-07 (b): empty users table with no admin password", async () => {
    test.setTimeout(3 * 60_000);
    await freshPostgres();
    setEnvKeys({ STEPUI_ADMIN_PASSWORD: undefined });
    await expectFatal("No admin user exists and STEPUI_ADMIN_PASSWORD is not set");
  });

  test("E2E-BOOT-07 (c): database unreachable", async () => {
    test.setTimeout(3 * 60_000);
    await freshPostgres();
    await composeOrThrow("stop", "postgres");
    setEnvKeys({ STEPUI_ADMIN_PASSWORD: ADMIN_PW });
    await expectFatal(["PostgreSQL not reachable", "Cannot connect to database:"]);
  });

  test("E2E-BOOT-07 (d): TRUST_PROXY=true with no usable CIDR list", async () => {
    test.setTimeout(3 * 60_000);
    await freshPostgres();
    setEnvKeys({ STEPUI_ADMIN_PASSWORD: ADMIN_PW, TRUST_PROXY: "true", TRUSTED_PROXY_CIDRS: undefined });
    await expectFatal("FATAL: TRUST_PROXY=true requires a usable TRUSTED_PROXY_CIDRS");

    await freshPostgres();
    setEnvKeys({ TRUSTED_PROXY_CIDRS: "not-a-cidr" });
    await expectFatal("FATAL: TRUST_PROXY=true requires a usable TRUSTED_PROXY_CIDRS");
  });

  test("E2E-BOOT-07 (e): bad OIDC_DEFAULT_ROLE with OIDC on", async () => {
    test.setTimeout(3 * 60_000);
    await freshPostgres();
    setEnvKeys({
      STEPUI_ADMIN_PASSWORD: ADMIN_PW,
      TRUST_PROXY: undefined,
      TRUSTED_PROXY_CIDRS: undefined,
      OIDC_ENABLED: "true",
      OIDC_DEFAULT_ROLE: "nonsense",
    });
    await expectFatal("is not one of viewer, manager, admin");
  });

  test("E2E-BOOT-07 positive control: none of the five conditions injected", async () => {
    test.setTimeout(3 * 60_000);
    // Without this, an override that simply prevented step-ui from starting
    // at all would pass every case above.
    await composeOrThrow("down", "-v");
    setEnvKeys({
      STEPUI_ADMIN_PASSWORD: ADMIN_PW,
      TRUST_PROXY: undefined,
      TRUSTED_PROXY_CIDRS: undefined,
      OIDC_ENABLED: undefined,
      OIDC_DEFAULT_ROLE: undefined,
    });
    await upStack();
    await waitHealthy("step-ui", 180_000);
  });

  test.afterAll(async () => {
    await composeOrThrow("down", "-v");
  });
});
