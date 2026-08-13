import { APIResponse } from "@playwright/test";
import { composeOrThrow, psql, waitHealthy } from "../../helpers/compose";
import { AuthedContext, expect, test } from "../../fixtures/test";

interface ConsoleResult {
  success: boolean;
  exitCode: number;
  output: string;
}

/**
 * The result renders inline: an "exit N" badge on failure, and the output in a
 * <pre> (templates/admin_console.html:53-70).
 */
async function runCommand(admin: AuthedContext, commandID: string): Promise<ConsoleResult> {
  const token = await admin.csrf("/admin/console");
  const res: APIResponse = await admin.send(() =>
    admin.ctx.post("/admin/console", { form: { command_id: commandID, csrf_token: token } }),
  );
  const body = res.status() === 200 ? await res.text() : await (await admin.ctx.get("/admin/console")).text();
  const exit = /<span class="badge danger">exit (\d+)<\/span>/.exec(body);
  const output = /<pre class="admin-console-output">([\s\S]*?)<\/pre>/.exec(body)?.[1] ?? "";
  return {
    success: exit === null,
    exitCode: exit ? Number(exit[1]) : 0,
    output: output.trim(),
  };
}

// The oracle pair is serial and adjacent: ADM-02 alone passes against a stubbed
// Health() that always returns nil, and ADM-03 is what makes it meaningful.
test.describe.configure({ mode: "serial" });

test("E2E-ADM-02: ca.health with the CA up", async ({ admin }) => {
  const result = await runCommand(admin, "ca.health");
  expect(result.success).toBe(true);
  expect(result.output).toBe("ok");
});

test("E2E-ADM-03: ca.health with the CA down", async ({ admin }) => {
  test.setTimeout(300_000);
  await composeOrThrow("stop", "step-ca");
  try {
    const result = await runCommand(admin, "ca.health");
    expect(result.success, "the command reports failure").toBe(false);
    expect(result.exitCode).toBe(1);
    // Compose removes the service name from its DNS when the container stops,
    // so the dial fails at resolution rather than at connect. Both are the same
    // property: the failure happened while reaching the CA.
    expect(
      /connection refused|no such host|lookup .* server misbehaving|dial tcp|timeout|deadline exceeded|no route to host|EOF/i.test(
        result.output,
      ),
      `expected a dial-level error, got: ${result.output}`,
    ).toBe(true);

    // h.caClient() caches after its first success, so by this point the client
    // exists and the failure necessarily comes from Health(), not construction.
    // "CA client unavailable" here would mean the cache was lost, a different
    // bug, and is E2E-BOOT-09's expected result rather than this one's.
    expect(result.output, "not the construction-failure string").not.toContain("CA client unavailable");
  } finally {
    await composeOrThrow("start", "step-ca");
    await waitHealthy("step-ca");
  }
});

test("E2E-ADM-04: the OS diagnostic commands still run", async ({ admin }) => {
  test.setTimeout(180_000);
  const commands = [
    "system.date",
    "system.hostname",
    "system.identity",
    "system.disk",
    "system.processes",
    "app.files",
    "openssl.version",
    "postgres.ready",
  ];

  const results = new Map<string, ConsoleResult>();
  for (const id of commands) {
    const result = await runCommand(admin, id);
    results.set(id, result);
    expect(result.success, `${id} succeeded`).toBe(true);
    expect(result.exitCode, `${id} exit code`).toBe(0);
  }

  expect(results.get("postgres.ready")!.output).toContain("accepting connections");
  // USER stepui is uid 10001 (Dockerfile:48). Nothing else about these outputs
  // is asserted: "sane output" for a date or a process list has no falsifiable
  // form, and openssl.version reports a base image this project does not build.
  expect(results.get("system.identity")!.output).toContain("10001");
});

test("E2E-ADM-05: an unknown command_id is rejected and audited", async ({ admin }) => {
  const maxBefore = await psql("SELECT coalesce(max(id), 0) FROM auth_log");

  const token = await admin.csrf("/admin/console");
  const res = await admin.send(() =>
    admin.ctx.post("/admin/console", { form: { command_id: "rm.rf", csrf_token: token } }),
  );
  const body = res.status() === 200 ? await res.text() : await (await admin.ctx.get("/admin/console")).text();
  expect(body).toContain("Unknown command. Only allowlisted commands may be run.");

  const denied = await psql(
    `SELECT count(*) FROM auth_log WHERE id > ${maxBefore} AND reason LIKE '%console.denied command_id=rm.rf%'`,
  );
  expect(Number(denied), "the denial is audited").toBeGreaterThan(0);

  const ran = await psql(
    `SELECT count(*) FROM auth_log WHERE id > ${maxBefore} AND reason LIKE '%console.run id=rm.rf%'`,
  );
  expect(ran, "no console.run row for a command that never ran").toBe("0");
});
