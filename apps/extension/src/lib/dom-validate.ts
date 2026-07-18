import { canonicalProductUrl } from "./normalize";
import type { ParsedProduct } from "./types";

// DOM is a fallback/VALIDATION layer only (docs/06). The API response is the
// source of truth; the DOM is cross-checked to surface drift. A mismatch is a
// drift SIGNAL recorded alongside the evidence — never a reason to block the
// capture or to overwrite the API value (docs/10 step 4).
export interface DomSignals {
  // A canonical-URL mismatch between DOM and API (the page shows a different
  // product than the API returned) — a hard drift signal.
  urlMismatch: boolean;
  // The exact `ناموجود` unavailable badge is present in the DOM.
  domUnavailable: boolean;
  // The DOM and API disagree on availability.
  availabilityMismatch: boolean;
}

const UNAVAILABLE_BADGE = "ناموجود";

// validateAgainstDom cross-checks a parsed product against the live document
// using ONLY the primary selectors in docs/06 (canonical link, exact badge text).
// Utility/atomic CSS class names are never used as selectors.
export function validateAgainstDom(doc: Document, parsed: ParsedProduct): DomSignals {
  const canonicalHref = doc.querySelector('link[rel="canonical"]')?.getAttribute("href") ?? null;
  const domCanonical = canonicalHref ? canonicalProductUrl(canonicalHref) : null;
  const urlMismatch = domCanonical !== null && domCanonical !== parsed.canonicalUrl;

  const domUnavailable = documentHasBadge(doc, UNAVAILABLE_BADGE);
  const apiUnavailable =
    parsed.availability === "out_of_stock" || parsed.availability === "unavailable";
  // A missing badge is valid for in-stock (docs/06). Only a genuine disagreement
  // (DOM says unavailable, API says in-stock, or vice-versa) is a mismatch.
  const availabilityMismatch = domUnavailable !== apiUnavailable && domCanonical !== null;

  return { urlMismatch, domUnavailable, availabilityMismatch };
}

function documentHasBadge(doc: Document, badge: string): boolean {
  // Match the exact badge text on a small element, not a substring of the whole
  // page (which would false-positive on descriptions). Scan short text nodes.
  const candidates = doc.querySelectorAll("span, div, p, button");
  for (const el of Array.from(candidates)) {
    const text = (el.textContent ?? "").trim();
    if (text === badge) return true;
  }
  return false;
}
