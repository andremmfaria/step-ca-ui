import { defineConfig, devices } from "@playwright/test";
import * as path from "node:path";

// api/ui run as a container on step-network and dial the compose service name;
// infra runs on the host and dials the published port. Section 4.1.2.
const BASE_URL = process.env.BASE_URL ?? "https://step-ui:8443";

// Playwright's junit reporter and actions/upload-artifact are configured
// independently, so both are pinned to this one directory.
const ARTIFACT_DIR = process.env.E2E_ARTIFACT_DIR ?? path.join(__dirname, "artifacts");

const isCI = !!process.env.CI;

export default defineConfig({
  testDir: path.join(__dirname, "specs"),
  outputDir: path.join(ARTIFACT_DIR, "test-results"),

  // Section 4.7: no test-level retries anywhere. A green-on-retry e2e test is
  // indistinguishable from the intermittent regression this suite exists to catch.
  retries: 0,
  forbidOnly: isCI,
  workers: 1,

  // Auto-waiting covers the browser tier only; container healthchecks and
  // certificate serials go through the bounded-poll helpers instead.
  timeout: 120_000,
  expect: { timeout: 15_000 },
  globalTimeout: 45 * 60_000,

  reporter: [
    ["list"],
    ["junit", { outputFile: path.join(ARTIFACT_DIR, "junit.xml") }],
    ["html", { outputFolder: path.join(ARTIFACT_DIR, "html-report"), open: "never" }],
  ],

  use: {
    baseURL: BASE_URL,
    // The UI's serving cert never carries a SAN for the name the harness dials:
    // the self-signed fallback covers localhost and HOST_IP, and a stepca-issued
    // leaf covers resolveUIHostname's answer. Without this every request fails
    // the handshake (tlsbootstrap.go).
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },

  projects: [
    {
      name: "api",
      testMatch: /.*\.api\.spec\.ts/,
      fullyParallel: false,
    },
    {
      name: "ui",
      testMatch: /.*\.ui\.spec\.ts/,
      fullyParallel: false,
      use: { ...devices["Desktop Chrome"], ignoreHTTPSErrors: true },
    },
    {
      name: "infra",
      testMatch: /.*\.infra\.spec\.ts/,
      fullyParallel: false,
      // Bootstrap scenarios bring their own stack up from cold and wait on
      // healthchecks, which is minutes of stack time rather than test time.
      timeout: 15 * 60_000,
    },
  ],
});
