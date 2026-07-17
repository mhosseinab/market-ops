import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The Sentry/Spotlight dev-observability wiring is env-gated at RUNTIME behind
// `import.meta.env.DEV && VITE_SENTRY_SPOTLIGHT` (see app/observability.ts);
// scripts/assert-prod-clean.mjs proves the production bundle carries no Sentry
// or Spotlight code.
export default defineConfig({
  plugins: [react()],
  build: {
    sourcemap: false,
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    // Absolute gateway base so the undici fetch under jsdom can parse the URL;
    // MSW handlers match on path (origin-agnostic `*` prefix).
    env: { VITE_GATEWAY_BASE_URL: "http://localhost/api" },
  },
});
