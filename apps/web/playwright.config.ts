import { defineConfig, devices } from "@playwright/test";

// Playwright config for the journey-1 smoke against the REAL core (dk-p0-monorepo
// §3: `task dev` stack + `task db:reset` seeded fixtures). Chromium is
// preinstalled at /opt/pw-browsers (PLAYWRIGHT_BROWSERS_PATH); never run
// `playwright install` here.
//
// The web app is served from the production build via `vite preview`; the gateway
// origin is baked in at build time through VITE_GATEWAY_BASE_URL (default points
// at the local core). Optional E2E_EMAIL / E2E_PASSWORD let the smoke open a
// session against the seeded owner before driving the UI.

const WEB_PORT = Number(process.env.E2E_WEB_PORT ?? 4173);
const BASE_URL = process.env.E2E_WEB_URL ?? `http://localhost:${WEB_PORT}`;

export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 30_000,
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: BASE_URL,
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: process.env.E2E_WEB_URL
    ? undefined
    : {
        // Build with the gateway origin baked in, then serve the static build.
        command: `pnpm run build && pnpm exec vite preview --port ${WEB_PORT} --strictPort`,
        url: BASE_URL,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
