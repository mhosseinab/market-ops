#!/usr/bin/env node
// Build assertion: the PRODUCTION bundle must contain NO Sentry/Spotlight code.
// The dev-observability wiring lives behind `import.meta.env.DEV && …`, which
// Vite statically replaces with `false` in a prod build, dead-code-eliminating
// the dynamic `@sentry/react` import. This proves it (S25 assignment).

import { execSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const distAssets = join(webRoot, "dist", "assets");

console.log("assert-prod-clean: building production bundle…");
execSync("pnpm exec vite build", { cwd: webRoot, stdio: "inherit" });

if (!existsSync(distAssets)) {
  console.error(`assert-prod-clean: no build output at ${distAssets}`);
  process.exit(2);
}

const FORBIDDEN = [/sentry/i, /spotlight/i];
const offenders = [];

for (const file of readdirSync(distAssets)) {
  if (!/\.(js|mjs)$/.test(file)) continue;
  const contents = readFileSync(join(distAssets, file), "utf8");
  for (const pattern of FORBIDDEN) {
    if (pattern.test(contents)) {
      offenders.push(`${file} contains ${pattern}`);
    }
  }
}

if (offenders.length > 0) {
  console.error("assert-prod-clean FAILED — dev observability leaked into prod bundle:");
  for (const o of offenders) console.error(`  ${o}`);
  process.exit(1);
}

console.log("assert-prod-clean OK — production bundle contains no Sentry/Spotlight code.");
