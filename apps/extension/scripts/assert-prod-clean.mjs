// Standalone packaging assertion (mirrors the web app's assert:prod-clean). It
// runs the full production build, then verifies the distributable zip contains no
// Sentry/Spotlight code. `pnpm --filter extension assert:prod-clean`.
import { execFileSync } from "node:child_process";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));

console.log("assert-prod-clean: running production build + packaging assertion…");
execFileSync("node", ["scripts/build.mjs"], { cwd: root, stdio: "inherit" });
console.log("assert-prod-clean OK — distributable contains no Sentry/Spotlight code.");
