import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import { expect, test } from "@playwright/test";
import { composeOrThrow, logs, projectName, run, upStack, waitHealthy } from "../../helpers/compose";
import { getEnvKey, setEnvKeys } from "../../helpers/envfile";
import { parsePEM, probeTLS } from "../../helpers/openssl";

test("E2E-BOOT-03: UI_TLS_MODE=provided leaves an operator certificate untouched", async () => {
  test.setTimeout(4 * 60_000);

  await composeOrThrow("down", "-v");
  await composeOrThrow("up", "-d", "--wait", "postgres", "step-ca");

  // Generated on the harness, not inside the runtime image (uid 10001 cannot
  // apk add), then copied into the named volume by a throwaway container so
  // it is in place before step-ui's first start — "provided" never writes
  // anything, so nothing else would put it there.
  const tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-boot03-"));
  try {
    const gen = await run([
      "openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-days", "1", "-nodes",
      "-subj", "/CN=test-provided", "-keyout", path.join(tmpDir, "server.key"), "-out", path.join(tmpDir, "server.crt"),
    ]);
    expect(gen.code, `openssl req failed: ${gen.stderr}`).toBe(0);
    const seededPEM = await fs.readFile(path.join(tmpDir, "server.crt"), "utf8");
    const seededCert = await parsePEM(seededPEM);

    const vol = `${projectName()}_step-ui-ssl`;
    const seed = await run([
      "docker", "run", "--rm",
      "-v", `${vol}:/ssl`,
      "-v", `${tmpDir}:/src:ro`,
      "alpine",
      "sh", "-c", "cp /src/server.crt /src/server.key /ssl/ && chown 10001:10001 /ssl/server.crt /ssl/server.key",
    ]);
    expect(seed.code, `seeding the operator certificate failed: ${seed.stderr}`).toBe(0);

    setEnvKeys({ UI_TLS_MODE: "provided" });
    await upStack();
    await waitHealthy("step-ui", 180_000);

    const port = getEnvKey("UI_HTTPS_PORT") ?? "443";
    const served = await probeTLS(`localhost:${port}`);
    expect(served.subject).toBe(seededCert.subject);
    expect(served.sha256).toBe(seededCert.sha256);

    // Negative control: the same pre-seeded material stays in place, but a
    // changed UI_TLS_MODE with a reachable CA now issues a different leaf.
    // Without this, "no bootstrap log lines in the provided case" would pass
    // just as well if the whole bootstrap block had been deleted.
    setEnvKeys({ UI_TLS_MODE: "stepca" });
    await upStack();
    await waitHealthy("step-ui", 180_000);

    const reissued = await probeTLS(`localhost:${port}`);
    expect(reissued.subject).not.toBe(served.subject);
    expect(reissued.sha256).not.toBe(served.sha256);
    expect(await logs("step-ui")).toContain("UI leaf certificate obtained");
  } finally {
    await fs.rm(tmpDir, { recursive: true, force: true });
  }
});

test.afterAll(async () => {
  await composeOrThrow("down", "-v");
});
