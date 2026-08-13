import { logs, psql } from "../../helpers/compose";
import {
  IssueRequest,
  assertKeyPairs,
  certFileExists,
  certificateID,
  dnsNames,
  downloadCert,
  downloadKey,
  expectedKey,
  issue,
  issuedFlash,
  keyDetails,
} from "../../helpers/certs";
import { validityMs } from "../../helpers/openssl";
import { expect, test } from "../../fixtures/test";

const HOUR = 3_600_000;
const MINUTE = 60_000;

// safeName allows only [A-Za-z0-9._-], so a name carrying the key type verbatim
// (e2e-server-EC:P-256) is rejected before anything reaches the CA.
const MATRIX: IssueRequest[] = [
  // The boundary probe runs first: 87600h equals STEPCA_MAX_TLS_CERT_DURATION
  // exactly, and validityValidator rejects only what exceeds the maximum. If the
  // boundary behaves differently every other row fails for the same reason.
  { name: "e2e-internal-ec-p256", template: "internal", domain: "e2e-internal.example.com", duration: "87600h", keyType: "EC:P-256" },

  { name: "e2e-server-ec-p256", template: "server", domain: "e2e-server.example.com", duration: "8760h", keyType: "EC:P-256" },
  { name: "e2e-server-ec-p384", template: "server", domain: "e2e-server.example.com", duration: "8760h", keyType: "EC:P-384" },
  { name: "e2e-server-rsa2048", template: "server", domain: "e2e-server.example.com", duration: "8760h", keyType: "RSA:2048" },
  { name: "e2e-server-rsa4096", template: "server", domain: "e2e-server.example.com", duration: "8760h", keyType: "RSA:4096" },

  { name: "e2e-wildcard-ec-p256", template: "wildcard", domain: "*.e2e-wildcard.example.com", duration: "8760h", keyType: "EC:P-256" },
  { name: "e2e-client-ec-p256", template: "client", domain: "e2e-client.example.com", duration: "8760h", keyType: "EC:P-256" },

  { name: "e2e-dur-720h", template: "server", domain: "e2e-dur.example.com", duration: "720h", keyType: "EC:P-256" },
  { name: "e2e-dur-4380h", template: "server", domain: "e2e-dur.example.com", duration: "4380h", keyType: "EC:P-256" },
  { name: "e2e-dur-8760h", template: "server", domain: "e2e-dur.example.com", duration: "8760h", keyType: "EC:P-256" },
  { name: "e2e-dur-87600h", template: "server", domain: "e2e-dur.example.com", duration: "87600h", keyType: "EC:P-256" },
];

const observedKeys = new Map<string, string>();
const observedValidity = new Map<string, number>();

test.describe("E2E-CERT-01: issuance matrix", () => {
  for (const row of MATRIX) {
    test(`E2E-CERT-01: ${row.name} (${row.template}, ${row.keyType}, ${row.duration})`, async ({ manager }) => {
      test.setTimeout(180_000);

      const token = await manager.csrf("/issue");
      const res = await issue(manager.ctx, token, row);
      expect(res.status(), `POST /issue for ${row.name}`).toBe(302);
      expect(res.headers()["location"]).toBe("/issue");

      const page = await manager.ctx.get("/issue");
      expect(await page.text()).toContain(issuedFlash(row));

      expect(await certFileExists(row.name), "issued material on disk").toBe(true);

      const id = await certificateID(row.name);
      const cert = await downloadCert(manager.ctx, id);
      const key = await downloadKey(manager.ctx, id);

      expect(cert.subject, "Subject.CommonName is the requested domain").toContain(`CN=${row.domain}`);

      // Risk R4's CSR-shape half. "Contains the domain" would be satisfied by
      // both a CN-only and a DNSNames-only CSR, the two wrong shapes.
      expect(dnsNames(cert), `${row.name} DNSNames`).toEqual([row.domain]);

      const details = await keyDetails(cert.pem);
      expect(details, `${row.name} key parameters read from the downloaded certificate`).toEqual(expectedKey(row.keyType));

      await assertKeyPairs(cert.pem, key);

      const requestedMs = Number(row.duration.replace("h", "")) * HOUR;
      expect(Math.abs(validityMs(cert) - requestedMs), `${row.name} validity within a minute of ${row.duration}`).toBeLessThanOrEqual(MINUTE);

      observedKeys.set(row.name, `${details.algorithm}/${details.curve}/${details.rsaBits}`);
      observedValidity.set(row.name, validityMs(cert));
    });
  }

  test("E2E-CERT-01: the key axis yields four distinct public-key parameter sets", () => {
    const axis = ["e2e-server-ec-p256", "e2e-server-ec-p384", "e2e-server-rsa2048", "e2e-server-rsa4096"];
    const seen = axis.map((n) => observedKeys.get(n));
    expect(seen, "every key-axis row was observed").not.toContain(undefined);
    // A build stuck on EC P-256 fails here and nowhere else.
    expect(new Set(seen).size, `distinct key parameters across ${axis.join(", ")}: ${seen.join(" | ")}`).toBe(4);
  });

  test("E2E-CERT-01: the duration axis yields four distinct windows in the requested order", () => {
    const axis = ["e2e-dur-720h", "e2e-dur-4380h", "e2e-dur-8760h", "e2e-dur-87600h"];
    const seen = axis.map((n) => observedValidity.get(n));
    expect(seen, "every duration-axis row was observed").not.toContain(undefined);
    expect(new Set(seen).size, `distinct validity windows: ${seen.join(" | ")}`).toBe(4);
    const values = seen as number[];
    for (let i = 1; i < values.length; i++) {
      expect(values[i]!, `${axis[i]} is longer than ${axis[i - 1]}`).toBeGreaterThan(values[i - 1]!);
    }
  });
});

