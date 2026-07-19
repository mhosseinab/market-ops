import { freshnessState } from "@market-ops/locale";
import { describe, expect, it } from "vitest";
import {
  deriveOverlayView,
  freshnessBucketOf,
  type ObservationTarget,
  type ObservedOffer,
} from "./overlay-data";

const NOW = Date.parse("2026-07-18T12:00:00Z");

function target(): ObservationTarget {
  return {
    id: "t1",
    marketplaceAccountId: "acct-1",
    identityId: "identity-1",
    variantId: "variant-1",
    nativeVariantId: 111,
    nativeProductId: 222,
    tier: "priority",
    cadenceSeconds: 3600,
    freshnessDeadlineSeconds: 3600,
    active: true,
  };
}

function offer(overrides: Partial<ObservedOffer>): ObservedOffer {
  return {
    id: `id-${Math.random()}`,
    targetId: "t1",
    marketplaceAccountId: "acct-1",
    offerIdentity: "111:seller-a",
    nativeVariantId: 111,
    nativeSellerId: "seller-a",
    price: { text: "100000 IRR-rial", value: "100000", unit: "IRR-rial" },
    listPrice: { text: "110000 IRR-rial", value: "110000", unit: "IRR-rial" },
    availabilityStatus: "in_stock",
    quality: "verified",
    capturedAt: "2026-07-18T11:50:00Z",
    freshnessDeadline: "2026-07-18T12:50:00Z",
    routes: ["route_b"],
    ...overrides,
  };
}

