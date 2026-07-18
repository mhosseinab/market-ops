import { en } from "./catalog/en";
import type { Catalog, MessageKey } from "./catalog/keys";
import { MESSAGE_KEYS } from "./catalog/keys";

// Pseudo-localization pack (LOC-011). Derived from the English authoring catalog,
// it is: EXPANDED (~40% longer, to surface clipping), BRACKETED (⟦…⟧, to surface
// untranslated / hardcoded strings that never pass through the catalog), and
// FORCED-LTR (wrapped in LRI/PDI isolates + a forced-ltr direction, to surface
// direction-broken layout). ICU argument blocks ({name}, {count, plural, …}) are
// left intact so the message still parses.

export const PSEUDO_ID = "pseudo" as const;
export const PSEUDO_DIR = "ltr" as const;
export const PSEUDO_OPEN = "⟦";
export const PSEUDO_CLOSE = "⟧";
const LRI = "⁦"; // Left-to-Right Isolate
const PDI = "⁩"; // Pop Directional Isolate

const ACCENT: Record<string, string> = {
  a: "á",
  b: "ƀ",
  c: "ç",
  d: "ð",
  e: "é",
  f: "ƒ",
  g: "ğ",
  h: "ĥ",
  i: "í",
  j: "ĵ",
  k: "ķ",
  l: "ļ",
  m: "ɱ",
  n: "ñ",
  o: "ó",
  p: "þ",
  q: "ɋ",
  r: "ř",
  s: "š",
  t: "ţ",
  u: "ú",
  v: "ṽ",
  w: "ŵ",
  x: "ẍ",
  y: "ý",
  z: "ž",
};

function accentChar(ch: string): string {
  const lower = ch.toLowerCase();
  const mapped = ACCENT[lower];
  if (!mapped) return ch;
  return ch === ch.toUpperCase() ? mapped.toUpperCase() : mapped;
}

/** Accent only text OUTSIDE ICU argument braces so the message stays parseable. */
function accentOutsideBraces(message: string): string {
  let out = "";
  let depth = 0;
  for (const ch of message) {
    if (ch === "{") {
      depth++;
      out += ch;
    } else if (ch === "}") {
      depth = Math.max(0, depth - 1);
      out += ch;
    } else if (depth > 0) {
      out += ch;
    } else {
      out += accentChar(ch);
    }
  }
  return out;
}

/** Transform one message into its pseudo form (expanded + bracketed + LTR-isolated). */
export function pseudoMessage(message: string): string {
  const accented = accentOutsideBraces(message);
  const visibleLen = accented.replace(/\s/g, "").length;
  const pad = "·".repeat(Math.max(3, Math.ceil(visibleLen * 0.4)));
  return `${LRI}${PSEUDO_OPEN}${accented} ${pad}${PSEUDO_CLOSE}${PDI}`;
}

/** Build the full pseudo catalog from a base catalog (defaults to English). */
export function buildPseudoCatalog(base: Catalog = en): Catalog {
  const out = {} as Record<MessageKey, string>;
  for (const key of MESSAGE_KEYS) {
    out[key] = pseudoMessage(base[key]);
  }
  return out;
}

/** A rendered string is "translated" iff it passed through the pseudo catalog. */
export function isPseudoTranslated(rendered: string): boolean {
  return rendered.includes(PSEUDO_OPEN) && rendered.includes(PSEUDO_CLOSE);
}
