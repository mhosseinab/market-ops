import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Source-level guarantee behind the packaging assertion: the dev error surface's
// ONLY reference to the dev-observability package sits INSIDE the
// `import.meta.env.DEV` guard, so a production build strips it. The packaging
// assertion (scripts/prod-clean.mjs) proves the built artifact is clean; this
// test proves the source is written so that elimination is possible.
const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "spotlight.ts"), "utf8");

describe("spotlight dev-only wiring", () => {
  it("gates the dev-observability import behind import.meta.env.DEV", () => {
    expect(src).toContain("if (!import.meta.env.DEV) return;");
  });

  it("has NO top-level import of the dev-observability package (would defeat elimination)", () => {
    const topLevel = src.split("\n").filter((l) => /^\s*import\s/.test(l));
    for (const line of topLevel) {
      expect(line.toLowerCase()).not.toContain("spotlight");
      expect(line.toLowerCase()).not.toContain("sentry");
    }
  });
});