describe("overlay-data — EXT-005 rendered, never recomputed", () => {
  it("counts offers/sellers, picks the lowest QUALIFYING raw price, never a fabricated Money", () => {
    const offers = [
      offer({
        id: "1",
        nativeSellerId: "seller-a",
        price: { text: "x", value: "150000", unit: "IRR-rial" },
      }),
      offer({
        id: "2",
        nativeSellerId: "seller-b",
        price: { text: "x", value: "120000", unit: "IRR-rial" },
      }),
      // Not qualifying (out_of_stock) — must never win "lowest qualifying" even
      // though its raw price is smaller.
      offer({
        id: "3",
        nativeSellerId: "seller-c",
        availabilityStatus: "out_of_stock",
        price: { text: "x", value: "1", unit: "IRR-rial" },
      }),
    ];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.offerCount).toBe(3);
    expect(view.sellerCount).toBe(3);
    expect(view.lowestQualifying).toEqual({
      kind: "single",
      amount: { text: "x", value: "120000", unit: "IRR-rial" },
    });
  });

  it("carries the gateway-generated variantId (distinct from nativeVariantId/nativeProductId) — EXT-008 deep-link id space", () => {
    const view = deriveOverlayView(target(), [], NOW);
    expect(view.variantId).toBe("variant-1");
  });

  it("excludes offers for OTHER targets and offers that have disappeared (endedAt set)", () => {
    const offers = [
      offer({ id: "1", targetId: "other-target" }),
      offer({ id: "2", endedAt: "2026-07-18T10:00:00Z" }),
      offer({ id: "3" }),
    ];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.offerCount).toBe(1);
  });

  it("lowestQualifying is an explicit 'none' when NO offer qualifies — never a fabricated zero/placeholder price", () => {
    const offers = [offer({ availabilityStatus: "unavailable" })];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.lowestQualifying).toEqual({ kind: "none" });
  });

  // Issue #157 — quarantine over inference (PRD §9.1 / §4.6). Raw prices carry a
  // validation-gated, un-canonicalized source unit; ordering across incompatible
  // units would fabricate a commercial ranking. The overlay must report the
  // comparison unavailable, NEVER coerce one cross-unit minimum.
  it("NEGATIVE: two qualifying offers with incompatible source units are NEVER cross-compared — explicit conflicted state, no fabricated minimum", () => {
    const offers = [
      offer({
        id: "1",
        nativeSellerId: "seller-a",
        price: { text: "100 toman", value: "100", unit: "toman" },
      }),
      offer({
        id: "2",
        nativeSellerId: "seller-b",
        price: { text: "900 IRR-rial", value: "900", unit: "IRR-rial" },
      }),
    ];
    const view = deriveOverlayView(target(), offers, NOW);
    // Must not order 100 toman "below" 900 rial (or vice versa).
    expect(view.lowestQualifying.kind).toBe("conflicted");
    if (view.lowestQualifying.kind === "conflicted") {
      expect([...view.lowestQualifying.units].sort()).toEqual(["IRR-rial", "toman"]);
    }
  });

  it("same-unit values order EXACTLY via BigInt beyond Number.MAX_SAFE_INTEGER — no float/Number conversion", () => {
    // 9007199254740993 vs 9007199254740992: adjacent integers straddling 2^53
    // that collapse to the same float. BigInt keeps them distinct.
    const lower = "9007199254740992";
    const higher = "9007199254740993";
    const offers = [
      offer({
        id: "1",
        nativeSellerId: "a",
        price: { text: higher, value: higher, unit: "IRR-rial" },
      }),
      offer({
        id: "2",
        nativeSellerId: "b",
        price: { text: lower, value: lower, unit: "IRR-rial" },
      }),
    ];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.lowestQualifying).toEqual({
      kind: "single",
      amount: { text: lower, value: lower, unit: "IRR-rial" },
    });
  });

  it("an unknown/blank source unit is quarantined — comparison unavailable, never inferred as compatible", () => {
    const offers = [
      offer({ id: "1", nativeSellerId: "a", price: { text: "100", value: "100", unit: "" } }),
      offer({ id: "2", nativeSellerId: "b", price: { text: "200", value: "200", unit: "" } }),
    ];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.lowestQualifying.kind).toBe("conflicted");
  });

  it("single-unit result preserves the winning offer's ORIGINAL raw text/value/unit evidence verbatim", () => {
    const offers = [
      offer({
        id: "1",
        nativeSellerId: "a",
        price: { text: "۱۲۰٬۰۰۰ تومان", value: "120000", unit: "toman" },
      }),
      offer({
        id: "2",
        nativeSellerId: "b",
        price: { text: "۱۵۰٬۰۰۰ تومان", value: "150000", unit: "toman" },
      }),
    ];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.lowestQualifying).toEqual({
      kind: "single",
      amount: { text: "۱۲۰٬۰۰۰ تومان", value: "120000", unit: "toman" },
    });
  });

  it("freshnessBucketOf DELEGATES to the shared freshnessState — overlay bucket === Market derivation for the same offer/instant (EXT-005)", () => {
    const o = offer({
      capturedAt: "2026-07-18T11:00:00Z",
      freshnessDeadline: "2026-07-18T17:00:00Z",
    });
    expect(freshnessBucketOf(o, NOW)).toBe(freshnessState(o, NOW));
    const later = Date.parse("2026-07-18T16:30:00Z");
    expect(freshnessBucketOf(o, later)).toBe(freshnessState(o, later));
  });

  it("a PRIORITY-tier offer (60m deadline) flips to Stale AT its own deadline — NOT held fresh by a fixed six-hour threshold (OBS-004)", () => {
    // Captured at 12:00, priority deadline 60m later at 13:00. A fixed 6h
    // threshold would call this offer aging/fresh well past its deadline; the
    // authoritative derivation calls it STALE at 13:00.
    const priority = offer({
      capturedAt: "2026-07-18T12:00:00Z",
      freshnessDeadline: "2026-07-18T13:00:00Z",
    });
    expect(freshnessBucketOf(priority, Date.parse("2026-07-18T12:59:59Z"))).toBe("aging");
    expect(freshnessBucketOf(priority, Date.parse("2026-07-18T13:00:00Z"))).toBe("stale");
    expect(freshnessBucketOf(priority, Date.parse("2026-07-18T13:00:01Z"))).toBe("stale");
  });

  it("an empty offer set yields no freshness/quality/lowest — honest absence, never a guess", () => {
    const view = deriveOverlayView(target(), [], NOW);
    expect(view).toEqual({
      targetId: "t1",
      variantId: "variant-1",
      offerCount: 0,
      sellerCount: 0,
      lowestQualifying: { kind: "none" },
      freshness: null,
      quality: null,
    });
  });
});
