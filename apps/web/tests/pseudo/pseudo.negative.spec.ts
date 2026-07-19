import { PSEUDO_DIR } from "@market-ops/locale";
import { expect, test } from "@playwright/test";
import { CRITICAL_SELECTORS, collectPseudoLayout } from "./layout";

// Negative fixture (issue #15 acceptance #5): a page carrying KNOWN defects —
// a fixed-width clipping container, a viewport-overflowing row, and a hardcoded
// RTL element — proves the gate FAILS on real regressions. It exercises the same
// `collectPseudoLayout` detector the live-shell gate uses, so this is direct
// evidence that a genuine app regression would be caught, not silently passed.

const DEFECT_HTML = `
  <main style="font-family: sans-serif; padding: 8px;">
    <!-- Clipping: a fixed-width, single-line box narrower than expanded copy. -->
    <button class="badge" style="width:60px;overflow:hidden;white-space:nowrap;">
      ⟦Expánded pséudo lábel ····⟧
    </button>
    <!-- Horizontal overflow: a nowrap row far wider than the viewport. -->
    <div class="panel__title" style="white-space:nowrap;width:3000px;">
      ⟦Á row wíder thán the víewport thát fórces horízontal overflów ····⟧
    </div>
    <!-- Direction-sensitive: copy that hardcodes RTL against the pseudo dir. -->
    <h2 dir="rtl">⟦Hárdcoded RTL héading ····⟧</h2>
  </main>`;

test("detector fails on known clipping, overflow, and direction defects", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.setContent(
    `<!doctype html><html dir="${PSEUDO_DIR}" lang="pseudo"><head><meta charset="utf-8" /></head><body style="margin:0">${DEFECT_HTML}</body></html>`,
  );

  const report = await page.evaluate(collectPseudoLayout, {
    selectors: [...CRITICAL_SELECTORS],
    expectedDir: PSEUDO_DIR,
  });

  expect(report.rootOverflowPx, "the viewport-overflowing row must be detected").toBeGreaterThan(1);
  expect(report.clipped.length, "the clipped fixed-width label must be detected").toBeGreaterThan(
    0,
  );
  expect(
    report.directionViolations.length,
    "the hardcoded RTL heading must be detected",
  ).toBeGreaterThan(0);
});
