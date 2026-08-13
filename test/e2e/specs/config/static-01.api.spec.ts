import * as tls from "node:tls";
import { BASE_URL, REPO_ROOT } from "../../helpers/env";
import { expect, test } from "../../fixtures/test";

// mimeByExt exists because a minimal image's /etc/mime.types maps .css to
// text/plain (main.go:43-65). A regression there leaves every page at 200 with
// correct-looking bytes while the browser refuses to apply the stylesheet, and
// no status assertion anywhere else in this suite would notice.
const EXPECTED_TYPES: Record<string, string> = {
  ".css": "text/css; charset=utf-8",
  ".js": "application/javascript; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
};

/**
 * A normal HTTP client normalises "/static/../templates/base.html" to
 * "/templates/base.html" before it leaves the process, so the server would
 * never see a literal "..". The request line has to go out over a raw socket
 * for the traversal defence to be exercised at all.
 */
function rawRequest(requestLine: string): Promise<string> {
  const url = new URL(BASE_URL);
  return new Promise((resolve, reject) => {
    const socket = tls.connect(
      { host: url.hostname, port: Number(url.port || 443), rejectUnauthorized: false, servername: url.hostname },
      () => {
        socket.write(`${requestLine}\r\nHost: ${url.host}\r\nConnection: close\r\n\r\n`);
      },
    );
    let data = "";
    socket.setTimeout(20_000, () => socket.destroy(new Error(`timed out on: ${requestLine}`)));
    socket.on("data", (chunk) => (data += chunk.toString("latin1")));
    socket.on("end", () => resolve(data));
    socket.on("error", reject);
  });
}

function statusOf(response: string): number {
  return Number(/^HTTP\/1\.[01] (\d{3})/.exec(response)?.[1] ?? 0);
}

test("E2E-STATIC-01: static assets are served with their enforced MIME types", async ({ admin }) => {
  const assets = await discoverAssets();
  for (const [ext, path] of Object.entries(assets)) {
    const res = await admin.ctx.get(path);
    expect(res.status(), `GET ${path}`).toBe(200);
    expect(res.headers()["content-type"], `Content-Type for ${ext}`).toBe(EXPECTED_TYPES[ext]);
  }
});

test("E2E-STATIC-01: static assets resist traversal on a raw socket", async () => {
  test.setTimeout(120_000);

  // In-subtree positive control, sent the same way: without it, a 404 on every
  // traversal attempt would also be satisfied by a broken raw-socket path.
  const control = await rawRequest("GET /static/css/pages.css HTTP/1.1");
  expect(statusOf(control), `the raw-socket request path itself works:\n${control.slice(0, 300)}`).toBe(200);

  // templates/ is embedded as a sibling of static/ (main.go:40-41), so it is a
  // real file a genuine boundary escape could reach. main.go itself is never
  // embedded and would 404 whether or not the defence works.
  const attempts = [
    "GET /static/../templates/base.html HTTP/1.1",
    "GET /static/%2e%2e/templates/base.html HTTP/1.1",
    "GET /static/....//templates/base.html HTTP/1.1",
  ];
  for (const line of attempts) {
    const res = await rawRequest(line);
    const status = statusOf(res);
    expect([400, 404], `${line} was refused, got ${status}`).toContain(status);
    expect(res, `${line} leaked no template bytes`).not.toContain("<!DOCTYPE html>");
    expect(res, `${line} leaked no template source`).not.toContain("{{define");
  }
});

/** Picks one real asset per extension out of the embedded static tree. */
async function discoverAssets(): Promise<Record<string, string>> {
  const fs = await import("node:fs/promises");
  const path = await import("node:path");
  const root = path.join(REPO_ROOT, "step-ui-go", "static");

  const found: Record<string, string> = {};
  const walk = async (dir: string): Promise<void> => {
    for (const entry of await fs.readdir(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        await walk(full);
        continue;
      }
      const ext = path.extname(entry.name);
      if (EXPECTED_TYPES[ext] && !found[ext]) {
        found[ext] = `/static/${path.relative(root, full).split(path.sep).join("/")}`;
      }
    }
  };
  await walk(root);
  expect(Object.keys(found).length, "the static tree carries assets to check").toBeGreaterThan(0);
  return found;
}
