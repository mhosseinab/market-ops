// LTR isolation of technical identifiers in PLAIN-STRING contexts (LOC-005,
// design/LOCALIZATION.md).
//
// In rendered markup a technical identifier (SKU, offer identity, URL, id) is
// isolated with CSS — `direction: ltr; unicode-bidi: isolate` — which is what the
// `LtrToken` component does. That has NO effect in a plain string: an `aria-label`,
// a `title`, or a screen-reader announcement carries no CSS, so a Latin identifier
// embedded in RTL copy is reordered by the Unicode bidirectional algorithm and can be
// announced or displayed with its parts transposed.
//
// The Unicode-level equivalent is an explicit isolate: U+2066 LEFT-TO-RIGHT ISOLATE
// opens a run whose direction is LTR and whose resolution cannot leak into the
// surrounding text, and U+2069 POP DIRECTIONAL ISOLATE closes it. The same pair the
// pseudo-locale generator already uses.
//
// This is NOT a locale branch: it is applied unconditionally to identifiers in every
// locale, because an identifier's direction is a property of the IDENTIFIER, not of
// the reader's language (PRD §11 — locale is data, and no direction branch lives in
// application code).

// The SINGLE source for the Unicode isolate controls in this package: the
// pseudo-locale generator wraps its messages with the same pair, and two private
// copies of the same invisible characters are exactly the kind of duplication that
// drifts silently (DRY).
/** U+2066 LEFT-TO-RIGHT ISOLATE. */
export const LRI = "⁦";
/** U+2069 POP DIRECTIONAL ISOLATE. */
export const PDI = "⁩";

/**
 * Wrap a technical identifier in a Unicode LTR isolate so it survives interpolation
 * into RTL copy in a plain-string context (aria-label, title, announcements).
 *
 * An empty value is returned unchanged: isolating nothing would inject two invisible
 * control characters into a string an assistive technology then announces as
 * non-empty.
 */
export function ltrIsolate(value: string): string {
  if (value === "") return value;
  return `${LRI}${value}${PDI}`;
}
