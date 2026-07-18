import { describe, expect, it } from "vitest";
import { REGION_IR } from "../config";
import { renderMoney } from "./money";

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

  it("scales exponent without floating point", () => {
    const r = renderMoney({ mantissa: 12345n, currency: "IRR", exponent: 2 }, REGION_IR, "en");
    expect(r.amount).toBe("123.45");
  });
});
