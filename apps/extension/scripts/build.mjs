// Production build of the MV3 extension into a loadable zip.
//   1. Build the service worker + popup (ES modules).
//   2. Build the content script (self-contained IIFE).
//   3. Assert the distributable carries NO Sentry/Spotlight code.
//   4. Zip dist/ into build/market-ops-extension.zip (the loadable artifact CI
//      publishes).
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { assertProdClean } from "./prod-clean.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dist = join(root, "dist");
const buildDir = join(root, "build");
const zipPath = join(buildDir, "market-ops-extension.zip");

function vite(args) {
  execFileSync("pnpm", ["exec", "vite", ...args], { cwd: root, stdio: "inherit" });
}

console.log("extension build: bundling service worker + popup…");
vite(["build"]);
console.log("extension build: bundling content script (IIFE)…");
vite(["build", "--config", "vite.content.config.ts"]);
console.log("extension build: bundling MAIN-world nav shim (IIFE)…");
vite(["build", "--config", "vite.navshim.config.ts"]);

if (!existsSync(join(dist, "manifest.json"))) {
  console.error("extension build: dist/manifest.json missing — the zip would not load.");
  process.exit(2);
}

const scanned = assertProdClean(dist);
console.log(
  `extension build: packaging assertion OK — ${scanned} JS files carry no Sentry/Spotlight code.`,
);

mkdirSync(buildDir, { recursive: true });
rmSync(zipPath, { force: true });
// Zip the CONTENTS of dist so the manifest is at the archive root (loadable).
execFileSync("zip", ["-r", "-q", zipPath, "."], { cwd: dist, stdio: "inherit" });
console.log(`extension build: wrote ${zipPath}`);
