import { describe, expect, it } from "vitest";
import { REGION_IR } from "../config";
import fixtures from "./money.fixtures.json";
import { renderMoney } from "./money";

// Derive the `en`-locale rendering from the locale-independent `rawDecimal`
// (ASCII '-' and '.') that both TS and Go agree on: U+2212 minus, comma-grouped
// integer part, unchanged fractional part. String arithmetic only — no float.
function enFromRawDecimal(rawDecimal: string): string {
  const neg = rawDecimal.startsWith("-");
  const unsigned = neg ? rawDecimal.slice(1) : rawDecimal;
  const [intPart, fracPart] = unsigned.split(".");
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const sign = neg ? "−" : "";
  return fracPart ? `${sign}${grouped}.${fracPart}` : `${sign}${grouped}`;
}

// §9.1 / LOC-008: the region display transform is UNVERIFIED today, so the money
// renderer shows the exact SOURCE unit only and NEVER infers Toman. A currency
// mismatch quarantines rather than inferring.
describe("money renderer (source-unit only, §9.1)", () => {
  it("renders the exact source unit with the rial label key", () => {
    const r = renderMoney(
      { mantissa: 145000000n, currency: "IRR", exponent: 0 },
      REGION_IR,
      "fa-IR",
    );
    expect(r.mode).toBe("source");
    expect(r.quarantined).toBe(false);
    expect(r.unitKey).toBe("unit.rial");
    expect(r.amount).toBe("۱۴۵٬۰۰۰٬۰۰۰");
  });

  it("never divides to Toman while the transform is unverified", () => {
    expect(REGION_IR.displayTransform.verified).toBe(false);
    const r = renderMoney({ mantissa: 100n, currency: "IRR", exponent: 0 }, REGION_IR, "fa-IR");
    // 100 rial stays 100 — not 10 toman.
    expect(r.amount).toBe("۱۰۰");
    expect(r.unitKey).not.toBe("unit.toman");
  });

  it("renders negative amounts with a real minus sign", () => {
    const r = renderMoney({ mantissa: -14100000n, currency: "IRR", exponent: 0 }, REGION_IR, "en");
    expect(r.amount).toBe("−14,100,000");
  });

  it("quarantines a currency that does not match the region source unit", () => {
    const r = renderMoney({ mantissa: 5n, currency: "USD", exponent: 2 }, REGION_IR, "fa-IR");
    expect(r.quarantined).toBe(true);
    expect(r.unitKey).toBe("money.quarantined");
    expect(r.amount).toBe("");
  });

  // Canonical MONEY CORRECTNESS (PRD §4.6 / §9.1): Value = mantissa × 10^exponent.
  // Positive exponent scales UP; negative exponent places the decimal point.
  it("scales a positive exponent UP without floating point", () => {
    const r = renderMoney({ mantissa: 12345n, currency: "IRR", exponent: 2 }, REGION_IR, "en");
    expect(r.amount).toBe("1,234,500");
  });

  it("places the decimal point for a negative exponent", () => {
    const r = renderMoney({ mantissa: 12345n, currency: "IRR", exponent: -2 }, REGION_IR, "en");
    expect(r.amount).toBe("123.45");
  });

  it("left-pads the fractional part with leading zeros", () => {
    const r = renderMoney({ mantissa: 5n, currency: "IRR", exponent: -2 }, REGION_IR, "en");
    expect(r.amount).toBe("0.05");
  });

  it("renders a negative mantissa with a negative exponent using U+2212", () => {
    const r = renderMoney({ mantissa: -12345n, currency: "IRR", exponent: -2 }, REGION_IR, "en");
    expect(r.amount).toBe("−123.45");
  });

  it("keeps exact bigint precision at int64-max magnitude (no float rounding)", () => {
    const r = renderMoney(
      { mantissa: 9223372036854775807n, currency: "IRR", exponent: -2 },
      REGION_IR,
      "en",
    );
    expect(r.amount).toBe("92,233,720,368,547,758.07");
  });

  it("groups a positive-exponent value in the fa-IR digit family after scaling up", () => {
    const r = renderMoney({ mantissa: 12345n, currency: "IRR", exponent: 2 }, REGION_IR, "fa-IR");
    // 12345 × 10^2 = 1,234,500 → Persian digits with Persian grouping separator.
    expect(r.amount).toBe("۱٬۲۳۴٬۵۰۰");
  });

  it("matches the shared canonical conformance fixture (en) byte-for-byte", () => {
    for (const v of fixtures.vectors) {
      const r = renderMoney(
        { mantissa: BigInt(v.mantissa), currency: v.currency, exponent: v.exponent },
        REGION_IR,
        "en",
      );
      expect(r.quarantined, v.name).toBe(false);
      // The formatter's `en` output equals the value derived from rawDecimal…
      expect(r.amount, v.name).toBe(enFromRawDecimal(v.rawDecimal));
      // …and the fixture's own expectedEn stays in sync with rawDecimal.
      expect(v.expectedEn, v.name).toBe(enFromRawDecimal(v.rawDecimal));
    }
  });
});
