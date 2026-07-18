import { resolve } from "node:path";
import { defineConfig } from "vite";

// Content-script build. A declarative MV3 content script runs as a CLASSIC script
// (not an ES module), so it must be a single self-contained file with no import
// statements — hence a dedicated IIFE build with all dynamic imports inlined.
// emptyOutDir is false so this build adds to the main build's dist rather than
// wiping it.
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: false,
    sourcemap: false,
    lib: {
      entry: resolve(import.meta.dirname, "src/content/content-script.ts"),
      formats: ["iife"],
      name: "MarketOpsCapture",
      fileName: () => "content-script.js",
    },
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
        extend: true,
      },
    },
  },
});
