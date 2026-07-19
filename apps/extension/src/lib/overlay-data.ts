import type { components } from "@market-ops/gen-ts";
import { type FreshnessState, freshnessState } from "@market-ops/locale";

export type ObservedOffer = components["schemas"]["ObservedOffer"];
export type ObservationTarget = components["schemas"]["ObservationTarget"];

// Overlay view model (EXT-005): offers, seller count, lowest qualifying offer,
// freshness, and quality. Every field here is RENDERED from data the gateway
// already returns for the account's own Observed Offers — the SAME rows the
// Market screen (apps/web/src/screens/Market.tsx) reads from
// `/observation/observed-offers` + `/observation/targets`. This module does the
// SAME kind of presentational aggregation Market.tsx does client-side (row
// counts, freshness buckets) — it NEVER derives a Money, a margin, or any
// commercial value; price stays raw evidence (RawAmount), never promoted.
//
// `freshnessBucketOf` DELEGATES to the shared, deadline-driven `freshnessState`
// (packages/locale/src/freshness.ts) — the SAME derivation Market.tsx's
// FreshnessPill uses — passing the offer's OWN authoritative `freshnessDeadline`
// (a REQUIRED field on ObservedOffer, OBS-004). So the overlay's freshness
// bucket is byte-identical to what a human sees on the Market screen for the
// same offer at the same instant (overlay-parity test) — a SINGLE source of
// truth, never a duplicated threshold that could silently drift, and a
// priority-tier offer flips to Stale at ITS deadline, not a fixed six hours.
export type FreshnessBucket = FreshnessState;

export interface OverlayView {
  readonly targetId: string;
  // The gateway-generated STRING id (ObservationTarget.variantId) — the SAME
  // id apps/web/src/screens/ProductDetail.tsx resolves against
  // (`targetsQuery.data.items.find(tg => tg.variantId === variantId)`) and
  // the id `/product?variantId=` deep links expect. This is DISTINCT from
  // `nativeVariantId`/`nativeProductId` (DK's own numeric ids) — never
  // interchange them (EXT-008: a deep link built from the wrong id space
  // resolves nothing).
  readonly variantId: string;
  readonly offerCount: number;
  readonly sellerCount: number;
  /** Raw evidence only — never a Money. Null when no in-stock/limited offer exists. */
  readonly lowestQualifying: components["schemas"]["RawAmount"] | null;
  readonly freshness: FreshnessBucket | null;
  readonly quality: components["schemas"]["ObservedOffer"]["quality"] | null;
}

const QUALIFYING_AVAILABILITY = new Set(["in_stock", "limited"]);

// freshnessBucketOf DELEGATES to the shared deadline-driven `freshnessState`,
// passing the offer's OWN freshnessDeadline — exactly what apps/web's
// FreshnessPill derives. No fixed-threshold fork remains for these
// deadline-carrying offers.
export function freshnessBucketOf(offer: ObservedOffer, nowMs: number): FreshnessBucket {
  return freshnessState(offer, nowMs);
}

// deriveOverlayView filters the account's Observed Offers down to the one
// target and renders EXACTLY what the server already computed — never invents
// a value, never fabricates a price for an unavailable offer.
export function deriveOverlayView(
  target: ObservationTarget,
  allOffers: readonly ObservedOffer[],
  nowMs: number,
): OverlayView {
  const offers = allOffers.filter((o) => o.targetId === target.id && !o.endedAt);
  const sellerIds = new Set(offers.map((o) => o.nativeSellerId).filter((s): s is string => !!s));

  const qualifying = offers.filter((o) => QUALIFYING_AVAILABILITY.has(o.availabilityStatus));
  const lowest = qualifying.reduce<ObservedOffer | null>((best, o) => {
    if (best === null) return o;
    return bigIntOf(o.price.value) < bigIntOf(best.price.value) ? o : best;
  }, null);

  // "Representative" offer for freshness/quality is the most-recently captured
  // one — the same row Market.tsx's `offerByTarget` map keeps (first-wins over
  // an already-ordered list from the server).
  const primary = offers[0] ?? null;

  return {
    targetId: target.id,
    variantId: target.variantId,
    offerCount: offers.length,
    sellerCount: sellerIds.size,
    lowestQualifying: lowest?.price ?? null,
    freshness: primary ? freshnessBucketOf(primary, nowMs) : null,
    quality: primary?.quality ?? null,
  };
}

function bigIntOf(raw: string): bigint {
  // Raw price evidence is a digit-string (docs/11); a non-numeric value never
  // silently wins a "lowest" comparison — it sorts last (quarantine posture).
  try {
    return BigInt(raw);
  } catch {
    return BigInt(Number.MAX_SAFE_INTEGER) * 1_000_000n;
  }
}
