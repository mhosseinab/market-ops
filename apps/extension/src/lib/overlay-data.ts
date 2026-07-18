import type { components } from "@market-ops/gen-ts";
import { FRESHNESS_AGING_MAX_MINUTES, FRESHNESS_FRESH_MAX_MINUTES } from "@market-ops/locale";

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
// `freshnessBucketOf` reads the SAME shared thresholds
// (packages/locale/src/freshness.ts) as Market.tsx's FreshnessPill so the
// overlay's freshness bucket is byte-identical to what a human sees on the
// Market screen for the same offer (overlay-parity contract test) — a SINGLE
// source of truth, not a duplicated magic number that could silently drift.
export type FreshnessBucket = "fresh" | "aging" | "stale";

export interface OverlayView {
  readonly targetId: string;
  readonly offerCount: number;
  readonly sellerCount: number;
  /** Raw evidence only — never a Money. Null when no in-stock/limited offer exists. */
  readonly lowestQualifying: components["schemas"]["RawAmount"] | null;
  readonly freshness: FreshnessBucket | null;
  readonly quality: components["schemas"]["ObservedOffer"]["quality"] | null;
}

const QUALIFYING_AVAILABILITY = new Set(["in_stock", "limited"]);

// freshnessBucketOf uses the SHARED FRESHNESS_*_MAX_MINUTES constants —
// exactly what apps/web's FreshnessPill compares `ageMinutes` against.
export function freshnessBucketOf(capturedAtIso: string, nowMs: number): FreshnessBucket {
  const ageMinutes = (nowMs - Date.parse(capturedAtIso)) / 60_000;
  if (ageMinutes <= FRESHNESS_FRESH_MAX_MINUTES) return "fresh";
  if (ageMinutes <= FRESHNESS_AGING_MAX_MINUTES) return "aging";
  return "stale";
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
    offerCount: offers.length,
    sellerCount: sellerIds.size,
    lowestQualifying: lowest?.price ?? null,
    freshness: primary ? freshnessBucketOf(primary.capturedAt, nowMs) : null,
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
