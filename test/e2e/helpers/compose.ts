import { execFile } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import { promisify } from "node:util";
import { REPO_ROOT } from "./env";
import { pollUntil } from "./poll";

const execFileAsync = promisify(execFile);

export interface RunResult {
  code: number;
  stdout: string;
  stderr: string;
}

/**
 * The api/ui projects reach the stack through a mounted docker socket rather
 * than through a second, purpose-built mechanism per assertion (Section 2.7.2):
 * exec, logs, stop/start and psql all go through this one path.
 */
export async function run(argv: string[], opts: { cwd?: string; timeoutMs?: number; env?: NodeJS.ProcessEnv } = {}): Promise<RunResult> {
  const [cmd, ...args] = argv;
  if (!cmd) throw new Error("run() called with an empty argv");
  try {
    const { stdout, stderr } = await execFileAsync(cmd, args, {
      cwd: opts.cwd ?? REPO_ROOT,
      timeout: opts.timeoutMs ?? 120_000,
      maxBuffer: 64 * 1024 * 1024,
      env: { ...process.env, ...opts.env },
    });
    return { code: 0, stdout, stderr };
  } catch (err) {
    const e = err as { code?: number; stdout?: string; stderr?: string; message?: string };
    return {
      code: typeof e.code === "number" ? e.code : 1,
      stdout: e.stdout ?? "",
      stderr: e.stderr ?? e.message ?? "",
    };
  }
}

