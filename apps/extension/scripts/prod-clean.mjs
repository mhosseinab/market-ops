// Shared packaging assertion: the distributable extension must contain NO
// Sentry/Spotlight (dev-only) code. The dev error surface lives behind
// `import.meta.env.DEV`, which Vite replaces with `false` in a production build,
// so esbuild dead-code-eliminates it. This walks the built dist and fails on any
// Sentry/Spotlight reference in the shipped JS.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const FORBIDDEN = [/sentry/i, /spotlight/i];

function jsFiles(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      out.push(...jsFiles(p));
    } else if (/\.(js|mjs)$/.test(name)) {
      out.push(p);
    }
  }
  return out;
}

// assertProdClean throws with the list of offenders if any shipped JS references
// Sentry/Spotlight. Returns the number of files scanned on success.
export function assertProdClean(distDir) {
  const files = jsFiles(distDir);
  const offenders = [];
  for (const file of files) {
    const contents = readFileSync(file, "utf8");
    for (const pattern of FORBIDDEN) {
      if (pattern.test(contents)) offenders.push(`${file} contains ${pattern}`);
    }
  }
  if (offenders.length > 0) {
    throw new Error(
      `packaging assertion FAILED — dev observability leaked into the distributable:\n  ${offenders.join("\n  ")}`,
    );
  }
  return files.length;
}
