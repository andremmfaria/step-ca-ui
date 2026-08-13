import { run } from "./compose";

export interface ProbedCert {
  issuer: string;
  subject: string;
  serial: string;
  notBefore: Date;
  notAfter: Date;
  sans: string[];
  sha256: string;
  pem: string;
  /** A self-signed fallback has issuer equal to subject; a CA-issued leaf does not. */
  selfSigned: boolean;
}

export interface ProbeOptions {
  servername?: string;
  timeoutMs?: number;
}

/** Completes a real handshake against host:port and parses the served leaf. */
export async function probeTLS(hostPort: string, opts: ProbeOptions = {}): Promise<ProbedCert> {
  const servername = opts.servername ? `-servername ${opts.servername}` : "";
  const res = await run(
    ["bash", "-lc", `openssl s_client -connect ${hostPort} ${servername} </dev/null 2>/dev/null | openssl x509 -outform pem`],
    { timeoutMs: opts.timeoutMs ?? 30_000 },
  );
  const pem = res.stdout.trim();
  if (!pem.includes("BEGIN CERTIFICATE")) {
    throw new Error(`no certificate served by ${hostPort}: exit ${res.code}, stderr: ${res.stderr}`);
  }
  return parsePEM(pem);
}

/** execFile cannot feed openssl on stdin, so the PEM goes through a temp file. */
export async function parsePEM(pem: string): Promise<ProbedCert> {
  const fs = await import("node:fs/promises");
  const os = await import("node:os");
  const path = await import("node:path");
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-cert-"));
  const file = path.join(dir, "leaf.pem");
  try {
    await fs.writeFile(file, pem);
    const out = await run(
      ["openssl", "x509", "-in", file, "-noout", "-issuer", "-subject", "-serial", "-dates", "-fingerprint", "-sha256", "-ext", "subjectAltName"],
      { timeoutMs: 15_000 },
    );
    const text = out.stdout;
    const issuer = field(text, /^issuer=(.*)$/m);
    const subject = field(text, /^subject=(.*)$/m);
    return {
      issuer,
      subject,
      serial: field(text, /^serial=(.*)$/m),
      notBefore: new Date(field(text, /^notBefore=(.*)$/m)),
      notAfter: new Date(field(text, /^notAfter=(.*)$/m)),
      sans: (text.match(/(DNS|IP Address|email):[^,\n]+/g) ?? []).map((s) => s.trim()),
      sha256: field(text, /Fingerprint=(.*)$/m),
      pem,
      selfSigned: issuer === subject,
    };
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
}

function field(text: string, re: RegExp): string {
  const m = re.exec(text);
  if (!m || m[1] === undefined) throw new Error(`could not parse ${re} out of:\n${text}`);
  return m[1].trim();
}

/** Validity window in milliseconds, which every duration assertion compares against a tolerance. */
export function validityMs(cert: ProbedCert): number {
  return cert.notAfter.getTime() - cert.notBefore.getTime();
}
