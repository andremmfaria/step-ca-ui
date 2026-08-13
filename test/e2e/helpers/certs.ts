import { APIRequestContext } from "@playwright/test";
import { exec, psql, run } from "./compose";
import { ProbedCert, parsePEM } from "./openssl";

export interface IssueRequest {
  name: string;
  domain: string;
  template: "server" | "internal" | "wildcard" | "client";
  keyType: "EC:P-256" | "EC:P-384" | "RSA:2048" | "RSA:4096";
  duration: "720h" | "4380h" | "8760h" | "87600h";
}

/** The flash IssuePost writes on success (handlers/certs.go:220). */
export function issuedFlash(req: IssueRequest): string {
  return `Certificate ${req.name} for ${req.domain} issued (${req.keyType})!`;
}

export async function issue(ctx: APIRequestContext, csrfToken: string, req: IssueRequest) {
  return ctx.post("/issue", {
    form: {
      name: req.name,
      domain: req.domain,
      template: req.template,
      key_type: req.keyType,
      duration: req.duration,
      csrf_token: csrfToken,
    },
    // RSA-4096 keygen plus the CA round trip must not be cut short by the
    // client, or a WriteTimeout finding presents as a client timeout instead.
    timeout: 120_000,
  });
}

export async function certificateID(name: string): Promise<string> {
  const id = await psql(`SELECT id FROM certificates WHERE name = '${name}' ORDER BY id DESC LIMIT 1`);
  if (!id) throw new Error(`no certificates row for ${name}`);
  return id;
}

/**
 * Assertions read the downloaded material, never the row: IssuePost writes the
 * request back into the row, so a build whose CA client ignored key_type
 * entirely would still produce a byte-identical row (Section 3.4 preamble).
 */
export async function downloadCert(ctx: APIRequestContext, id: string): Promise<ProbedCert> {
  const res = await ctx.get(`/download/cert/${id}`);
  if (res.status() !== 200) {
    throw new Error(`GET /download/cert/${id} returned ${res.status()}: ${(await res.text()).slice(0, 300)}`);
  }
  return parsePEM(await res.text());
}

export async function downloadKey(ctx: APIRequestContext, id: string): Promise<string> {
  const res = await ctx.get(`/download/key/${id}`);
  if (res.status() !== 200) {
    throw new Error(`GET /download/key/${id} returned ${res.status()}: ${(await res.text()).slice(0, 300)}`);
  }
  return res.text();
}

export interface KeyDetails {
  algorithm: "id-ecPublicKey" | "rsaEncryption" | string;
  /** "P-256" / "P-384" for EC, "" for RSA. */
  curve: string;
  /** Modulus size for RSA, 0 for EC. */
  rsaBits: number;
}

export async function keyDetails(pem: string): Promise<KeyDetails> {
  const fs = await import("node:fs/promises");
  const os = await import("node:os");
  const path = await import("node:path");
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-key-"));
  const file = path.join(dir, "leaf.pem");
  try {
    await fs.writeFile(file, pem);
    const out = await run(["openssl", "x509", "-in", file, "-noout", "-text"], { timeoutMs: 20_000 });
    const text = out.stdout;
    const algorithm = /Public Key Algorithm:\s*(\S+)/.exec(text)?.[1] ?? "";
    const curve = /ASN1 OID:\s*(\S+)/.exec(text)?.[1] ?? "";
    const bits = /Public-Key:\s*\((\d+) bit\)/.exec(text)?.[1] ?? "0";
    return {
      algorithm,
      curve: curve === "prime256v1" ? "P-256" : curve === "secp384r1" ? "P-384" : curve,
      rsaBits: algorithm === "rsaEncryption" ? Number(bits) : 0,
    };
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
}

export function expectedKey(keyType: IssueRequest["keyType"]): KeyDetails {
  switch (keyType) {
    case "EC:P-256":
      return { algorithm: "id-ecPublicKey", curve: "P-256", rsaBits: 0 };
    case "EC:P-384":
      return { algorithm: "id-ecPublicKey", curve: "P-384", rsaBits: 0 };
    case "RSA:2048":
      return { algorithm: "rsaEncryption", curve: "", rsaBits: 2048 };
    case "RSA:4096":
      return { algorithm: "rsaEncryption", curve: "", rsaBits: 4096 };
  }
}

/** The DNS SANs, with the "DNS:" prefix stripped, so length can be asserted exactly. */
export function dnsNames(cert: ProbedCert): string[] {
  return cert.sans.filter((s) => s.startsWith("DNS:")).map((s) => s.slice("DNS:".length));
}

/**
 * Checks the key parses and that it pairs with the certificate — two artifacts
 * from different code paths, cross-checked by a third-party parser.
 */
export async function assertKeyPairs(certPEM: string, keyPEM: string): Promise<void> {
  const fs = await import("node:fs/promises");
  const os = await import("node:os");
  const path = await import("node:path");
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-pair-"));
  const certFile = path.join(dir, "cert.pem");
  const keyFile = path.join(dir, "key.pem");
  try {
    await fs.writeFile(certFile, certPEM);
    await fs.writeFile(keyFile, keyPEM);

    const isRSA = keyPEM.includes("RSA PRIVATE KEY") || (await run(["openssl", "pkey", "-in", keyFile, "-noout", "-text"])).stdout.includes("RSA");
    const check = await run(["openssl", isRSA ? "rsa" : "ec", "-in", keyFile, "-check", "-noout"], { timeoutMs: 20_000 });
    if (check.code !== 0) throw new Error(`private key failed openssl check: ${check.stderr}`);

    const certMod = await run(["bash", "-lc", `openssl x509 -in ${certFile} -noout -pubkey | openssl md5`]);
    const keyMod = await run(["bash", "-lc", `openssl pkey -in ${keyFile} -pubout | openssl md5`]);
    if (certMod.stdout.trim() !== keyMod.stdout.trim()) {
      throw new Error(`certificate and key do not pair: ${certMod.stdout.trim()} vs ${keyMod.stdout.trim()}`);
    }
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
}

/** Whether the issued material exists on the container's own filesystem. */
export async function certFileExists(name: string): Promise<boolean> {
  const res = await exec("step-ui", "test", "-s", `/opt/step-ui/certs/${name}/certificate.crt`);
  return res.code === 0;
}
