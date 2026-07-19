import { defineConfig, devices } from "@playwright/test";

// Playwright config for the LOC-011 pseudo-locale VISUAL/layout gate (issue #15).
// Unlike the journey e2e config (which drives the REAL core), this serves a
// self-contained dev harness — `vite --mode pseudo` renders the real shell under
// the pseudo pack with MSW supplying fixtures — so it runs in the fast gate
// environment with no compose stack. Chromium is preinstalled at /opt/pw-browsers
// (PLAYWRIGHT_BROWSERS_PATH); never run `playwright install` here.

const WEB_PORT = Number(process.env.PSEUDO_WEB_PORT ?? 4319);
const BASE_URL = process.env.PSEUDO_WEB_URL ?? `http://localhost:${WEB_PORT}`;

export default defineConfig({
  testDir: "./tests/pseudo",
  timeout: 30_000,
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  // Baselines live beside the specs, reviewed intentionally in review (a diff to
  // a committed PNG is a visible, deliberate change — never a silent one).
  snapshotPathTemplate: "{testDir}/__screenshots__/{arg}{ext}",
  expect: {
    toHaveScreenshot: {
      // Tolerate sub-pixel antialiasing only; a real overflow/clip/direction
      // change moves far more than this and also trips the deterministic asserts.
      maxDiffPixelRatio: 0.02,
      animations: "disabled",
      caret: "hide",
    },
  },
  use: {
    baseURL: BASE_URL,
    viewport: { width: 1280, height: 800 },
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: process.env.PSEUDO_WEB_URL
    ? undefined
    : {
        command: `pnpm exec vite --mode pseudo --port ${WEB_PORT} --strictPort`,
        url: BASE_URL,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
