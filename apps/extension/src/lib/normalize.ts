// Normalization rules (docs/11). These run at the capture boundary so stored
// values are canonical: ASCII digits for parsing, NFC Persian text, and RAW Rial
// money preserved as evidence (never converted, never floated — money is
// quarantined to the server).

const PERSIAN_DIGITS = "۰۱۲۳۴۵۶۷۸۹";
const ARABIC_DIGITS = "٠١٢٣٤٥٦٧٨٩";

// toAsciiDigits converts Persian/Arabic-Indic digits to ASCII BEFORE parsing
// (docs/11 "Digits"). Non-digit characters are left untouched.
export function toAsciiDigits(input: string): string {
  let out = "";
  for (const ch of input) {
    const p = PERSIAN_DIGITS.indexOf(ch);
    if (p >= 0) {
      out += String(p);
      continue;
    }
    const a = ARABIC_DIGITS.indexOf(ch);
    if (a >= 0) {
      out += String(a);
      continue;
    }
    out += ch;
  }
  return out;
}

// normalizePersian applies NFC and folds the Arabic ي→ی and ك→ک variants
// (docs/11 "Unicode"), and trims LRM/RLM direction marks that must never be
// stored as canonical text (docs/11 "Direction controls").
export function normalizePersian(input: string): string {
  return input.normalize("NFC").replace(/ي/g, "ی").replace(/ك/g, "ک").replace(/[‎‏]/g, "").trim();
}

// stripThousands removes DOM thousands separators (ASCII/Persian) so a numeric
// string parses; API integers are preferred and need no stripping (docs/11).
export function stripThousands(input: string): string {
  return toAsciiDigits(input).replace(/[,٬،\s]/g, "");
}

// rialFromInteger builds a RAW money evidence triple from a DK integer Rial
// amount. The value is preserved verbatim as a string; the extension NEVER does
// money arithmetic or unit conversion (that is the server's quarantined path).
export function rialFromInteger(amount: number): { text: string; value: string; unit: string } {
  const value = String(Math.trunc(amount));
  return { text: `${value} IRR-rial`, value, unit: "IRR-rial" };
}

// canonicalProductUrl slug-strips a DK product URL to /product/dkp-{id}/ and
// drops the query (docs/11 "Product URLs"). Returns null when the input is not a
// product URL.
export function canonicalProductUrl(rawUrl: string): string | null {
  let u: URL;
  try {
    u = new URL(rawUrl, "https://www.digikala.com");
  } catch {
    return null;
  }
  const m = u.pathname.match(/\/product\/dkp-(\d+)\b/);
  if (!m) return null;
  return `https://www.digikala.com/product/dkp-${m[1]}/`;
}

// productIdFromPath extracts the numeric DK product id from a product path, or
// null when the path is not an explicit product page (EXT-002 scope guard).
export function productIdFromPath(pathname: string): number | null {
  const m = pathname.match(/\/product\/dkp-(\d+)\//);
  if (!m || m[1] === undefined) return null;
  const id = Number(m[1]);
  return Number.isSafeInteger(id) ? id : null;
}
