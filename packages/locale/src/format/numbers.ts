import { LOCALE_PACKS, type LocaleId } from "../config";

// Number formatting through `Intl.NumberFormat` only (LOCALIZATION.md step 3).
// The output digit family and grouping separator come from the locale's
// `formatLocale` (…-nu-arabext for fa-IR) — never hand-mapped, never
// concatenated. bigint is supported end-to-end so integer money mantissas never
// touch a float.

const nfCache = new Map<string, Intl.NumberFormat>();

function integerFormatter(locale: LocaleId): Intl.NumberFormat {
  const tag = LOCALE_PACKS[locale].formatLocale;
  let nf = nfCache.get(tag);
  if (!nf) {
    nf = new Intl.NumberFormat(tag, { useGrouping: true, maximumFractionDigits: 0 });
    nfCache.set(tag, nf);
  }
  return nf;
}

/** Format an integer (bigint or safe number) with locale grouping + digits. */
export function formatInteger(value: bigint | number, locale: LocaleId): string {
  return integerFormatter(locale).format(value);
}

/** The locale's output digit glyphs (index 0-9) and decimal separator glyph. */
export interface LocaleGlyphs {
  readonly digits: readonly string[];
  readonly decimal: string;
}

const glyphCache = new Map<string, LocaleGlyphs>();

export function localeGlyphs(locale: LocaleId): LocaleGlyphs {
  const tag = LOCALE_PACKS[locale].formatLocale;
  let g = glyphCache.get(tag);
  if (!g) {
    const plain = new Intl.NumberFormat(tag, { useGrouping: false });
    const digits = Array.from({ length: 10 }, (_, d) => plain.format(d));
    const decimal =
      new Intl.NumberFormat(tag, { minimumFractionDigits: 1 })
        .formatToParts(1.1)
        .find((p) => p.type === "decimal")?.value ?? ".";
    g = { digits, decimal };
    glyphCache.set(tag, g);
  }
  return g;
}

/** Map a Latin-digit string to the locale's output digit family. */
export function toOutputDigits(latin: string, locale: LocaleId): string {
  const { digits } = localeGlyphs(locale);
  let out = "";
  for (const ch of latin) {
    const d = ch.charCodeAt(0) - 0x30;
    out += d >= 0 && d <= 9 ? digits[d] : ch;
  }
  return out;
}

/**
 * Format a percentage held as fixed-point basis points (1% = 100 bp), never a
 * float — rates/percentages are basis points per §9.1. Renders up to two
 * fractional digits in the locale's digit family.
 */
export function formatBasisPoints(basisPoints: number, locale: LocaleId): string {
  const neg = basisPoints < 0;
  const abs = Math.abs(Math.trunc(basisPoints));
  const whole = Math.trunc(abs / 100);
  const frac = abs % 100;
  const sign = neg ? "−" : ""; // U+2212 minus sign
  const wholeText = formatInteger(whole, locale);
  if (frac === 0) return `${sign}${wholeText}`;
  const { decimal } = localeGlyphs(locale);
  const fracText = toOutputDigits(String(frac).padStart(2, "0"), locale);
  return `${sign}${wholeText}${decimal}${fracText}`;
}
