import react from "@vitejs/plugin-react";
import type { UserConfig } from "vite";

// Vite 8 + React. `test` is consumed by Vitest at runtime; it is typed loosely
// here because Vitest 3.2's bundled Vite 7 types don't unify with Vite 8's
// rolldown plugin types (a type-only mismatch, not a runtime one).
//
// The Sentry/Spotlight dev-observability wiring is env-gated at RUNTIME behind
// `import.meta.env.DEV && VITE_SENTRY_SPOTLIGHT` (see app/observability.ts);
// scripts/assert-prod-clean.mjs proves the production bundle carries no Sentry
// or Spotlight code.
const config: UserConfig & { test: Record<string, unknown> } = {
  plugins: [react()],
  build: {
    sourcemap: false,
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
  },
};

export default config;
