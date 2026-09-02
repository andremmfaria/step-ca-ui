import { expect, test } from "@playwright/test";
import {
  captureBeforeRecreate,
  compose,
  composeOrThrow,
  countExact,
  cumulativeLog,
  exec,
  logs,
  resetStepUIVolumes,
  run,
  upStack,
  waitHealthy,
  waitForLogLine,
} from "../../helpers/compose";
import { getEnvKey, setEnvKeys } from "../../helpers/envfile";
import { probeTLS } from "../../helpers/openssl";

// Three cases share one CA identity (Section 3.1's teardown table): tearing
// down step-ca-data between them would recompute the fingerprint and defeat
// E2E-BOOT-05's "wrong but well-formed" precondition. Only step-ui's own
// volumes are reset between cases.
test.describe.serial("bootstrap: fingerprint scenario", () => {
  test.beforeAll(async () => {
    await composeOrThrow("down", "-v");
    await composeOrThrow("up", "-d", "--wait", "step-ca");
  });

  test.afterAll(async () => {
    await composeOrThrow("down", "-v");
  });

  test("E2E-BOOT-01: stepca mode happy path from empty volumes via CA_FINGERPRINT", async () => {
    test.setTimeout(5 * 60_000);

    const fp = await exec("step-ca", "step", "certificate", "fingerprint", "/home/step/certs/root_ca.crt");
    expect(fp.code, `fingerprint command failed: ${fp.stderr}`).toBe(0);
    const fingerprint = fp.stdout.trim();
    expect(fingerprint).toMatch(/^[0-9a-f]{64}$/);

    setEnvKeys({ UI_TLS_MODE: "stepca", CA_FINGERPRINT: fingerprint, CA_ROOT_CERT_PEM: undefined });
    await upStack();
    await waitHealthy("step-ui", 180_000);

    const log = await logs("step-ui");
    const iFetching = log.indexOf("fetching root CA certificate via CA_FINGERPRINT");
    const iFetched = log.indexOf("root CA certificate fetched and verified");
    const iIssued = log.indexOf("UI leaf certificate obtained");
    // compose.e2e-fingerprint.yml applied is what makes this line possible at
    // all: under the stock read-only mount, ensureRootCert early-returns and
    // CA_FINGERPRINT is never read.
    expect(iFetching, "missing 'fetching root CA certificate via CA_FINGERPRINT' — was compose.e2e-fingerprint.yml applied?").toBeGreaterThanOrEqual(0);
    expect(iFetched, "missing 'root CA certificate fetched and verified'").toBeGreaterThan(iFetching);
    expect(iIssued, "missing 'UI leaf certificate obtained'").toBeGreaterThan(iFetched);

    const rootStat = await exec("step-ui", "test", "-s", "/opt/step-ui/data/root_ca.crt");
    expect(rootStat.code, "root cert missing or empty at the writable path").toBe(0);

    const port = getEnvKey("UI_HTTPS_PORT") ?? "443";
    const cert = await probeTLS(`localhost:${port}`);
    expect(cert.selfSigned, `issuer ${cert.issuer} should differ from subject ${cert.subject} for a step-ca-issued leaf`).toBe(false);

    const whichStep = await exec("step-ui", "which", "step");
    expect(whichStep.code, "the step binary must not be present in the runtime image").not.toBe(0);

    const grep = await run(["grep", "-cE", "(^|[^-[:alnum:]])step +(ca|certificate|version|crypto)([^-[:alnum:]]|$)", "backend/entrypoint.sh"]);
    expect(grep.stdout.trim(), "entrypoint.sh must not still invoke the step CLI").toBe("0");
  });

  test("E2E-BOOT-05: wrong CA_FINGERPRINT exhausts the root fetch and is reported distinctly", async () => {
    test.setTimeout(5 * 60_000);

    await captureBeforeRecreate("step-ui");
    await composeOrThrow("down");
    await resetStepUIVolumes();
    await composeOrThrow("up", "-d", "--wait", "step-ca");

    setEnvKeys({ UI_TLS_MODE: "stepca", CA_FINGERPRINT: "0".repeat(64), CA_ROOT_CERT_PEM: undefined });
    await upStack();

    const fetching = await waitForLogLine("step-ui", "fetching root CA certificate via CA_FINGERPRINT", { timeoutMs: 90_000 });
    const exhausted = await waitForLogLine(
      "step-ui",
      "could not fetch root CA certificate after retries — continuing without it",
      { timeoutMs: 90_000 },
    );
    const gapMs = timestampOf(exhausted).getTime() - timestampOf(fetching).getTime();
    expect(gapMs, `retry loop should span at least 28s, got ${gapMs}ms`).toBeGreaterThanOrEqual(28_000);

    const port = getEnvKey("UI_HTTPS_PORT") ?? "443";
    const [caPs, caHealth, rootStat, ready, login] = await Promise.all([
      compose("ps", "--format", "json", "step-ca"),
      exec("step-ca", "curl", "-sk", "https://localhost:9443/health"),
      exec("step-ui", "test", "-e", "/opt/step-ui/data/root_ca.crt"),
      run(["curl", "-sk", `https://localhost:${port}/ready`]),
      run(["curl", "-sk", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "5", `https://localhost:${port}/login`]),
    ]);
    expect(caPs.stdout, "step-ca must stay healthy throughout").toContain('"Health":"healthy"');
    expect(caHealth.stdout.trim(), "step-ca's own /health must answer ok").toBe('{"status":"ok"}');
    expect(rootStat.code, "no root cert should ever have been written on a fingerprint mismatch").not.toBe(0);

    const readyBody = JSON.parse(ready.stdout) as { status: string; ca: string };
    expect(readyBody.ca).toBe("unreachable");
    expect(login.stdout.trim()).toBe("200");
  });

  test("E2E-BOOT-06: CA_ROOT_CERT_PEM inline root provisioning", async () => {
    test.setTimeout(5 * 60_000);

    await captureBeforeRecreate("step-ui");
    await composeOrThrow("down");
    await resetStepUIVolumes();
    await composeOrThrow("up", "-d", "--wait", "step-ca");

    const rootPEM = await exec("step-ca", "cat", "/home/step/certs/root_ca.crt");
    expect(rootPEM.code).toBe(0);
    expect(rootPEM.stdout).toContain("BEGIN CERTIFICATE");

    setEnvKeys({ UI_TLS_MODE: "stepca", CA_FINGERPRINT: undefined, CA_ROOT_CERT_PEM: rootPEM.stdout });
    await upStack();
    await waitHealthy("step-ui", 180_000);

    const log = await logs("step-ui");
    expect(log).toContain("wrote root CA certificate from CA_ROOT_CERT_PEM");
    expect(countExact(log, "fetching root CA certificate via CA_FINGERPRINT")).toBe(0);

    // Positive control: E2E-BOOT-01 produced that line earlier in this same
    // job, under the same override. Without this, a harness wired to always
    // report zero matches would pass the assertion above for the wrong reason.
    const cumulative = await cumulativeLog("step-ui");
    expect(countExact(cumulative, "fetching root CA certificate via CA_FINGERPRINT")).toBeGreaterThanOrEqual(1);

    const written = await exec("step-ui", "cat", "/opt/step-ui/data/root_ca.crt");
    expect(written.stdout.trim()).toBe(rootPEM.stdout.trim());

    expect(log).toContain("UI leaf certificate obtained");

    const ready = await run(["curl", "-sk", `https://localhost:${getEnvKey("UI_HTTPS_PORT") ?? "443"}/ready`]);
    expect(ready.stdout.trim()).toBe('{"status":"ready"}');
  });
});

function timestampOf(line: string): Date {
  const m = /\|\s*(\S+)/.exec(line);
  if (!m || !m[1]) throw new Error(`could not find a docker --timestamps prefix in: ${line}`);
  const d = new Date(m[1]);
  if (Number.isNaN(d.getTime())) throw new Error(`unparseable timestamp ${m[1]} in: ${line}`);
  return d;
}
