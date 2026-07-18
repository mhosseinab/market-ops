import { describe, expect, it } from "vitest";
import {
  canonicalProductUrl,
  normalizePersian,
  productIdFromPath,
  rialFromInteger,
  stripThousands,
  toAsciiDigits,
} from "./normalize";

describe("normalization (docs/11)", () => {
  it("converts Persian/Arabic digits to ASCII before parsing", () => {
    expect(toAsciiDigits("۱۲۳۴۵")).toBe("12345");
    expect(toAsciiDigits("٤٥٦")).toBe("456");
  });

  it("strips thousands separators (ASCII + Persian)", () => {
    expect(stripThousands("1٬200٬000")).toBe("1200000");
    expect(stripThousands("1,200,000")).toBe("1200000");
  });

  it("folds Arabic letter variants and trims direction marks", () => {
    expect(normalizePersian("كیفیت‎")).toBe("کیفیت");
  });

  it("preserves RAW Rial money verbatim (no arithmetic, unit IRR-rial)", () => {
    expect(rialFromInteger(125000000)).toEqual({
      text: "125000000 IRR-rial",
      value: "125000000",
      unit: "IRR-rial",
    });
  });

  it("slug-strips product URLs to /product/dkp-{id}/ and drops the query", () => {
    expect(canonicalProductUrl("https://www.digikala.com/product/dkp-2345678/some-slug/?a=1")).toBe(
      "https://www.digikala.com/product/dkp-2345678/",
    );
    expect(canonicalProductUrl("/search/category-mobile/")).toBeNull();
  });

  it("extracts a product id ONLY from an explicit product path (EXT-002 scope)", () => {
    expect(productIdFromPath("/product/dkp-2345678/x/")).toBe(2345678);
    expect(productIdFromPath("/search/category-mobile/")).toBeNull();
    expect(productIdFromPath("/")).toBeNull();
  });
});
