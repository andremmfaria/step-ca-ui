import { NobleCryptoPlugin, ScureBase32Plugin, TOTP } from "otplib";
import { sleep } from "./poll";

const PERIOD_SECONDS = 30;

// pquerna/otp generates a 20-byte secret (handlers/totp.go), which clears
// otplib's 16-byte guardrail, so the default guardrails stay on.
function totp(secret: string): TOTP {
  return new TOTP({
    secret,
    crypto: new NobleCryptoPlugin(),
    base32: new ScureBase32Plugin(),
    digits: 6,
    period: PERIOD_SECONDS,
  });
}

/**
 * A code generated in the last few seconds of its window can expire between
 * generation and submission, and E2E-AUTH-05's replay assertion needs the same
 * code to still be inside its window on the second submission. So wait the
 * boundary out rather than racing it.
 */
export async function totpNow(secret: string, guardSeconds = 5): Promise<string> {
  const remaining = PERIOD_SECONDS - secondsIntoWindow();
  if (remaining <= guardSeconds) {
    await sleep((remaining + 1) * 1000);
  }
  return totp(secret).generate();
}

/** A syntactically valid code that is not the current one, for the wrong-code cases. */
export async function totpWrong(secret: string): Promise<string> {
  const real = await totpNow(secret);
  const wrong = (Number(real) + 1) % 1_000_000;
  return String(wrong).padStart(6, "0");
}

export function secondsIntoWindow(): number {
  return Math.floor(Date.now() / 1000) % PERIOD_SECONDS;
}
