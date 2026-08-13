import { APIRequestContext, APIResponse } from "@playwright/test";
import { psql } from "../../helpers/compose";
import { FLAGS } from "../../helpers/env";
import { FORBIDDEN_BODY } from "../../helpers/flash";
import { csrfTokenFrom, newJar } from "../../helpers/session";
import { AuthedContext, disposableName, expect, test } from "../../fixtures/test";

type RoleName = "viewer" | "manager" | "admin";

interface Cell {
  status: number | number[];
  /** Every 200 cell carries a content assertion: this app renders errors inline at 200. */
  contains?: string;
}

interface Row {
  route: string;
  method: "GET" | "POST";
  form?: Record<string, string>;
  /** LE manager/admin cells are meaningful only against the LE stack (Section 3.14). */
  leGated?: boolean;
  cells: Partial<Record<RoleName, Cell>>;
}

const CERT_TABLE_HEADER = "All Certificates";
const AUTHED_ROWS: Row[] = [
  { route: "/", method: "GET", cells: cellAll({ status: 200, contains: "Step-CA Manager" }) },
  { route: "/dashboard", method: "GET", cells: cellAll({ status: 200, contains: "Welcome," }) },
  { route: "/api/status", method: "GET", cells: cellAll({ status: 200, contains: "{" }) },
  { route: "/certificates", method: "GET", cells: cellAll({ status: 200, contains: CERT_TABLE_HEADER }) },
  { route: "/history", method: "GET", cells: cellAll({ status: 200, contains: "Operation History" }) },
  { route: "/provisioners", method: "GET", cells: cellAll({ status: 200, contains: "Provisioners" }) },
  { route: "/profile", method: "GET", cells: cellAll({ status: 200, contains: "My Profile" }) },
  { route: "/profile/2fa", method: "GET", cells: cellAll({ status: 200, contains: "Two-Factor Authentication" }) },

  {
    route: "/issue",
    method: "GET",
    cells: { viewer: { status: 403 }, manager: { status: 200, contains: "Issue Certificate" }, admin: { status: 200, contains: "Issue Certificate" } },
  },
  {
    route: "/import",
    method: "GET",
    cells: { viewer: { status: 403 }, manager: { status: 200, contains: "Import Certificates" }, admin: { status: 200, contains: "Import Certificates" } },
  },
  {
    route: "/download/ca",
    method: "GET",
    cells: { viewer: { status: 403 }, manager: { status: 403 }, admin: { status: 200, contains: "CERTIFICATE" } },
  },
  {
    route: "/download/intermediate-ca",
    method: "GET",
    cells: { viewer: { status: 403 }, manager: { status: 403 }, admin: { status: 200, contains: "CERTIFICATE" } },
  },
  {
    route: "/download/full-chain",
    method: "GET",
    cells: { viewer: { status: 403 }, manager: { status: 403 }, admin: { status: 200, contains: "CERTIFICATE" } },
  },

  { route: "/admin", method: "GET", cells: adminOnly("Dashboard") },
  { route: "/admin/activity", method: "GET", cells: adminOnly("CA Activity") },
  { route: "/admin/security", method: "GET", cells: adminOnly("Security Log") },
  { route: "/admin/about", method: "GET", cells: adminOnly("About") },
  { route: "/admin/integrity", method: "GET", cells: adminOnly("CA integrity") },
  { route: "/admin/backup", method: "GET", cells: adminOnly("Backup") },
  { route: "/admin/console", method: "GET", cells: adminOnly("Diagnostics Console") },
  { route: "/admin/users", method: "GET", cells: adminOnly("All Users") },
  { route: "/admin/users-temp", method: "GET", cells: adminOnly("Temporary Users") },
  { route: "/admin/notifications", method: "GET", cells: adminOnly("Notifications") },

  // The viewer 403 cells hold regardless of the LE stack: RequireRole rejects
  // before any LE-specific code runs (main.go:299-308).
  { route: "/le", method: "GET", leGated: true, cells: { viewer: { status: 403 }, manager: { status: 200, contains: "Let&#39;s Encrypt" }, admin: { status: 200, contains: "Let&#39;s Encrypt" } } },
  { route: "/le/issue", method: "GET", leGated: true, cells: { viewer: { status: 403 }, manager: { status: 200, contains: "Issue LE Certificate" }, admin: { status: 200, contains: "Issue LE Certificate" } } },
  { route: "/le/settings", method: "GET", leGated: true, cells: { viewer: { status: 403 }, manager: { status: 200, contains: "Let&#39;s Encrypt Settings" }, admin: { status: 200, contains: "Let&#39;s Encrypt Settings" } } },
  { route: "/le/logs", method: "GET", leGated: true, cells: { viewer: { status: 403 }, manager: { status: 200, contains: "Let&#39;s Encrypt Log" }, admin: { status: 200, contains: "Let&#39;s Encrypt Log" } } },
];

