/**
 * Bounded poll-until-predicate. Section 4.7 forbids a bare `sleep N`: a fixed
 * sleep either wastes time or races, and a race produces exactly the flake the
 * no-retry policy refuses to paper over. On expiry this throws with the last
 * observed value, so the failure carries its own evidence.
 */
export async function pollUntil<T>(
  describe: string,
  probe: () => Promise<T> | T,
  predicate: (value: T) => boolean,
  opts: { timeoutMs: number; intervalMs?: number },
): Promise<T> {
  const intervalMs = opts.intervalMs ?? 2000;
  const deadline = Date.now() + opts.timeoutMs;
  let last: T | undefined;
  let lastError: unknown;

  for (;;) {
    try {
      last = await probe();
      lastError = undefined;
      if (predicate(last)) return last;
    } catch (err) {
      lastError = err;
    }
    if (Date.now() >= deadline) {
      const observed = lastError
        ? `last probe threw: ${String(lastError)}`
        : `last observed value: ${format(last)}`;
      throw new Error(`timed out after ${opts.timeoutMs}ms waiting for ${describe}; ${observed}`);
    }
    await sleep(Math.min(intervalMs, Math.max(0, deadline - Date.now())));
  }
}

/** Only for a deliberate real-time wait that is itself the property under test. */
export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function format(value: unknown): string {
  if (value === undefined) return "<never probed successfully>";
  if (typeof value === "string") return value.length > 2000 ? `${value.slice(0, 2000)}…` : value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}
