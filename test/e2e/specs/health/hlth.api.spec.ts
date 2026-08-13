import { APIRequestContext } from "@playwright/test";
import { compose, composeOrThrow, exec, waitHealthy } from "../../helpers/compose";
import { pollUntil, sleep } from "../../helpers/poll";
import { loginOrThrow, newJar } from "../../helpers/session";
import { expect, test } from "../../fixtures/test";

interface ReadyBody {
  status: string;
  db: string;
  ca: string;
}

/**
 * Readiness builds its body with json.Marshal over a map, and Go sorts map
 * keys, so the wire order is ca, db, status. A literal written in the request
 * order would never match — parse, never string-compare.
 */
async function ready(ctx: APIRequestContext): Promise<{ status: number; body: ReadyBody }> {
  const res = await ctx.get("/ready");
  return { status: res.status(), body: JSON.parse(await res.text()) as ReadyBody };
}

// Every test here stops a container, so they share the Section 3.0.4 barrier:
// serial within this file, and each restores the service and waits healthy.
test.describe.configure({ mode: "serial" });

/**
 * A container reporting healthy is not the same as step-ui's own pool having
 * recovered: /ready dials the database on every call, and the first call after
 * a restart can still see a stale connection. Each test that needs a fully
 * ready stack waits for it rather than inheriting the previous test's recovery.
 */
async function waitFullyReady(ctx: APIRequestContext): Promise<void> {
  await pollUntil(
    "/ready to report the whole stack ready",
    () => ready(ctx),
    (r) => r.status === 200 && r.body.status === "ready",
    { timeoutMs: 120_000, intervalMs: 2000 },
  );
}

test("E2E-HLTH-01: /health is unconditional", async () => {
  test.setTimeout(300_000);
  const anon = await newJar();
  const seen: string[] = [];
  try {
    for (const phase of ["everything up", "step-ca stopped", "postgres stopped"] as const) {
      if (phase === "step-ca stopped") await composeOrThrow("stop", "step-ca");
      if (phase === "postgres stopped") await composeOrThrow("stop", "postgres");

      const res = await anon.get("/health");
      seen.push(`${phase}: ${res.status()} ${(await res.text()).trim()}`);
      expect(res.status(), `GET /health with ${phase}`).toBe(200);
      // Liveness performs no database and no CA check (handlers/health.go:21-25).
      expect((await anon.get("/health")).status(), `GET /health with ${phase}`).toBe(200);
    }
    expect(seen.every((s) => s.includes('{"status":"ok"}')), `observed: ${seen.join(" | ")}`).toBe(true);
  } finally {
    await composeOrThrow("start", "postgres");
    await composeOrThrow("start", "step-ca");
    await waitHealthy("postgres");
    await waitHealthy("step-ca");
    await waitHealthy("step-ui");
    await waitFullyReady(anon);
    await anon.dispose();
  }
});

test("E2E-HLTH-03: /ready reports the CA down", async () => {
  test.setTimeout(300_000);
  const anon = await newJar();
  try {
    await waitFullyReady(anon);
    await composeOrThrow("stop", "step-ca");
    const observed = await pollUntil(
      "/ready to report the CA unreachable",
      () => ready(anon),
      (r) => r.status === 503,
      { timeoutMs: 60_000, intervalMs: 2000 },
    );
    expect(observed.status).toBe(503);
    expect(observed.body.status).toBe("not ready");
    expect(observed.body.db).toBe("ok");
    // checkCAReachability returns "unreachable" for any client.Do error, so this
    // means "the CA was not reachable" and nothing more specific.
    expect(observed.body.ca).toBe("unreachable");
  } finally {
    await anon.dispose();
  }
  // Deliberately leaves step-ca stopped: E2E-HLTH-04 depends on that end state.
});

test("E2E-HLTH-04: /ready recovers when step-ca comes back", async () => {
  test.setTimeout(300_000);
  const anon = await newJar();
  try {
    const st = await compose("ps", "--format", "json", "step-ca");
    if (!st.stdout.includes('"State":"exited"')) {
      await composeOrThrow("stop", "step-ca");
    }

    await composeOrThrow("start", "step-ca");

    // Both traces are polled in the same loop: a slow CA boot and a broken
    // checkCAReachability both present as 503, and only the direct probe of
    // step-ca's own /health separates them.
    const uiTrace: string[] = [];
    const caTrace: string[] = [];
    let caFirstOK = -1;
    let uiFirstReady = -1;

    for (let i = 0; i < 60; i++) {
      const ca = await exec("step-ca", "curl", "-sk", "https://localhost:9443/health");
      const caOK = ca.code === 0 && ca.stdout.includes('"ok"');
      caTrace.push(`${i}s ca=${caOK ? "ok" : `code=${ca.code}`}`);
      if (caOK && caFirstOK < 0) caFirstOK = i;

      let uiReady = false;
      try {
        const r = await ready(anon);
        uiReady = r.status === 200 && r.body.status === "ready";
        uiTrace.push(`${i}s ui=${r.status}/${r.body.status}`);
      } catch (err) {
        uiTrace.push(`${i}s ui=threw ${String(err)}`);
      }
      if (uiReady && uiFirstReady < 0) uiFirstReady = i;

      if (uiFirstReady >= 0) break;
      await sleep(1000);
    }

    const evidence = `\nui trace:\n${uiTrace.join("\n")}\nca trace:\n${caTrace.join("\n")}`;
    expect(uiFirstReady, `/ready never recovered within the bound${evidence}`).toBeGreaterThanOrEqual(0);
    expect(caFirstOK, `step-ca never answered its own /health${evidence}`).toBeGreaterThanOrEqual(0);
    // No caching in checkCAReachability, so there is no propagation delay
    // beyond the handshake.
    expect(uiFirstReady - caFirstOK, `/ready recovered within a second of the CA${evidence}`).toBeLessThanOrEqual(1);
  } finally {
    await waitHealthy("step-ca");
    await anon.dispose();
  }
});

