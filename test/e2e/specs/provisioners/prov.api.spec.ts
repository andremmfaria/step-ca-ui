import { compose, composeOrThrow, exec, waitHealthy } from "../../helpers/compose";
import { expect, test } from "../../fixtures/test";

/** One .prov-card per provisioner (templates/provisioners.html:80-84). */
function provisionerRowCount(html: string): number {
  return (html.match(/<div class="prov-card">/g) ?? []).length;
}

/** The empty state, which renders for nil, a stub and a genuinely empty CA alike. */
const EMPTY_STATE = "No provisioners or CA is unavailable";

// Oracle pair: each half passes against a stub the other would catch, so flake
// triage must never retire one without the other.
let rowsWhenHealthy = 0;

test("E2E-PROV-01: the provisioner list matches the CA's own configuration", async ({ viewer }) => {
  // jq ships in the step-ca image, which is what step-ca-bootstrap.sh relies on.
  // There is no `step-ca provisioner list` subcommand.
  const caJSON = await exec("step-ca", "sh", "-c", "cat /home/step/config/ca.json | jq -c '.authority.provisioners'");
  expect(caJSON.code, `reading ca.json: ${caJSON.stderr}`).toBe(0);
  const provisioners = JSON.parse(caJSON.stdout.trim()) as Array<{ name: string; type: string }>;
  expect(provisioners.length, "the CA has at least one provisioner").toBeGreaterThan(0);

  const res = await viewer.send(() => viewer.ctx.get("/provisioners"));
  expect(res.status()).toBe(200);
  const body = await res.text();

  for (const p of provisioners) {
    expect(body, `provisioner ${p.name} is listed`).toContain(p.name);
    expect(body, `provisioner type ${p.type} is listed`).toContain(p.type);
  }

  const config = await compose("config", "--format", "json");
  const parsed = JSON.parse(config.stdout) as {
    services: Record<string, { environment: Record<string, string> }>;
  };
  const env = parsed.services["step-ui"]!.environment;
  expect(body, "CAURL renders").toContain(env["CA_URL"]!);
  expect(body, "RootCert renders").toContain(env["ROOT_CERT"]!);
  expect(body, "Provisioner renders").toContain(env["PROVISIONER"]!);

  rowsWhenHealthy = provisionerRowCount(body);
  expect(rowsWhenHealthy, "the healthy page renders at least one provisioner row").toBeGreaterThan(0);
});

test("E2E-PROV-02: the page degrades rather than failing when the CA is unreachable", async ({ viewer }) => {
  test.setTimeout(240_000);
  expect(rowsWhenHealthy, "E2E-PROV-01 must run first: it is this test's positive control").toBeGreaterThan(0);

  await composeOrThrow("stop", "step-ca");
  try {
    const res = await viewer.send(() => viewer.ctx.get("/provisioners"));
    expect(res.status(), "degrades, never 500").toBe(200);
    const body = await res.text();

    expect(provisionerRowCount(body), `row count fell from ${rowsWhenHealthy} to zero`).toBe(0);
    expect(body, "the empty state renders in place of the cards").toContain(EMPTY_STATE);

    // provs stays nil when either caClient() or Provisioners() errors, and the
    // template renders nil, a stub and a genuinely empty CA identically. The
    // three configuration values are what prove the handler reached the end of
    // its body at all.
    const config = await compose("config", "--format", "json");
    const parsed = JSON.parse(config.stdout) as { services: Record<string, { environment: Record<string, string> }> };
    const env = parsed.services["step-ui"]!.environment;
    expect(body, "CAURL still renders").toContain(env["CA_URL"]!);
    expect(body, "RootCert still renders").toContain(env["ROOT_CERT"]!);
    expect(body, "Provisioner still renders").toContain(env["PROVISIONER"]!);
  } finally {
    await composeOrThrow("start", "step-ca");
    await waitHealthy("step-ca");
  }
});
