import { describe, expect, it } from "vitest";
import available from "../test/fixtures/product-available.json";
import unavailable from "../test/fixtures/product-unavailable.json";
import { PARSER_VERSION, RIAL_UNIT } from "./constants";
import { parseProductResponse } from "./parse";
import { containsSecretKey } from "./redact";

describe("parseProductResponse (docs/06 selector contract, golden fixtures)", () => {
  it("maps an available product to a normalized, allow-listed capture with the parser version stamped", () => {
    const result = parseProductResponse(available);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const p = result.product;
    expect(p.nativeProductId).toBe(2345678);
    expect(p.canonicalUrl).toBe("https://www.digikala.com/product/dkp-2345678/");
    expect(p.availability).toBe("in_stock");
    expect(p.parserVersion).toBe(PARSER_VERSION);
    expect(p.offer?.nativeVariantId).toBe(987654321);
    expect(p.offer?.nativeSellerId).toBe("4321");
    // Money is RAW Rial evidence — verbatim integer string, unit IRR-rial, no
    // arithmetic and no Toman conversion in the extension.
    expect(p.offer?.price).toEqual({
      text: `125000000 ${RIAL_UNIT}`,
      value: "125000000",
      unit: RIAL_UNIT,
    });
    expect(p.offer?.listPrice?.value).toBe("139000000");
    expect(p.offer?.stockSignal).toBe(7);
  });

  it("models an unavailable product with NO invented price (docs/10 step 3)", () => {
    const result = parseProductResponse(unavailable);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.product.availability).toBe("out_of_stock");
    expect(result.product.offer).toBeNull();
  });

  it("NEVER carries reviewer user_name or question sender into the parsed product (docs/12)", () => {
    const result = parseProductResponse(available);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    // The parsed product allow-lists fields; no name-like/secret key survives.
    expect(containsSecretKey(result.product)).toBe(false);
    const serialized = JSON.stringify(result.product);
    expect(serialized).not.toContain("کاربر دیجی‌کالا"); // review user_name
    expect(serialized).not.toContain("پرسنده"); // question sender
    expect(serialized).not.toContain("user_name");
    expect(serialized).not.toContain("sender");
  });

  it("reports response key-set drift instead of guessing (docs/14, §10.4)", () => {
    expect(parseProductResponse({}).ok).toBe(false);
    expect(parseProductResponse({ data: {} }).ok).toBe(false);
    const marketableNoVariants = {
      data: {
        product: { id: 1, status: "marketable", url: { uri: "/product/dkp-1/x/" }, variants: [] },
      },
    };
    const r = parseProductResponse(marketableNoVariants);
    expect(r.ok).toBe(false);
  });
});