test("E2E-CERT-02: the wildcard template rejects a non-wildcard domain", async ({ manager }) => {
  const before = await psql("SELECT count(*) FROM certificates");
  const token = await manager.csrf("/issue");

  const res = await issue(manager.ctx, token, {
    name: "e2e-wildcard-rejected",
    domain: "not-a-wildcard.example.com",
    template: "wildcard",
    keyType: "EC:P-256",
    duration: "8760h",
  });

  // The policy-error branch renders inline; it does not redirect.
  expect(res.status()).toBe(200);
  expect(await res.text()).toContain("Policy error: wildcard template requires domain like *.example.com");

  expect(await psql("SELECT count(*) FROM certificates"), "no row was created").toBe(before);
  expect(await certFileExists("e2e-wildcard-rejected"), "no file under CertsDir").toBe(false);
});

test("E2E-CERT-03: an invalid domain is rejected before it reaches the CA", async ({ manager }) => {
  test.setTimeout(180_000);

  // Positive control first: an absence assertion over a log is unfalsifiable on
  // its own, so the same pattern must be shown to fire in this environment
  // before anything is concluded from its absence.
  const controlName = `e2e-control-${Date.now().toString(36)}`;
  const controlSince = new Date().toISOString();
  const token = await manager.csrf("/issue");
  const control = await issue(manager.ctx, token, {
    name: controlName,
    domain: "e2e-control.example.com",
    template: "server",
    keyType: "EC:P-256",
    duration: "8760h",
  });
  expect(control.status(), "control issuance").toBe(302);

  const SIGN_PATTERN = "/sign";
  const controlLog = await logs("step-ca", { since: controlSince });
  const controlHits = controlLog.split("\n").filter((l) => l.includes(SIGN_PATTERN)).length;
  expect(controlHits, `positive control: the CA logs ${SIGN_PATTERN} in this environment`).toBeGreaterThan(0);

  const since = new Date().toISOString();

  const flagInjection = await issue(manager.ctx, await manager.csrf("/issue"), {
    name: "e2e-badid-dash",
    domain: "--foo",
    template: "server",
    keyType: "EC:P-256",
    duration: "8760h",
  });
  expect(flagInjection.status()).toBe(200);
  // html/template escapes the quotes validateIdentifier's %q adds, so the
  // rendered form is what the assertion compares against.
  expect(await flagInjection.text()).toContain(
    "Error: identifier &#34;--foo&#34; starts with &#39;-&#39;: possible flag injection",
  );

  const shellish = await issue(manager.ctx, await manager.csrf("/issue"), {
    name: "e2e-badid-shell",
    domain: "; rm -rf /",
    template: "server",
    keyType: "EC:P-256",
    duration: "8760h",
  });
  expect(shellish.status()).toBe(200);
  expect(await shellish.text()).toContain("contains disallowed characters");

  const after = await logs("step-ca", { since });
  const hits = after.split("\n").filter((l) => l.includes(SIGN_PATTERN));
  expect(hits.length, `no signing request reached the CA; matched lines:\n${hits.join("\n")}`).toBe(0);
});
