import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./specs",
  fullyParallel: false,
  retries: 0,
  reporter: [["html", { open: "never" }]],
  globalSetup: "./global-setup.ts",
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:8080",
    storageState: "storageState.json",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
