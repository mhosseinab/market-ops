// Shared config + browser-context detector for the LOC-011 pseudo-locale VISUAL
// gate (issue #15). The same `collectPseudoLayout` runs against the live shell
// (pseudo.gate.spec — asserts clean) and against a deliberately broken fixture
// (pseudo.negative.spec — asserts detected), so a genuine app regression is
// caught by the identical logic the negative fixture proves fails.

/** Dev harness entry served by `vite --mode pseudo` (index.pseudo.html). */
export const PSEUDO_HARNESS_PATH = "/index.pseudo.html";

/** Desktop viewport the baselines are captured at. */
export const PSEUDO_VIEWPORT = { width: 1280, height: 800 } as const;

/**
 * Representative shell routes — one per top-level area (design/IA_AND_COMPONENTS)
 * plus the onboarding capability surface — rendered full-shell under the pseudo
 * pack so expansion/direction regressions surface across the critical states.
 */
export const PSEUDO_ROUTES = [
  { name: "today", path: "/today" },
  { name: "products", path: "/products" },
  { name: "market", path: "/market" },
  { name: "actions", path: "/actions" },
  { name: "settings", path: "/settings" },
  { name: "operations", path: "/operations" },
  { name: "onboarding", path: "/onboarding" },
] as const;

/**
 * Single-line, copy-bearing chrome that clips first under pseudo expansion:
 * nav labels, badges/pills, section titles, buttons, headings, the toolbar. A
 * regression here (a fixed width, a hardcoded direction) is what the gate exists
 * to catch.
 */
export const CRITICAL_SELECTORS = [
  ".badge",
  ".stepper__label",
  ".panel__title",
  ".capability-gate__note",
  ".toolbar__search",
  ".nav-item",
  "button",
  "h1",
  "h2",
] as const;

export interface PseudoLayoutReport {
  readonly dir: string | null;
  readonly lang: string | null;
  /** Document horizontal overflow in CSS px (>1 ⇒ the shell overflows). */
  readonly rootOverflowPx: number;
  /** Copy-bearing boxes narrower than their single-line content (clipped). */
  readonly clipped: { selector: string; text: string }[];
  /** Copy-bearing elements whose resolved direction ≠ the expected direction. */
  readonly directionViolations: { selector: string; dir: string }[];
}

/**
 * Runs IN THE BROWSER (Playwright serializes this function into the page, so it
 * is intentionally self-contained — no imports, no outer references). It reports
 * the three regression classes pseudo-localization is meant to expose:
 * horizontal overflow, clipped critical copy, and direction-sensitive layout.
 */
export function collectPseudoLayout(arg: {
  selectors: readonly string[];
  expectedDir: string;
}): PseudoLayoutReport {
  const { selectors, expectedDir } = arg;
  const root = document.documentElement;
  const clipped: { selector: string; text: string }[] = [];
  const directionViolations: { selector: string; dir: string }[] = [];

  for (const selector of selectors) {
    for (const el of Array.from(document.querySelectorAll(selector))) {
      const style = getComputedStyle(el);
      const text = (el.textContent ?? "").trim();
      if (text.length === 0) continue;

      // Direction is DATA (LOC-005): a copy-bearing element that resolves to a
      // different direction than the pseudo document has hardcoded direction.
      if (style.direction !== expectedDir) {
        directionViolations.push({ selector, dir: style.direction });
      }

      // Clip: a single-line or overflow-hidden box whose content is wider than
      // its box. Multi-line wrapping copy is fine; only truncation is a defect.
      const singleLine = style.whiteSpace === "nowrap" || style.textOverflow === "ellipsis";
      const hidesOverflow = style.overflowX === "hidden" || style.overflowX === "clip";
      if ((singleLine || hidesOverflow) && el.scrollWidth - el.clientWidth > 1) {
        clipped.push({ selector, text: text.slice(0, 40) });
      }
    }
  }

  return {
    dir: root.getAttribute("dir"),
    lang: root.getAttribute("lang"),
    rootOverflowPx: root.scrollWidth - root.clientWidth,
    clipped,
    directionViolations,
  };
}