function cellAll(cell: Cell): Partial<Record<RoleName, Cell>> {
  return { viewer: cell, manager: cell, admin: cell };
}

function adminOnly(contains: string): Partial<Record<RoleName, Cell>> {
  return { viewer: { status: 403 }, manager: { status: 403 }, admin: { status: 200, contains } };
}

function statuses(cell: Cell): number[] {
  return Array.isArray(cell.status) ? cell.status : [cell.status];
}

async function assertCell(role: RoleName, row: Row, res: APIResponse, cell: Cell): Promise<void> {
  const where = `${role} ${row.method} ${row.route}`;
  expect(statuses(cell), `${where} status`).toContain(res.status());
  const body = await res.text();
  if (res.status() === 403) {
    expect(body, `${where} body`).toBe(FORBIDDEN_BODY);
  } else if (cell.contains) {
    expect(body, `${where} content (a rendered error page also returns 200)`).toContain(cell.contains);
  }
}

test.describe("E2E-RBAC-01: the route-by-role matrix, driven as data", () => {
  for (const row of AUTHED_ROWS) {
    for (const role of ["viewer", "manager", "admin"] as const) {
      const cell = row.cells[role];
      if (!cell) continue;
      const leSkipped = row.leGated && role !== "viewer" && !FLAGS.le;

      test(`E2E-RBAC-01: ${role} ${row.method} ${row.route}`, async ({ viewer, manager, admin }) => {
        test.skip(!!leSkipped, "LE stack not enabled");
        const ctx: AuthedContext = { viewer, manager, admin }[role];
        const res = await ctx.send(() =>
          row.method === "GET" ? ctx.ctx.get(row.route) : ctx.ctx.post(row.route, { form: row.form ?? {} }),
        );
        await assertCell(role, row, res, cell);
      });
    }
  }

  /**
   * The RBAC boundary this whole table exists to state: a viewer can see that a
   * certificate exists but cannot obtain its key. Neither row states it alone.
   */
  test("E2E-RBAC-01: viewer sees /certificates/{id} but is refused /download/key/{id}", async ({ viewer, manager }) => {
    const name = disposableName("rbac-boundary");
    const token = await manager.csrf("/issue");
    const issued = await manager.send(() =>
      manager.ctx.post("/issue", {
        form: { name, domain: `${name}.e2e.invalid`, template: "server", key_type: "EC:P-256", duration: "24h", csrf_token: token },
      }),
    );
    expect([302, 303]).toContain(issued.status());

    const id = await certificateID(name);

    const details = await viewer.send(() => viewer.ctx.get(`/certificates/${id}`));
    expect(details.status(), "viewer may see that a certificate exists").toBe(200);
    expect(await details.text()).toContain(name);

    const key = await viewer.send(() => viewer.ctx.get(`/download/key/${id}`));
    expect(key.status(), "viewer may not obtain its key").toBe(403);
    expect(await key.text()).toBe(FORBIDDEN_BODY);
  });

  test("E2E-RBAC-01: manager and admin POST rows leave their declared residue", async ({ manager, admin }) => {
    for (const [role, ctx] of [["manager", manager], ["admin", admin]] as const) {
      const name = `rbac-matrix-${role}`;
      const token = await ctx.csrf("/issue");
      const res = await ctx.send(() =>
        ctx.ctx.post("/issue", {
          form: { name, domain: `${name}.e2e.invalid`, template: "server", key_type: "EC:P-256", duration: "24h", csrf_token: token },
        }),
      );
      expect([302, 303], `${role} POST /issue`).toContain(res.status());
    }

    const userToken = await admin.csrf("/admin/users");
    const scratch = disposableName("rbac-scratch");
    const created = await admin.send(() =>
      admin.ctx.post("/admin/users", {
        form: { action: "create", username: scratch, password: "Rbac-scratch-Pass1!", role: "viewer", csrf_token: userToken },
      }),
    );
    expect([200, 302, 303]).toContain(created.status());
  });

  test("E2E-RBAC-01: viewer POST rows are refused with the exact RequireRole body", async ({ viewer }) => {
    const rows: Array<{ route: string; form: Record<string, string> }> = [
      { route: "/issue", form: { name: "rbac-denied", domain: "rbac-denied.e2e.invalid" } },
      { route: "/import", form: {} },
      { route: "/revoke/1", form: {} },
      { route: "/admin/users", form: { action: "create" } },
      { route: "/admin/console", form: { command_id: "ca.health" } },
      { route: "/admin/backup/download", form: {} },
      { route: "/le/issue", form: { domain: "rbac-denied.e2e.invalid" } },
    ];
    for (const row of rows) {
      const token = await csrfTokenFrom(viewer.ctx, "/");
      const res = await viewer.send(() => viewer.ctx.post(row.route, { form: { ...row.form, csrf_token: token } }));
      expect(res.status(), `viewer POST ${row.route}`).toBe(403);
      expect(await res.text(), `viewer POST ${row.route} body`).toBe(FORBIDDEN_BODY);
    }
  });

  test("E2E-RBAC-01: manager is refused the admin-only POST routes", async ({ manager }) => {
    for (const route of ["/admin/users", "/admin/users-temp", "/admin/console", "/admin/notifications", "/admin/backup/download", "/revoke/1"]) {
      const token = await csrfTokenFrom(manager.ctx, "/");
      const res = await manager.send(() => manager.ctx.post(route, { form: { csrf_token: token } }));
      expect(res.status(), `manager POST ${route}`).toBe(403);
      expect(await res.text(), `manager POST ${route} body`).toBe(FORBIDDEN_BODY);
    }
  });
});

