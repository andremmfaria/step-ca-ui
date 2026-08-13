/** The message requireCSRF flashes before redirecting (handlers/handler.go:325-332). */
export const CSRF_FLASH = "Session error. Please refresh the page.";

/** RequireRole writes this exact body, trailing newline included (middleware/middleware.go:151). */
export const FORBIDDEN_BODY = "403 Forbidden\n";

export function countOccurrences(haystack: string, needle: string): number {
  if (needle === "") return 0;
  return haystack.split(needle).length - 1;
}

/**
 * This application renders its error paths inline at 200, so a status code alone
 * never identifies a failure — every content assertion carries the body it saw.
 */
export function assertContains(body: string, needle: string, context: string): void {
  if (!body.includes(needle)) {
    throw new Error(`${context}: expected body to contain ${JSON.stringify(needle)}; body was:\n${body.slice(0, 1500)}`);
  }
}
