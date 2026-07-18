import { describe, expect, it } from "vitest";
import { validateAgainstDom } from "./dom-validate";
import type { ParsedProduct } from "./types";

function product(overrides: Partial<ParsedProduct> = {}): ParsedProduct {
  return {
    nativeProductId: 2345678,
    canonicalUrl: "https://www.digikala.com/product/dkp-2345678/",
    title: "کالا",
    availability: "in_stock",
    offer: { nativeVariantId: 987654321 },
    parserVersion: "dk-product@1.0.0",
    ...overrides,
  };
}

function doc(html: string): Document {
  return new DOMParser().parseFromString(
    `<!doctype html><html><head>${html}</head><body>${html}</body></html>`,
    "text/html",
  );
}

describe("validateAgainstDom (DOM is a validation layer only — docs/06)", () => {
  it("no drift when the canonical link matches and no unavailable badge is present", () => {
    const d = doc(`<link rel="canonical" href="https://www.digikala.com/product/dkp-2345678/x/">`);
    const s = validateAgainstDom(d, product());
    expect(s.urlMismatch).toBe(false);
    expect(s.availabilityMismatch).toBe(false);
  });

  it("flags a canonical URL mismatch (the page shows a different product)", () => {
    const d = doc(`<link rel="canonical" href="https://www.digikala.com/product/dkp-9999999/y/">`);
    expect(validateAgainstDom(d, product()).urlMismatch).toBe(true);
  });

  it("flags an availability mismatch when the exact ناموجود badge contradicts the API", () => {
    const d = doc(
      `<link rel="canonical" href="https://www.digikala.com/product/dkp-2345678/x/"><span>ناموجود</span>`,
    );
    expect(validateAgainstDom(d, product({ availability: "in_stock" })).availabilityMismatch).toBe(
      true,
    );
  });
});
