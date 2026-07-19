// Production build of the MV3 extension into a loadable zip.
//   1. Build the service worker + popup (ES modules).
//   2. Build the content script (self-contained IIFE).
//   3. Generate the effective manifest: inject exactly the configured gateway
//      origin (VITE_GATEWAY_BASE_URL) into host_permissions (issue #144).
//   4. Assert the distributable carries NO Sentry/Spotlight code AND that the
//      packaged manifest's host_permissions match VITE_GATEWAY_BASE_URL exactly.
//   5. Zip dist/ into build/market-ops-extension.zip (the loadable artifact CI
//      publishes).
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { assertManifestScoped, deriveManifest, resolveGatewayBaseUrl } from "./manifest.mjs";
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

const manifestPath = join(dist, "manifest.json");
if (!existsSync(manifestPath)) {
  console.error("extension build: dist/manifest.json missing — the zip would not load.");
  process.exit(2);
}

// scripts/build.mjs IS the production packaging path (pnpm build → zip), so it
// defaults to mode "production" and REQUIRES VITE_GATEWAY_BASE_URL to be set — an
// unset/empty var aborts rather than silently shipping a localhost-scoped artifact.
// The dev/unpacked flow opts in via EXT_BUILD_MODE=development, keeping the loopback
// default for local load-unpacked only.
const buildMode = process.env.EXT_BUILD_MODE === "development" ? "development" : "production";
try {
  const gatewayBaseUrl = resolveGatewayBaseUrl(process.env, buildMode);
  const sourceManifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  const effective = deriveManifest(sourceManifest, gatewayBaseUrl);
  // Cross-boundary gate: the packaged artifact's host_permissions MUST equal the
  // two Digikala origins plus exactly the configured gateway origin. A missing or
  // mismatched gateway permission, or any arbitrary/wildcard host, fails the build.
  assertManifestScoped(effective, gatewayBaseUrl);
  writeFileSync(manifestPath, `${JSON.stringify(effective, null, 2)}\n`);
  console.log(
    `extension build: manifest scoped to gateway ${gatewayBaseUrl} — host_permissions ${JSON.stringify(effective.host_permissions)}`,
  );
} catch (err) {
  console.error(`extension build: manifest generation FAILED — ${err.message}`);
  process.exit(3);
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
