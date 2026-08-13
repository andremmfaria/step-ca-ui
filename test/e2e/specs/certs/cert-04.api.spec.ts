import { psql } from "../../helpers/compose";
import { certificateID, downloadCert, keyDetails } from "../../helpers/certs";
import { validityMs } from "../../helpers/openssl";
import { expect, test } from "../../fixtures/test";

// Distinct from the certificate E2E-CERT-05 revokes: renewal and revocation
// interfere, so the two must not share a subject.
const SUBJECT = "e2e-dur-8760h";

test("E2E-CERT-04: renew", async ({ manager }) => {
  test.setTimeout(180_000);

  const originalID = await certificateID(SUBJECT);
  const original = await downloadCert(manager.ctx, originalID);
  const originalKey = await keyDetails(original.pem);
  const originalValidity = validityMs(original);
  const rowsBefore = Number(await psql(`SELECT count(*) FROM certificates WHERE name = '${SUBJECT}'`));

  const token = await manager.csrf("/certificates");
  const res = await manager.send(() => manager.ctx.post(`/renew/${originalID}`, { form: { csrf_token: token } }));
  expect(res.status()).toBe(302);
  expect(res.headers()["location"]).toBe("/certificates");
  expect(await (await manager.ctx.get("/certificates")).text()).toContain("Certificate renewed");

  // Renew inserts a new row rather than updating the old one, so the stale
  // pre-renewal serial survives in the database while the file holds the new
  // certificate. The file is the oracle; neither row is.
  const rowsAfter = Number(await psql(`SELECT count(*) FROM certificates WHERE name = '${SUBJECT}'`));
  expect(rowsAfter, "renew inserts, it does not update").toBe(rowsBefore + 1);

  const newestID = await certificateID(SUBJECT);
  expect(newestID).not.toBe(originalID);

  const renewed = await downloadCert(manager.ctx, newestID);
  expect(renewed.serial, "the file carries a new serial").not.toBe(original.serial);
  expect(renewed.notAfter.getTime(), "and a later expiry").toBeGreaterThan(original.notAfter.getTime());

  // Renew reuses the stored KeyType/IssueDuration, falling back only for rows
  // that predate those columns.
  expect(await keyDetails(renewed.pem), "key type is carried over").toEqual(originalKey);
  expect(Math.abs(validityMs(renewed) - originalValidity), "validity window is carried over").toBeLessThanOrEqual(60_000);

  const history = await psql(
    `SELECT count(*) FROM cert_history WHERE action = 'renew' AND cert_name = '${SUBJECT}'`,
  );
  expect(Number(history), "a renew history row exists").toBeGreaterThan(0);
});
