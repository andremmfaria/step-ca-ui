import { run } from "../../helpers/compose";
import { certificateID } from "../../helpers/certs";
import { expect, test } from "../../fixtures/test";

const ISSUED = "e2e-server-ec-p256";

test("E2E-CERT-09: per-certificate downloads carry an attachment filename", async ({ manager }) => {
  const id = await certificateID(ISSUED);

  const cert = await manager.send(() => manager.ctx.get(`/download/cert/${id}`));
  expect(cert.status()).toBe(200);
  expect(cert.headers()["content-disposition"]).toBe(`attachment; filename=${ISSUED}.crt`);
  expect(await cert.text()).toContain("BEGIN CERTIFICATE");

  const key = await manager.send(() => manager.ctx.get(`/download/key/${id}`));
  expect(key.status()).toBe(200);
  expect(key.headers()["content-disposition"]).toBe(`attachment; filename=${ISSUED}.key`);
  expect(await key.text()).toContain("PRIVATE KEY");
});

test("E2E-CERT-09: the CA downloads carry their fixed filenames", async ({ admin }) => {
  const cases: Array<[string, string]> = [
    ["/download/ca", "home-ca-root.crt"],
    ["/download/intermediate-ca", "home-ca-intermediate.crt"],
    ["/download/full-chain", "home-ca-full-chain.crt"],
  ];
  for (const [route, filename] of cases) {
    const res = await admin.send(() => admin.ctx.get(route));
    expect(res.status(), `GET ${route}`).toBe(200);
    expect(res.headers()["content-disposition"], `GET ${route} filename`).toBe(`attachment; filename=${filename}`);
    expect(await res.text(), `GET ${route} body`).toContain("BEGIN CERTIFICATE");
  }
});

test("E2E-CERT-09: full-chain is the intermediate followed by the root, and nothing else", async ({ admin }) => {
  const res = await admin.send(() => admin.ctx.get("/download/full-chain"));
  const chain = await res.text();

  const fs = await import("node:fs/promises");
  const os = await import("node:os");
  const path = await import("node:path");
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-chain-"));
  try {
    const file = path.join(dir, "full-chain.crt");
    await fs.writeFile(file, chain);

    // A third-party parser counting the certificates is the assertion; a
    // substring count over the PEM would pass on a duplicated intermediate.
    const parsed = await run(["bash", "-lc", `openssl crl2pkcs7 -nocrl -certfile ${file} | openssl pkcs7 -print_certs -noout`]);
    const subjects = parsed.stdout.split("\n").filter((l) => l.startsWith("subject="));
    expect(subjects.length, `full-chain holds exactly two certificates; got:\n${parsed.stdout}`).toBe(2);

    const intermediate = await admin.send(() => admin.ctx.get("/download/intermediate-ca"));
    const root = await admin.send(() => admin.ctx.get("/download/ca"));
    expect(chain.indexOf((await intermediate.text()).trim().split("\n")[1] ?? ""), "intermediate comes first").toBeLessThan(
      chain.indexOf((await root.text()).trim().split("\n")[1] ?? ""),
    );
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});