test.describe("E2E-RBAC-01: unauthenticated routes stay reachable", () => {
  let anon: APIRequestContext;

  test.beforeAll(async () => {
    anon = await newJar();
  });

  test.afterAll(async () => {
    await anon.dispose();
  });

  test("E2E-RBAC-01: /health answers with no session", async () => {
    const res = await anon.get("/health");
    expect(res.status()).toBe(200);
    expect((await res.text()).trim()).toBe('{"status":"ok"}');
  });

  test("E2E-RBAC-01: /ready answers JSON with no session and never redirects", async () => {
    const res = await anon.get("/ready");
    expect([200, 503]).toContain(res.status());
    expect(res.headers()["content-type"] ?? "").toContain("json");
    const body = await res.text();
    expect(() => JSON.parse(body)).not.toThrow();
  });

  test("E2E-RBAC-01: login, recovery and static routes need no session", async () => {
    for (const route of ["/login", "/forgot-password", "/reset-password"]) {
      const res = await anon.get(route);
      expect(res.status(), `GET ${route}`).toBe(200);
    }
    const css = await anon.get("/static/css/pages.css");
    expect(css.status(), "GET /static/css/pages.css").toBe(200);
  });

  test("E2E-RBAC-01: GET /logout redirects and POST /logout hits CSRF first", async () => {
    const get = await anon.get("/logout");
    expect(get.status()).toBe(302);
    expect(get.headers()["location"]).toBe("/login");

    const post = await anon.post("/logout", { form: {} });
    expect(post.status()).toBe(303);
    expect(post.headers()["location"]).toBe("/");
  });

  test("E2E-RBAC-01: the OIDC routes are registered only when OIDC is enabled", async () => {
    const res = await anon.get("/auth/oidc/login");
    if (FLAGS.oidc) {
      expect(res.status()).toBe(302);
    } else {
      expect(res.status(), "unregistered when OIDC_ENABLED is false").toBe(404);
    }
  });
});

test("E2E-RBAC-02: unauthenticated access to an authed route redirects, it does not 403", async () => {
  const anon = await newJar();
  try {
    for (const route of ["/", "/issue", "/admin"]) {
      const res = await anon.get(route);
      expect(res.status(), `GET ${route} with no session`).toBe(302);
      expect(res.headers()["location"], `GET ${route} target`).toBe("/login");
    }
  } finally {
    await anon.dispose();
  }
});

async function certificateID(name: string): Promise<string> {
  const id = await psql(`SELECT id FROM certificates WHERE name = '${name}' ORDER BY id DESC LIMIT 1`);
  if (!id) throw new Error(`no certificates row for ${name}; issuance did not land`);
  return id;
}