/** Extra `-f` files the driver selected for this stack, as a colon-separated list. */
function overrideFiles(): string[] {
  const raw = process.env.E2E_COMPOSE_OVERRIDES ?? "";
  return raw
    .split(":")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function composeArgv(...args: string[]): string[] {
  const files = ["docker-compose.yml", ...overrideFiles()].flatMap((f) => ["-f", f]);
  // A bootstrap scenario edits a copy of .env rather than the checked-out one.
  const envFile = process.env.E2E_ENV_FILE ? ["--env-file", process.env.E2E_ENV_FILE] : [];
  return ["docker", "compose", ...files, ...envFile, ...args];
}

export async function compose(...args: string[]): Promise<RunResult> {
  return run(composeArgv(...args));
}

export async function composeOrThrow(...args: string[]): Promise<string> {
  const res = await compose(...args);
  if (res.code !== 0) {
    throw new Error(`docker compose ${args.join(" ")} failed (${res.code}): ${res.stderr || res.stdout}`);
  }
  return res.stdout;
}

export async function exec(service: string, ...cmd: string[]): Promise<RunResult> {
  return compose("exec", "-T", service, ...cmd);
}

/** Runs a query as the stepui role and returns rows as unaligned, tuple-only text. */
export async function psql(sql: string): Promise<string> {
  const res = await compose("exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-U", "stepui", "-d", "stepui", "-At", "-c", sql);
  if (res.code !== 0) throw new Error(`psql failed: ${res.stderr || res.stdout}\nSQL: ${sql}`);
  return res.stdout.trim();
}

export async function psqlRows(sql: string): Promise<string[]> {
  const out = await psql(sql);
  return out === "" ? [] : out.split("\n");
}

export interface LogOptions {
  /** RFC3339 timestamp or a relative offset such as "30s"; scopes an absence assertion to this test. */
  since?: string;
  tail?: number;
}

/**
 * --timestamps is mandatory everywhere: every slog.Debug line is invisible under
 * the default handler, so a retry count is only ever inferable from the
 * timestamps on the INFO lines bracketing a loop (Section 2.6).
 */
export async function logs(service: string, opts: LogOptions = {}): Promise<string> {
  const args = ["logs", "--no-color", "--timestamps"];
  if (opts.since) args.push("--since", opts.since);
  if (opts.tail !== undefined) args.push("--tail", String(opts.tail));
  args.push(service);
  const res = await compose(...args);
  return res.stdout + res.stderr;
}

/**
 * A docker compose recreate replaces the container and `logs` only ever shows
 * the current one, so any test that recreates step-ui appends the outgoing
 * container's log here first. E2E-SEC-03's full-log sweep and collect.sh read
 * this file rather than calling `logs` directly (Section 2.6).
 */
export function cumulativeLogPath(): string {
  return process.env.E2E_CUMULATIVE_LOG ?? path.join(REPO_ROOT, "test", "e2e", "artifacts", "step-ui-cumulative.log");
}

export async function captureBeforeRecreate(service = "step-ui"): Promise<void> {
  const current = await logs(service);
  const target = cumulativeLogPath();
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.appendFileSync(target, current);
}

export async function cumulativeLog(service = "step-ui"): Promise<string> {
  const target = cumulativeLogPath();
  const previous = fs.existsSync(target) ? fs.readFileSync(target, "utf8") : "";
  return previous + (await logs(service));
}

/** Number of lines containing an exact string — the offset form of a log gate (Section 3.0.5). */
export function countExact(haystack: string, needle: string): number {
  return haystack.split("\n").filter((line) => line.includes(needle)).length;
}

export async function waitForLogLine(
  service: string,
  exact: string,
  opts: { timeoutMs: number; since?: string; minCount?: number } = { timeoutMs: 90_000 },
): Promise<string> {
  const minCount = opts.minCount ?? 1;
  const matched = await pollUntil(
    `log line ${JSON.stringify(exact)} in ${service} (at least ${minCount} occurrence(s))`,
    async () => logs(service, { since: opts.since }),
    (out) => countExact(out, exact) >= minCount,
    { timeoutMs: opts.timeoutMs, intervalMs: 2000 },
  );
  const line = matched.split("\n").find((l) => l.includes(exact));
  return line ?? "";
}

export interface ServiceStatus {
  Name: string;
  Service: string;
  State: string;
  Health: string;
}

export async function serviceStatus(service: string): Promise<ServiceStatus | undefined> {
  const res = await compose("ps", "--all", "--format", "json", service);
  const lines = res.stdout.split("\n").filter((l) => l.trim().startsWith("{"));
  for (const line of lines) {
    const parsed = JSON.parse(line) as ServiceStatus;
    if (parsed.Service === service) return parsed;
  }
  return undefined;
}

/**
 * The healthcheck's own ceiling is start_period 20s + interval 10s x retries 10,
 * so the default bound is a real timeout rather than an unbounded wait.
 */
export async function waitHealthy(service: string, timeoutMs = 180_000): Promise<void> {
  await pollUntil(
    `${service} to report healthy`,
    async () => {
      const st = await serviceStatus(service);
      return st ? `${st.State}/${st.Health}` : "<no such container>";
    },
    (v) => v.endsWith("/healthy"),
    { timeoutMs, intervalMs: 3000 },
  );
}

/** A restart clears both process-local rate limiters; an .env edit needs a recreate instead. */
export async function restartUI(): Promise<void> {
  await captureBeforeRecreate("step-ui");
  await composeOrThrow("restart", "step-ui");
  await waitHealthy("step-ui");
}

export async function recreate(service: string, env: Record<string, string> = {}): Promise<void> {
  if (service === "step-ui") await captureBeforeRecreate("step-ui");
  const res = await run(composeArgv("up", "-d", "--force-recreate", service), { env });
  if (res.code !== 0) throw new Error(`recreating ${service} failed: ${res.stderr || res.stdout}`);
  await waitHealthy(service);
}

export async function timestampMarker(): Promise<string> {
  return new Date().toISOString();
}

/** Resolves through `compose ps -q` rather than the bare service/container name, which is ambiguous whenever more than one compose project is live on the host. */
export async function containerId(service: string): Promise<string> {
  const res = await compose("ps", "-q", service);
  const id = res.stdout.trim().split("\n")[0];
  if (!id) throw new Error(`no container id for service ${service} (docker compose ps -q returned nothing)`);
  return id;
}

export interface ContainerState {
  Status: string;
  ExitCode: number;
}

export async function inspectState(service: string): Promise<ContainerState> {
  const id = await containerId(service);
  const res = await run(["docker", "inspect", "--format", "{{json .State}}", id]);
  if (res.code !== 0) throw new Error(`docker inspect ${id} failed: ${res.stderr}`);
  return JSON.parse(res.stdout.trim()) as ContainerState;
}

/** Bounded at 90s: E2E-BOOT-07 case (c) delays its fatal by entrypoint.sh's own 60x1s wait-for-postgres loop. */
export async function waitExited(service: string, timeoutMs = 90_000): Promise<ContainerState> {
  return pollUntil(
    `${service} to exit`,
    () => inspectState(service),
    (s) => s.Status === "exited",
    { timeoutMs, intervalMs: 2000 },
  );
}

/** CI substitutes the prebuilt image and never passes --build; a local run needs it to pick up source changes. */
export function upFlags(): string[] {
  const usesPrebuiltImage = overrideFiles().includes("compose.e2e-image.yml");
  return usesPrebuiltImage ? [] : ["--build"];
}

/** `up -d`, with a longer timeout than compose()'s default 120s: a cold `--build` runs past it. */
export async function upStack(...services: string[]): Promise<void> {
  const res = await run(composeArgv("up", "-d", ...upFlags(), ...services), { timeoutMs: 5 * 60_000 });
  if (res.code !== 0) throw new Error(`docker compose up -d failed (${res.code}): ${res.stderr || res.stdout}`);
}

/** Matches Makefile's E2E_PROJECT: compose derives the project name the same way when COMPOSE_PROJECT_NAME is unset. */
export function projectName(): string {
  return process.env.COMPOSE_PROJECT_NAME ?? path.basename(REPO_ROOT);
}

const STEP_UI_VOLUMES = ["step-ui-certs", "step-ui-ssl", "step-ui-data", "step-ui-uploads"];

/** Drops step-ui's own volumes while leaving step-ca-data, and the CA identity a fingerprint was computed from, intact. Call after `compose("down")` (no `-v`). */
export async function resetStepUIVolumes(): Promise<void> {
  const proj = projectName();
  for (const vol of STEP_UI_VOLUMES) {
    await run(["docker", "volume", "rm", `${proj}_${vol}`]);
  }
}