test("E2E-HLTH-05: /ready reports the database down", async ({ admin }) => {
  test.setTimeout(300_000);
  const anon = await newJar();

  // A session established before the outage and left untouched throughout it.
  // The one that is used during the outage is destroyed by rejectSession, so
  // only an untouched session can show that a cookie survives the outage.
  const untouched = await newJar();
  await loginOrThrow(untouched, admin.creds);
  expect((await untouched.get("/dashboard")).status(), "the untouched session works before the outage").toBe(200);

  try {
    await composeOrThrow("stop", "postgres");

    const observed = await pollUntil(
      "/ready to report the database unreachable",
      () => ready(anon),
      (r) => r.status === 503 && r.body.db === "unreachable",
      { timeoutMs: 60_000, intervalMs: 2000 },
    );
    expect(observed.body.status).toBe("not ready");
    expect(observed.body.db).toBe("unreachable");

    // Fail-closed by design: RequireLogin re-reads the user row on every
    // request and treats a load error as a rejected session, so an outage logs
    // everyone out rather than serving stale sessions.
    const authed = await admin.ctx.get("/dashboard");
    expect(authed.status(), "an authenticated request during the outage").toBe(302);
    expect(authed.headers()["location"]).toBe("/login");
  } finally {
    await composeOrThrow("start", "postgres");
    await waitHealthy("postgres");
    await waitHealthy("step-ui");
    await waitFullyReady(anon);
    await anon.dispose();
  }

  // Current behaviour, and it is stronger than "logged out for the duration":
  // rejectSession clears s.Values and Saves, which overwrites the client's
  // cookie, so the session used during the outage is gone for good and its
  // holder must log in again. A change to leave the cookie intact through a
  // transient outage would invert this assertion.
  const stillRejected = await admin.ctx.get("/dashboard");
  expect(stillRejected.status(), "the session used during the outage stays dead").toBe(302);

  // The untouched cookie, never presented during the outage, still works with
  // no re-login: it aged past neither the idle nor the absolute limit.
  const survivor = await pollUntil(
    "the untouched pre-outage session to work again without a re-login",
    async () => (await untouched.get("/dashboard")).status(),
    (s) => s === 200,
    { timeoutMs: 60_000, intervalMs: 2000 },
  );
  expect(survivor).toBe(200);
  await untouched.dispose();
});

test("E2E-HLTH-06: /admin/integrity tracks live CA availability and nothing else", async ({ admin }) => {
  test.setTimeout(300_000);

  // One <tr> per check: name in a <strong>, status as an OK/WARN/ERROR badge,
  // detail in a <code> (templates/admin_integrity.html:97-110). Rows are split
  // first and parsed one at a time, since a single regex over the whole page
  // pairs one row's name with the next row's status. Three rows share the
  // literal name "Disk space" and differ only by detail, so the list is
  // compared by position, never by name lookup.
  const capture = async (): Promise<string[]> => {
    const res = await admin.send(() => admin.ctx.get("/admin/integrity"));
    expect(res.status()).toBe(200);
    const body = await res.text();
    // The page renders two tables: a configuration summary first, then the
    // checks. Only the second carries status badges, so it is selected by
    // content rather than by position.
    const tbody = body
      .split("<tbody>")
      .slice(1)
      .map((chunk) => chunk.slice(0, chunk.indexOf("</tbody>")))
      .find((chunk) => /<span class="badge [^"]*">(OK|WARN|ERROR)<\/span>/.test(chunk));
    expect(tbody, "the integrity page renders a checks table with status badges").toBeTruthy();
    return tbody!
      .split("<tr>")
      .slice(1)
      .map((row) => {
        const name = /<strong>([^<]+)<\/strong>/.exec(row)?.[1]?.trim() ?? "?";
        const status = /<span class="badge [^"]*">(OK|WARN|ERROR)<\/span>/.exec(row)?.[1] ?? "none";
        const detail = /<code>([\s\S]*?)<\/code>/.exec(row)?.[1]?.trim() ?? "";
        return `${name}|${status}|${detail}`;
      });
  };

  const probe = await newJar();
  await waitFullyReady(probe);
  await probe.dispose();

  const healthy = await capture();
  expect(healthy.length, "the integrity page rendered no parseable rows").toBeGreaterThan(0);

  try {
    await composeOrThrow("stop", "step-ca");
    const down = await capture();

    expect(down.length, "the same rows render in both captures").toBe(healthy.length);

    // The diff is the assertion, not "the other rows stay ok": those rows depend
    // on the read-only /home/step mount, which some stacks in this suite remove.
    const differing = healthy
      .map((before, i) => ({ before, after: down[i]! }))
      .filter(({ before, after }) => before !== after);

    expect(
      differing.map(({ before, after }) => `${before} => ${after}`),
      "exactly one row changes while the CA is down",
    ).toHaveLength(1);
    expect(differing[0]!.before, "and it is Step-CA API, previously ok").toMatch(/^Step-CA API\|OK\|/);
    expect(differing[0]!.after, "now in error").toMatch(/^Step-CA API\|ERROR\|/);
  } finally {
    await composeOrThrow("start", "step-ca");
    await waitHealthy("step-ca");
  }
});
