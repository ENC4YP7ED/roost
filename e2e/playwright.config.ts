import { defineConfig } from "@playwright/test";

// The suite boots a real Roost binary against a throwaway database, so the
// tests exercise the shipped artefact rather than a dev server.
export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: process.env.ROOST_E2E_URL ?? "http://127.0.0.1:18500",
    headless: true,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  globalSetup: "./global-setup.ts",
  globalTeardown: "./global-teardown.ts",
});
