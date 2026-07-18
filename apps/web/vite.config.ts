import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

const DEV_SESSION_PATH = "/api/dev/session";
const CORE_LOGIN_URL = "http://127.0.0.1:8080/auth/login";

function devSessionPlugin(): Plugin {
  return {
    name: "market-ops-dev-session",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use(async (request, response, next) => {
        const path = request.url?.split("?", 1)[0];
        if (request.method !== "POST" || path !== DEV_SESSION_PATH) {
          next();
          return;
        }

        const email = process.env.MARKET_OPS_DEV_OWNER_EMAIL;
        const password = process.env.MARKET_OPS_DEV_OWNER_PASSWORD;
        if (!email || !password) {
          response.statusCode = 503;
          response.setHeader("content-type", "application/json");
          response.end(JSON.stringify({ code: "DEV_SESSION_UNAVAILABLE" }));
          return;
        }

        try {
          const upstream = await fetch(CORE_LOGIN_URL, {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({ email, password }),
          });
          response.statusCode = upstream.status;
          response.setHeader("cache-control", "no-store");
          const contentType = upstream.headers.get("content-type");
          if (contentType) response.setHeader("content-type", contentType);
          const sessionCookie = upstream.headers.get("set-cookie");
          if (sessionCookie) response.setHeader("set-cookie", sessionCookie);
          response.end(await upstream.text());
        } catch {
          response.statusCode = 502;
          response.setHeader("content-type", "application/json");
          response.end(JSON.stringify({ code: "DEV_SESSION_UPSTREAM_UNAVAILABLE" }));
        }
      });
    },
  };
}

// The Sentry/Spotlight dev-observability wiring is env-gated at RUNTIME behind
// `import.meta.env.DEV && VITE_SENTRY_SPOTLIGHT` (see app/observability.ts);
// scripts/assert-prod-clean.mjs proves the production bundle carries no Sentry
// or Spotlight code.
export default defineConfig({
  plugins: [react(), devSessionPlugin()],
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ""),
      },
    },
  },
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
