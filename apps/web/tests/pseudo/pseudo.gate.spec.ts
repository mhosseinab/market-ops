import { PSEUDO_DIR, PSEUDO_ID } from "@market-ops/locale";
import { expect, test } from "@playwright/test";
import {
  CRITICAL_SELECTORS,
  collectPseudoLayout,
  PSEUDO_HARNESS_PATH,
  PSEUDO_ROUTES,
} from "./layout";

// LOC-011 pseudo-locale VISUAL gate (issue #15). The vitest pseudo suite proves
// copy resolves through the catalog; jsdom performs no layout, so it cannot see
// clipping/overflow/direction regressions. This gate renders the REAL shell in
// Chromium under the pseudo pack (expanded + forced PSEUDO_DIR) and fails on:
//   • the document not carrying PSEUDO_DIR / the pseudo lang (the whole point of
//     the pack — direction is applied to the rendered root);
//   • horizontal overflow of the shell;
//   • clipped single-line critical copy;
//   • any copy-bearing element hardcoding a direction ≠ the pseudo direction;
//   • a screenshot drift from the reviewed baseline.

for (const route of PSEUDO_ROUTES) {
  test(`pseudo shell renders without layout or direction regression: ${route.name}`, async ({
    page,
  }) => {
    await page.goto(`${PSEUDO_HARNESS_PATH}#${route.path}`);
    await page.waitForFunction(() => document.documentElement.dataset.pseudoReady === "1");
    await expect(page.locator(".app-shell")).toBeVisible();

    const report = await page.evaluate(collectPseudoLayout, {
      selectors: [...CRITICAL_SELECTORS],
      expectedDir: PSEUDO_DIR,
    });

    // PSEUDO_DIR is applied to the rendered document root (acceptance #1).
    expect(report.dir, "document dir must be PSEUDO_DIR").toBe(PSEUDO_DIR);
    expect(report.lang, "document lang must be the pseudo pack").toBe(PSEUDO_ID);

    expect(
      report.rootOverflowPx,
      `horizontal overflow (${report.rootOverflowPx}px) on ${route.name}`,
    ).toBeLessThanOrEqual(1);
    expect(report.clipped, `clipped critical copy on ${route.name}`).toEqual([]);
    expect(report.directionViolations, `hardcoded direction on ${route.name}`).toEqual([]);

    // Reviewed visual baseline for the pseudo shell (acceptance #4).
    await expect(page).toHaveScreenshot(`${route.name}.png`);
  });
}
