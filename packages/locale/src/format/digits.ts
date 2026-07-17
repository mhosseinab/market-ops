// Digit-family normalization at the INPUT boundary (LOC-007). Persian (۰-۹,
// U+06F0..U+06F9) and Arabic-Indic (٠-٩, U+0660..U+0669) digits are accepted and
// normalized to Latin BEFORE any calculation. Output digit family is a property
// of the active locale and is applied by the `Intl` formatters, not here.

const PERSIAN_ZERO = 0x06f0;
const ARABIC_ZERO = 0x0660;
const LATIN_ZERO = 0x30;

/** Map a single code point in a Persian/Arabic digit block to its Latin digit. */
function normalizeCodePoint(code: number): string {
  if (code >= PERSIAN_ZERO && code <= PERSIAN_ZERO + 9) {
    return String.fromCharCode(LATIN_ZERO + (code - PERSIAN_ZERO));
  }
  if (code >= ARABIC_ZERO && code <= ARABIC_ZERO + 9) {
    return String.fromCharCode(LATIN_ZERO + (code - ARABIC_ZERO));
  }
  return String.fromCharCode(code);
}

/**
 * Normalize every Persian/Arabic digit glyph in `input` to Latin 0-9. Non-digit
 * characters pass through unchanged. Pure and idempotent.
 */
export function normalizeDigits(input: string): string {
  let out = "";
  for (const ch of input) {
    out += normalizeCodePoint(ch.charCodeAt(0));
  }
  return out;
}

/**
 * Parse a user-entered numeric string (possibly with Persian/Arabic digits,
 * `٬`/`,` grouping, and a `٫`/`.` decimal mark) into a canonical Latin-digit
 * numeric string suitable for the deterministic core. Returns `null` when the
 * input does not parse to a number — the caller decides how to surface that
 * (ambiguity is never silently inferred).
 */
export function parseNumericInput(input: string): string | null {
  const latin = normalizeDigits(input)
    .replace(/[٬,\s]/g, "") // grouping separators + whitespace
    .replace(/[٫،]/g, "."); // Arabic decimal / comma → dot
  if (latin === "" || !/^[+-]?\d+(\.\d+)?$/.test(latin)) {
    return null;
  }
  return latin;
}
