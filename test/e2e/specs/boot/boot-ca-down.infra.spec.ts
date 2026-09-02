import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import { APIRequestContext, expect, test } from "@playwright/test";
import { composeArgv, composeOrThrow, containerId, exec, run, upStack, waitForLogLine, waitHealthy } from "../../helpers/compose";
import { hostURL } from "../../helpers/env";
import { getEnvKey, setEnvKeys } from "../../helpers/envfile";
import { adminCredentials } from "../../fixtures/test";
import { probeTLS } from "../../helpers/openssl";
import { csrfTokenFrom, loginOrThrow, newJar } from "../../helpers/session";

function timestampOf(line: string): Date {
  const m = /\|\s*(\S+)/.exec(line);
  if (!m || !m[1]) throw new Error(`could not find a docker --timestamps prefix in: ${line}`);
  const d = new Date(m[1]);
  if (Number.isNaN(d.getTime())) throw new Error(`unparseable timestamp ${m[1]} in: ${line}`);
  return d;
}

interface ConsoleResult {
  success: boolean;
  output: string;
}

/** Mirrors specs/admin/adm-console.api.spec.ts's runCommand — duplicated rather than shared, since that one runs through the worker-scoped admin fixture this file cannot use (it targets BASE_URL, the container-network hostname; infra runs on the host). */
async function runConsoleCommand(ctx: APIRequestContext, commandID: string): Promise<ConsoleResult> {
  const token = await csrfTokenFrom(ctx, "/admin/console");
  const res = await ctx.post("/admin/console", { form: { command_id: commandID, csrf_token: token } });
  const body = res.status() === 200 ? await res.text() : await (await ctx.get("/admin/console")).text();
  const exit = /<span class="badge danger">exit (\d+)<\/span>/.exec(body);
  const output = /<pre class="admin-console-output">([\s\S]*?)<\/pre>/.exec(body)?.[1] ?? "";
  return { success: exit === null, output: output.trim() };
}

test.describe.serial("bootstrap: ca-down scenario", () => {
  test("E2E-BOOT-02: CA down at boot, UI_TLS_MODE=stepca exhausts the retry loop and falls back", async () => {
    test.setTimeout(3 * 60_000);

    // Fresh volumes with a root cert present (stock read-only mount) and step-ca
    // stopped. compose.e2e-nodeps.yml (already in E2E_COMPOSE_OVERRIDES for this
    // scenario) is what lets step-ui start against it at all.
    await composeOrThrow("down", "-v");
    await composeOrThrow("up", "-d", "--wait", "step-ca", "postgres");
    await composeOrThrow("stop", "step-ca");

    setEnvKeys({ UI_TLS_MODE: "stepca", CA_FINGERPRINT: undefined, CA_ROOT_CERT_PEM: undefined });
    await upStack("step-ui");

    const obtaining = await waitForLogLine("step-ui", "obtaining UI leaf certificate from step-ca", { timeoutMs: 90_000 });
    const fallback = await waitForLogLine(
      "step-ui",
      "step-ca certificate issuance failed after retries — falling back to self-signed",
      { timeoutMs: 90_000 },
    );
    const gapMs = timestampOf(fallback).getTime() - timestampOf(obtaining).getTime();
    expect(gapMs, `retry loop should span at least 28s, got ${gapMs}ms`).toBeGreaterThanOrEqual(28_000);

    const port = getEnvKey("UI_HTTPS_PORT") ?? "443";
    const login = await run(["curl", "-sk", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "5", `https://localhost:${port}/login`]);
    expect(login.stdout.trim(), "TLS bootstrap failure must be non-fatal").toBe("200");

    const cert = await probeTLS(`localhost:${port}`);
    expect(cert.selfSigned, `issuer ${cert.issuer} should equal subject ${cert.subject} for a self-signed fallback`).toBe(true);
  });

  test("E2E-BOOT-09: nil CA client short-circuits, and the failure is not cached", async () => {
    test.setTimeout(3 * 60_000);

    await composeOrThrow("down", "-v");
    // Distinct from E2E-BOOT-02: the fingerprint override's writable ROOT_CERT
    // path with no file at it, so stepca.New fails on construction rather than
    // on a reachable-but-down CA.
    const extraOverride = process.env.E2E_CA_DOWN_FINGERPRINT_OVERRIDE;
    if (extraOverride) {
      const current = (process.env.E2E_COMPOSE_OVERRIDES ?? "").split(":").filter(Boolean);
      if (!current.includes(extraOverride)) current.push(extraOverride);
      process.env.E2E_COMPOSE_OVERRIDES = current.join(":");
    }
    await composeOrThrow("up", "-d", "--wait", "step-ca");

    setEnvKeys({ UI_TLS_MODE: "stepca", CA_FINGERPRINT: undefined, CA_ROOT_CERT_PEM: undefined });
    await upStack();
    await waitHealthy("step-ui", 180_000);

    const id = await containerId("step-ui");
    const startedRes = await run(["docker", "inspect", "--format", "{{.State.StartedAt}}", id]);
    const startedAt = new Date(startedRes.stdout.trim());

    const constructionFailed = await waitForLogLine("step-ui", "CA client construction failed during TLS bootstrap", { timeoutMs: 30_000 });
    const fallback = await waitForLogLine(
      "step-ui",
      "UI_TLS_MODE=stepca but no CA client is available — falling back to self-signed",
      { timeoutMs: 30_000 },
    );
    expect(constructionFailed).not.toBe("");
    const elapsedMs = timestampOf(fallback).getTime() - startedAt.getTime();
    expect(elapsedMs, `the nil-client branch must not enter the retry loop, got ${elapsedMs}ms since container start`).toBeLessThan(5_000);

    const jar = await newJar(hostURL());
    try {
      await loginOrThrow(jar, adminCredentials());

      const before = await runConsoleCommand(jar, "ca.health");
      expect(before.success, "a nil client must report failure, not throw").toBe(false);
      expect(before.output).toBe("CA client unavailable");

      const rootPEM = await exec("step-ca", "cat", "/home/step/certs/root_ca.crt");
      expect(rootPEM.code).toBe(0);
      const tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-boot09-"));
      try {
        const tmpFile = path.join(tmpDir, "root_ca.crt");
        await fs.writeFile(tmpFile, rootPEM.stdout);
        const cp = await run(composeArgv("cp", tmpFile, "step-ui:/opt/step-ui/data/root_ca.crt"));
        expect(cp.code, `docker compose cp failed: ${cp.stderr}`).toBe(0);
      } finally {
        await fs.rm(tmpDir, { recursive: true, force: true });
      }

      // h.caClient() caches only on success and retries stepca.New on every
      // call after a failure, so the root cert appearing later is picked up
      // with no restart — the second half of R2.
      const after = await runConsoleCommand(jar, "ca.health");
      expect(after.success, "a root cert appearing later must be picked up with no restart").toBe(true);
      expect(after.output).toBe("ok");
    } finally {
      await jar.dispose();
    }
  });
});
