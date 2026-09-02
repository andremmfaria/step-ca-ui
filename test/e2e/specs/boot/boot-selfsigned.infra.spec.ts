import { expect, test } from "@playwright/test";
import { composeOrThrow, countExact, logs, upStack, waitHealthy } from "../../helpers/compose";
import { getEnvKey, setEnvKeys } from "../../helpers/envfile";
import { probeTLS, validityMs } from "../../helpers/openssl";

test("E2E-BOOT-04: self-signed default when UI_TLS_MODE is unset", async () => {
  test.setTimeout(3 * 60_000);

  await composeOrThrow("down", "-v");
  // config.Load's getEnv("UI_TLS_MODE", "self-signed") default only applies
  // when the key is genuinely absent, not merely empty.
  setEnvKeys({ UI_TLS_MODE: undefined });
  await upStack();
  await waitHealthy("step-ui", 180_000);

  const port = getEnvKey("UI_HTTPS_PORT") ?? "443";
  const cert = await probeTLS(`localhost:${port}`);
  expect(cert.selfSigned, `issuer ${cert.issuer} should equal subject ${cert.subject}`).toBe(true);
  expect(cert.sans).toEqual(expect.arrayContaining([`IP Address:${getEnvKey("HOST_IP") ?? "127.0.0.1"}`, "DNS:localhost"]));

  const tenYearsMs = 10 * 365 * 24 * 60 * 60 * 1000;
  expect(Math.abs(validityMs(cert) - tenYearsMs)).toBeLessThan(24 * 60 * 60 * 1000);

  const log = await logs("step-ui");
  // Self-signed is also the terminal state of every failure path: without
  // these two counts this test would pass against a completely dead
  // bootstrap block.
  expect(countExact(log, "generated self-signed TLS certificate")).toBe(1);
  expect(countExact(log, "falling back to self-signed")).toBe(0);
});

test.afterAll(async () => {
  await composeOrThrow("down", "-v");
});
