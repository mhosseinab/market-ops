import { resolve } from "node:path";
import { defineConfig } from "vite";

// MAIN-world nav-shim build (S31). Injected via chrome.scripting.executeScript
// with `files: ["nav-shim.js"]` and `world: "MAIN"` — that API requires a real
// packaged file, not an inline string, so this needs its OWN self-contained
// IIFE build (same reasoning as vite.content.config.ts). emptyOutDir is false
// so this adds to the shared dist/ rather than wiping the other builds.
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: false,
    sourcemap: false,
    lib: {
      entry: resolve(import.meta.dirname, "src/content/nav-shim.ts"),
      formats: ["iife"],
      name: "MarketOpsNavShim",
      fileName: () => "nav-shim.js",
    },
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
        extend: true,
      },
    },
  },
});
