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
    expect(view.lowestQualifying?.value).toBe("120000");
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

  it("lowestQualifying is null when NO offer qualifies — never a fabricated zero/placeholder price", () => {
    const offers = [offer({ availabilityStatus: "unavailable" })];
    const view = deriveOverlayView(target(), offers, NOW);
    expect(view.lowestQualifying).toBeNull();
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
      lowestQualifying: null,
      freshness: null,
      quality: null,
    });
  });
});
