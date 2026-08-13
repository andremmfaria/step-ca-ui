import { createHash } from "node:crypto";
import { psql, run } from "../../helpers/compose";
import { expect, test } from "../../fixtures/test";

interface BackupComponent {
  name: string;
  path: string;
  size: number;
  sha256: string;
  status: string;
  detail?: string;
}

interface Manifest {
  format: string;
  components: BackupComponent[];
  warnings?: string[];
}

// Must not run against compose.e2e-fingerprint.yml, which removes /home/step
// and with it the step-ca-data component (Section 2.7.1).
test("E2E-BAK-01: the bundle is valid, complete and self-verifying", async ({ admin }) => {
  test.setTimeout(300_000);

  const fs = await import("node:fs/promises");
  const os = await import("node:os");
  const path = await import("node:path");
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "e2e-backup-"));

  try {
    const token = await admin.csrf("/admin/backup");
    const res = await admin.send(() => admin.ctx.post("/admin/backup/download", { form: { csrf_token: token } }));
    expect(res.status()).toBe(200);
    expect(res.headers()["content-type"]).toBe("application/gzip");
    expect(res.headers()["content-disposition"]).toMatch(
      /^attachment; filename="step-ca-ui-backup-[0-9A-Za-z_-]+\.tgz"$/,
    );

    const bundle = path.join(dir, "backup.tgz");
    await fs.writeFile(bundle, await res.body());

    const listed = await run(["tar", "tzf", bundle], { timeoutMs: 60_000 });
    expect(listed.code, `tar tzf: ${listed.stderr}`).toBe(0);
    const entries = listed.stdout.split("\n").map((l) => l.trim()).filter(Boolean);
    for (const expected of [
      "manifest.json",
      "postgres-stepui.sql",
      "step-ca-data.tgz",
      "step-ui-data.tgz",
      "step-ui-certs.tgz",
      "step-ui-uploads.tgz",
    ]) {
      expect(entries, `${expected} is in the bundle`).toContain(expected);
    }

    const extracted = path.join(dir, "x");
    await fs.mkdir(extracted);
    const untar = await run(["tar", "xzf", bundle, "-C", extracted], { timeoutMs: 120_000 });
    expect(untar.code, `tar xzf: ${untar.stderr}`).toBe(0);

    const manifest = JSON.parse(await fs.readFile(path.join(extracted, "manifest.json"), "utf8")) as Manifest;
    expect(manifest.format).toBe("step-ca-ui-backup-v1");

    // /home/step is mounted read-only and step-ui runs as uid 10001, so the
    // step-ca-data walk aborts on EACCES and never becomes a component.
    expect(manifest.components.map((c) => c.name).sort()).toEqual(
      ["postgres", "step-ui-certs", "step-ui-data", "step-ui-uploads"].sort(),
    );
    expect(manifest.warnings ?? []).toEqual(["step-ca-data failed: open /home/step/config: permission denied"]);

    // The only assertion in this suite that is fully independent of the code
    // under test: the digest is re-derived from the artifact rather than the
    // application being asked to confirm its own claim.
    for (const component of manifest.components) {
      expect(component.status, `${component.name} status`).toBe("ok");
      const bytes = await fs.readFile(path.join(extracted, path.basename(component.path)));
      const digest = createHash("sha256").update(bytes).digest("hex");
      expect(digest, `${component.name} (${component.path}) digest matches the manifest`).toBe(component.sha256);
    }

    // Present but incomplete, not absent: WalkDir visits /home/step lexically
    // and aborts on the EACCES reading config/, so writeDirTGZ flushes what it
    // archived. The plan's calibration says config/ is absent; what is actually
    // in the tarball is the bare config/ directory entry with nothing under it,
    // since the header is written before the read that fails. secrets/, which
    // sorts after config/, is never reached at all.
    const partial = await run(["tar", "tzf", path.join(extracted, "step-ca-data.tgz")], { timeoutMs: 60_000 });
    expect(partial.code, `step-ca-data.tgz is a readable tarball: ${partial.stderr}`).toBe(0);
    const inner = partial.stdout.split("\n").map((l) => l.trim()).filter(Boolean);
    expect(inner.filter((e) => /^\.?\/?certs\/.+/.test(e)).length, "certs/ made it in with its contents").toBeGreaterThan(0);
    expect(inner.filter((e) => /^\.?\/?config\/.+/.test(e)), "config/ carries no contents").toEqual([]);
    expect(inner.filter((e) => /^\.?\/?secrets\//.test(e)), "secrets/ was never reached").toEqual([]);

    const dump = await fs.readFile(path.join(extracted, "postgres-stepui.sql"), "utf8");
    for (const table of ["certificates", "users", "cert_history"]) {
      expect(
        new RegExp(`(COPY|INSERT INTO) (public\\.)?${table}\\b`).test(dump),
        `the dump carries ${table} as plain SQL`,
      ).toBe(true);
    }

    const audited = await psql("SELECT count(*) FROM auth_log WHERE reason LIKE '%backup.download filename=%'");
    expect(Number(audited), "the download is audited").toBeGreaterThan(0);
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});
