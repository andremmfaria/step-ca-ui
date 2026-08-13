import { newJar } from "../../helpers/session";
import { expect, test } from "../../fixtures/test";

// Oracle pair with E2E-HLTH-03: on its own this passes against a /ready that
// always answers ready, which is why the pair must never be split in triage.
test("E2E-HLTH-02: /ready with everything healthy", async () => {
  const anon = await newJar();
  try {
    const res = await anon.get("/ready");
    expect(res.status()).toBe(200);
    expect((await res.text()).trim()).toBe('{"status":"ready"}');
  } finally {
    await anon.dispose();
  }
});
