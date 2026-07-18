import { resolve } from "node:path";
import { defineConfig } from "vitest/config";

// Main extension build: the MV3 service worker (ES module) and the popup page.
// The content script is built separately as a self-contained IIFE
// (vite.content.config.ts) because a declarative content script runs as a
// classic script and must carry no import statements.
//
// The dev-only Spotlight wiring (src/lib/spotlight.ts) is gated at RUNTIME behind
// `import.meta.env.DEV`, which Vite statically replaces with `false` in a
// production build — dead-code-eliminating the import. scripts/assert-prod-clean
// proves the packaged zip carries no Sentry/Spotlight code.
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    modulePreload: false,
    rollupOptions: {
      input: {
        "service-worker": resolve(import.meta.dirname, "src/background/service-worker.ts"),
        popup: resolve(import.meta.dirname, "popup.html"),
      },
      output: {
        entryFileNames: "[name].js",
        chunkFileNames: "chunks/[name].js",
        assetFileNames: "assets/[name][extname]",
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    include: ["src/**/*.test.ts"],
  },
});
